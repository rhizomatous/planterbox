package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"

	"github.com/rhizomatous/planterbox/internal/api/direct"
	"github.com/rhizomatous/planterbox/internal/api/rpc"
	"github.com/rhizomatous/planterbox/internal/proxy"
	"github.com/rhizomatous/planterbox/internal/sshd"
)

// ErrAlreadyRunning means a daemon is already listening on the socket.
var ErrAlreadyRunning = errors.New("a plbx daemon is already running")

// Options configures a daemon.
type Options struct {
	// Socket overrides where the daemon listens.
	Socket string
	// StateDir overrides where the daemon keeps sandbox records.
	StateDir string
	// ProxyAddr is where the egress proxy listens. Loopback by default: it is
	// reached from a sandbox through the relay, and binding it wider would
	// offer the whole LAN a way out through this machine.
	ProxyAddr string
	// RelayImage overrides the published relay, for testing against one built
	// locally.
	RelayImage string
	// Version is the build this daemon is, reported to clients so they can
	// tell whether it matches theirs.
	Version string
	// Ready, when set, is called once the daemon is listening. Tests use it to
	// learn when it is safe to connect.
	Ready func()
}

// DefaultProxyAddr is where the egress proxy listens unless told otherwise.
//
// A fixed port rather than an ephemeral one: a sandbox is given its proxy
// address when it is created, and that address has to still be right after the
// daemon restarts.
const DefaultProxyAddr = "127.0.0.1:47821"

// Serve runs the daemon until ctx is cancelled.
//
// It refuses to start alongside another daemon, and clears a socket left behind
// by one that died: a stale file is indistinguishable from a live one until you
// try to connect, so connecting is how it decides.
func Serve(ctx context.Context, opts Options) error {
	paths, err := resolvePaths(opts)
	if err != nil {
		return err
	}
	if err := ensureRuntimeDir(paths.socket); err != nil {
		return fmt.Errorf("preparing the runtime directory: %w", err)
	}
	if alive(ctx, paths.socket) {
		return fmt.Errorf("%w at %s", ErrAlreadyRunning, paths.socket)
	}
	// nothing answered, so whatever is at that path is a corpse.
	if err := os.Remove(paths.socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clearing a stale socket: %w", err)
	}

	// everything the daemon needs is stood up before the socket is, because
	// the socket is what tells the world it is ready. Bound first, a daemon
	// that then fails to start its proxy would spend its last moments accepting
	// connections it is about to drop, which a client sees as an unexplained
	// EOF rather than as the error that actually happened.
	connections := proxy.NewLog(0)
	svc, err := direct.Open(ctx, direct.Options{
		StateDir:      opts.StateDir,
		ConnectionLog: connections,
		EgressProxy:   relayUpstream(ProxyAddress(opts)),
		RelayImage:    opts.RelayImage,
	})
	if err != nil {
		return err
	}
	defer func() { _ = svc.Close() }()

	stopProxy, err := serveProxy(ctx, opts.ProxyAddr, svc, connections)
	if err != nil {
		return err
	}
	defer stopProxy()

	gateway, err := serveSSH(paths, svc)
	if err != nil {
		return err
	}
	defer func() { _ = gateway.Close() }()

	// the pidfile still goes down before the listener, so anything that finds
	// the socket answering can rely on the record being there.
	if err := writePid(paths); err != nil {
		return err
	}
	defer removePid(paths)

	lis, err := net.Listen("unix", paths.socket)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", paths.socket, err)
	}
	defer func() { _ = os.Remove(paths.socket) }()

	// the socket is an unauthenticated door onto every sandbox. Only its owner
	// gets to open it.
	if err := os.Chmod(paths.socket, 0o600); err != nil {
		_ = lis.Close()
		return fmt.Errorf("securing %s: %w", paths.socket, err)
	}

	server := grpc.NewServer()
	rpc.NewServer(svc, opts.Version).Register(server)

	served := make(chan error, 1)
	go func() { served <- server.Serve(lis) }()

	if opts.Ready != nil {
		opts.Ready()
	}

	select {
	case err := <-served:
		return err
	case <-ctx.Done():
		// let sessions in flight finish rather than cutting the terminal out
		// from under whoever is sitting in one.
		server.GracefulStop()
		return nil
	}
}

// alive reports whether a daemon answers on socket.
func alive(ctx context.Context, socket string) bool {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// Running reports the pid of the daemon answering on socket, and whether one
// does. A pidfile alone is not evidence: the process it names may be gone, or
// may be something else entirely by now.
func Running(ctx context.Context, env Env) (pid int, ok bool) {
	socket, err := Socket(env)
	if err != nil || !alive(ctx, socket) {
		return 0, false
	}
	path, err := PidPath(env)
	if err != nil {
		return 0, true // answering, but we cannot say as whom
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, true
	}
	pid, err = strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0, true
	}
	return pid, true
}

// paths holds the resolved locations where the daemon stores everything.
//
// Resolving once at startup ensures the pidfile and ssh socket sit beside
// whichever socket this daemon is using, so a daemon on a custom socket does
// not interfere with the default daemon's records.
type paths struct {
	socket string
	pid    string
	ssh    string
}

// resolvePaths resolves the socket, pidfile, and ssh gateway paths.
func resolvePaths(opts Options) (paths, error) {
	socket := opts.Socket
	if socket == "" {
		var err error
		if socket, err = Socket(HostEnv(runtime.GOOS)); err != nil {
			return paths{}, err
		}
	}
	socketDir := filepath.Dir(socket)

	pid := filepath.Join(socketDir, pidFile)

	return paths{
		socket: socket,
		pid:    pid,
		ssh:    filepath.Join(socketDir, sshFile),
	}, nil
}

func writePid(p paths) error {
	return os.WriteFile(p.pid, []byte(strconv.Itoa(os.Getpid())), 0o600)
}

func removePid(p paths) {
	_ = os.Remove(p.pid)
}

// serveProxy starts the egress proxy and returns a function that stops it.
//
// The policy is read from the service per request rather than captured, so
// `plbx policy allow` reaches every sandbox on its next connection.
func serveProxy(ctx context.Context, addr string, svc *direct.Service, log *proxy.Log) (func(), error) {
	if addr == "" {
		addr = DefaultProxyAddr
	}
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listening for sandbox egress on %s: %w", addr, err)
	}

	handler := proxy.NewServer(func() proxy.Policy {
		p, err := svc.Policy(ctx)
		if err != nil {
			// no policy chosen yet, or a store we could not read. Balanced is
			// the documented default and denies by default, so the failure
			// mode is a sandbox that cannot reach something rather than one
			// that can reach everything.
			return proxy.New(proxy.PresetBalanced)
		}
		return p
	}, proxy.WithLog(log))

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 30 * time.Second,
	}
	go func() { _ = srv.Serve(lis) }()

	return func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}, nil
}

// serveSSH starts the ssh gateway, which editors attach to sandboxes through.
//
// It listens on a unix socket rather than a port, so nothing about it is
// reachable from off this machine, and its sessions are execs rather than
// connections, which is what lets it reach a sandbox that has no route in.
func serveSSH(p paths, svc *direct.Service) (*sshd.Server, error) {
	return sshd.Serve(sshd.Options{
		Socket:      p.ssh,
		HostKeyPath: svc.SSHHostKeyPath(),
		Service:     svc,
	})
}

// ProxyAddress reports where a daemon's egress proxy listens.
func ProxyAddress(opts Options) string {
	if opts.ProxyAddr != "" {
		return opts.ProxyAddr
	}
	return DefaultProxyAddr
}

// relayUpstream rewrites the proxy's listen address into one the relay can
// reach it at.
//
// The proxy binds loopback, which means something entirely different inside the
// runtime: there, loopback is the container's own. The runtime publishes the
// host under a name of its own, and that is what the relay has to dial.
func relayUpstream(listen string) string {
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return listen
	}
	return net.JoinHostPort(runtimeHostAlias, port)
}

// runtimeHostAlias is what a container calls the machine the runtime runs on.
// Docker Desktop, OrbStack, and podman all publish this name.
const runtimeHostAlias = "host.docker.internal"
