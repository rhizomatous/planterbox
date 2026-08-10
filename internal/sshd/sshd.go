// Package sshd is jard's SSH gateway: it lets an editor attach to a sandbox
// over ssh without the sandbox listening on anything.
//
// There is no TCP port. The server listens on a unix socket beside the
// daemon's own, and `jard ssh-proxy` carries a client's bytes to it as an
// OpenSSH ProxyCommand. That is what makes the filesystem the access control:
// the socket is 0600 and owned by the user, so anything that can reach it is
// already running as them.
//
// A session is not a network connection to the sandbox either. It is an exec,
// the same one `jard exec` uses, so the gateway needs no route to a sandbox
// that by design has none.
package sshd

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"charm.land/ssh"
	"charm.land/wish/v2"
	gossh "golang.org/x/crypto/ssh"

	"github.com/rhizomatous/jardiniere/internal/api"
)

// Options configures the gateway.
type Options struct {
	// Socket is the unix socket to listen on.
	Socket string
	// HostKeyPath is where the server's identity is kept. It is generated on
	// first use and must persist: a key that changed on every daemon restart
	// would trip every client's known-hosts check.
	HostKeyPath string
	// Service is what sessions are run through.
	Service api.Service
}

// Server is the running gateway.
type Server struct {
	svc api.Service
	srv *ssh.Server
	lis net.Listener
}

// Serve starts the gateway and returns it. Call Close to stop.
func Serve(opts Options) (*Server, error) {
	if opts.Service == nil {
		return nil, errors.New("sshd needs a service to run sessions through")
	}
	key, err := hostKey(opts.HostKeyPath)
	if err != nil {
		return nil, err
	}

	s := &Server{svc: opts.Service}
	srv, err := wish.NewServer(
		wish.WithHostKeyPEM(key),
		// any key is accepted, because the key is not what authorises anything
		// here. Reaching the socket at all means being the user who owns it,
		// and no key would add to that. Refusing keyless auth still means a
		// client presents one, which is what an ssh client does anyway.
		wish.WithPublicKeyAuth(func(ssh.Context, ssh.PublicKey) bool { return true }),
		// and a client with no key at all still gets in, for the same reason.
		// Without this, someone who has never generated one is told there are
		// no supported authentication methods, which is a confusing way to
		// describe a socket they already have permission to open.
		wish.WithKeyboardInteractiveAuth(func(ssh.Context, gossh.KeyboardInteractiveChallenge) bool {
			return true
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("building the ssh gateway: %w", err)
	}
	srv.Handler = s.handle
	srv.ConnCallback = route
	// `ssh -L` reaches a service inside the sandbox, dialled from inside it.
	// See forward.go: the daemon has no route of its own to a sandbox.
	srv.LocalPortForwardingCallback = func(ssh.Context, string, uint32) bool { return true }
	srv.ChannelHandlers = map[string]ssh.ChannelHandler{
		"session":      ssh.DefaultSessionHandler,
		"direct-tcpip": s.directTCPIP,
	}
	// the other direction is refused. `ssh -R` would have the sandbox listening
	// on the host, which is the arrangement this whole design exists to avoid.
	srv.ReversePortForwardingCallback = func(ssh.Context, string, uint32) bool { return false }
	s.srv = srv

	lis, err := listen(opts.Socket)
	if err != nil {
		return nil, err
	}
	s.lis = lis

	go func() { _ = srv.Serve(lis) }()
	return s, nil
}

// Close stops the gateway, letting sessions in flight finish.
func (s *Server) Close() error {
	if s.srv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	return s.srv.Shutdown(ctx)
}

// listen opens the gateway's socket, clearing one left by a dead daemon.
func listen(socket string) (net.Listener, error) {
	if socket == "" {
		return nil, errors.New("sshd needs a socket to listen on")
	}
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return nil, fmt.Errorf("preparing %s: %w", filepath.Dir(socket), err)
	}
	if err := os.Remove(socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("clearing a stale ssh socket: %w", err)
	}
	lis, err := net.Listen("unix", socket)
	if err != nil {
		return nil, fmt.Errorf("listening for ssh on %s: %w", socket, err)
	}
	// the socket is the whole of the access control: it stands in for every
	// sandbox's shell, so only its owner opens it.
	if err := os.Chmod(socket, 0o600); err != nil {
		_ = lis.Close()
		return nil, fmt.Errorf("securing %s: %w", socket, err)
	}
	return lis, nil
}

// hostKey reads the server's identity, generating one on first use.
//
// ed25519 rather than RSA: it is small, every client since OpenSSH 6.5 speaks
// it, and there is no key size to get wrong.
func hostKey(path string) ([]byte, error) {
	if path == "" {
		return nil, errors.New("sshd needs somewhere to keep its host key")
	}
	switch data, err := os.ReadFile(path); {
	case err == nil:
		return data, nil
	case !errors.Is(err, os.ErrNotExist):
		return nil, fmt.Errorf("reading the ssh host key: %w", err)
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating an ssh host key: %w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("encoding the ssh host key: %w", err)
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("preparing %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return nil, fmt.Errorf("writing the ssh host key: %w", err)
	}
	return data, nil
}
