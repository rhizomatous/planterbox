package sshd

import (
	"errors"
	"io"
	"net"
	"strings"
	"time"

	"charm.land/ssh"

	"github.com/rhizomatous/planterbox/internal/api"
)

// Which sandbox a connection is for arrives ahead of the ssh bytes.
//
// It has to come from somewhere, and ssh itself offers nowhere to put it. The
// protocol never sends the hostname the client typed — it is used for the
// known-hosts lookup and then dropped — and the username cannot carry it
// either, because a single `Host *.plbx` block has no token to interpolate one
// from. So `plbx ssh-proxy` writes the name and a newline before it starts
// relaying, and this reads exactly that much before the handshake begins.
const (
	// routeDeadline caps how long a connection may sit having said nothing. A
	// client that never writes a header would otherwise hold a goroutine open.
	routeDeadline = 10 * time.Second
	// maxRouteLen bounds the header, so a client that writes no newline at all
	// is refused rather than read forever.
	maxRouteLen = 256
	// shutdownGrace is how long sessions in flight get when the gateway stops.
	shutdownGrace = 5 * time.Second
)

// sandboxKey is the context key the route is stored under.
type contextKey struct{}

var sandboxKey = contextKey{}

// route reads the sandbox name a proxy wrote, and records it on the connection.
func route(ctx ssh.Context, conn net.Conn) net.Conn {
	if err := conn.SetReadDeadline(time.Now().Add(routeDeadline)); err != nil {
		_ = conn.Close()
		return conn
	}
	name, err := readRoute(conn)
	if err != nil {
		_ = conn.Close()
		return conn
	}
	// the deadline was for the header only; a session is idle for long
	// stretches and must not be cut off for it.
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		_ = conn.Close()
		return conn
	}
	ctx.SetValue(sandboxKey, name)
	return conn
}

// readRoute reads up to the first newline, one byte at a time.
//
// Byte at a time on purpose: a buffered reader would pull ssh's opening bytes
// in with the header and there would be no way to give them back, since what
// is returned here is the connection the handshake then reads from.
func readRoute(conn net.Conn) (string, error) {
	var name []byte
	buf := make([]byte, 1)
	for len(name) < maxRouteLen {
		if _, err := io.ReadFull(conn, buf); err != nil {
			return "", err
		}
		if buf[0] == '\n' {
			return sandboxName(string(name))
		}
		name = append(name, buf[0])
	}
	return "", errors.New("no sandbox name before the ssh bytes")
}

// sandboxName reads the name out of what a proxy wrote, which is the hostname
// ssh was given: "myrepo.plbx", or "myrepo" if someone wrote it by hand.
func sandboxName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	name = strings.TrimSuffix(name, "."+api.SSHDomain)
	if name == "" {
		return "", errors.New("no sandbox named")
	}
	if !api.ValidName(name) {
		return "", errors.New("not a sandbox name: " + name)
	}
	return name, nil
}
