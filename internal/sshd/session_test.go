package sshd

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/rhizomatous/jardiniere/internal/api"
)

// exercised is what a session handed to the service, captured from the fake.
type exercised struct {
	ref     api.Ref
	req     api.ExecRequest
	firstSz api.Size
	sawSize bool
}

// gateway stands up a real server over a real socket, with a fake service
// behind it. Everything but the sandbox is genuine: the routing header, the
// handshake, the pty request, and the env the client sets.
func gateway(t *testing.T, sandboxes ...api.Sandbox) (socket string, seen *exercised) {
	t.Helper()

	fake := api.NewFake(sandboxes...)
	seen = &exercised{}
	fake.OnExec = func(_ context.Context, ref api.Ref, req api.ExecRequest, streams api.Streams) (api.ExecResult, error) {
		seen.ref, seen.req = ref, req
		if streams.Resize != nil {
			select {
			case sz := <-streams.Resize:
				seen.firstSz, seen.sawSize = sz, true
			case <-time.After(time.Second):
			}
		}
		return api.ExecResult{ExitCode: 7}, nil
	}

	socket = filepath.Join(socketDir(t), "ssh.sock")
	srv, err := Serve(Options{
		Socket:      socket,
		HostKeyPath: filepath.Join(t.TempDir(), "hostkey"),
		Service:     fake,
	})
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return socket, seen
}

// dial opens an ssh client the way `jard ssh-proxy` does: the sandbox name,
// a newline, then the protocol.
func dial(t *testing.T, socket, host string) *gossh.Client {
	t.Helper()

	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if _, err := conn.Write([]byte(host + "\n")); err != nil {
		t.Fatalf("writing the route: %v", err)
	}

	cc, chans, reqs, err := gossh.NewClientConn(conn, host, &gossh.ClientConfig{
		User:            "agent",
		Auth:            []gossh.AuthMethod{gossh.KeyboardInteractive(noPrompts)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(), //nolint:gosec // the key is generated per test
		Timeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatalf("ssh handshake: %v", err)
	}
	client := gossh.NewClient(cc, chans, reqs)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func noPrompts(string, string, []string, []bool) ([]string, error) { return nil, nil }

func running(name string) api.Sandbox {
	return api.Sandbox{
		Spec: api.Spec{
			Name:       name,
			Workspaces: []api.Workspace{{Host: "/home/viv/" + name}},
		},
		State: api.State{Status: api.StatusRunning},
	}
}

// A pty opens at 0x0, and a full-screen program reads its size once. The
// client's size has to arrive before the session starts, or an agent lays
// itself out against nothing.
func TestSessionCarriesTheOpeningWindowSize(t *testing.T) {
	socket, seen := gateway(t, running("demo"))
	sess, err := dial(t, socket, "demo.jard").NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer func() { _ = sess.Close() }()

	if err := sess.RequestPty("xterm-256color", 40, 120, gossh.TerminalModes{}); err != nil {
		t.Fatalf("RequestPty: %v", err)
	}
	if err := sess.Run("true"); err != nil {
		var exit *gossh.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("Run: %v", err)
		}
	}

	if !seen.sawSize {
		t.Fatal("no window size reached the session; the pty would open at 0x0")
	}
	if seen.firstSz.Rows != 40 || seen.firstSz.Cols != 120 {
		t.Errorf("size = %dx%d, want 40x120", seen.firstSz.Rows, seen.firstSz.Cols)
	}
	if !seen.req.TTY {
		t.Error("a pty was requested but the exec did not ask for one")
	}
	if seen.req.Env["TERM"] != "xterm-256color" {
		t.Errorf("TERM = %q, want the client's", seen.req.Env["TERM"])
	}
}

func TestSessionRoutesToTheNamedSandbox(t *testing.T) {
	socket, seen := gateway(t, running("demo"), running("other"))
	sess, err := dial(t, socket, "other.jard").NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer func() { _ = sess.Close() }()
	_ = sess.Run("true")

	if seen.ref.Name != "other" {
		t.Errorf("ref = %+v, want the sandbox the hostname named", seen.ref)
	}
	if seen.req.User != agentUser {
		t.Errorf("user = %q, want %q whatever the client asked for", seen.req.User, agentUser)
	}
	if seen.req.Workdir != "/home/viv/other" {
		t.Errorf("workdir = %q, want the sandbox's primary workspace", seen.req.Workdir)
	}
}

// A client's exit status is the command's, so scripts over ssh behave.
func TestSessionReportsTheCommandsExitStatus(t *testing.T) {
	socket, _ := gateway(t, running("demo"))
	sess, err := dial(t, socket, "demo.jard").NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer func() { _ = sess.Close() }()

	err = sess.Run("false")
	var exit *gossh.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("Run err = %v, want an exit status", err)
	}
	if exit.ExitStatus() != 7 {
		t.Errorf("exit = %d, want the 7 the service reported", exit.ExitStatus())
	}
}

// The client sets what it likes; only what cannot redirect execution survives.
func TestSessionDropsEnvThatWouldRedirectExecution(t *testing.T) {
	socket, seen := gateway(t, running("demo"))
	sess, err := dial(t, socket, "demo.jard").NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer func() { _ = sess.Close() }()

	for k, v := range map[string]string{
		"LC_ALL":            "en_GB.UTF-8",
		"PATH":              "/tmp/evil",
		"NODE_OPTIONS":      "--require /tmp/evil.js",
		"ANTHROPIC_API_KEY": "sk-secret",
	} {
		if err := sess.Setenv(k, v); err != nil {
			// a server may refuse an env request outright, which is also fine.
			continue
		}
	}
	_ = sess.Run("true")

	if seen.req.Env["LC_ALL"] != "en_GB.UTF-8" {
		t.Errorf("LC_ALL = %q, want the client's locale through", seen.req.Env["LC_ALL"])
	}
	for _, refused := range []string{"PATH", "NODE_OPTIONS", "ANTHROPIC_API_KEY"} {
		if v, ok := seen.req.Env[refused]; ok {
			t.Errorf("%s reached the sandbox as %q", refused, v)
		}
	}
}

// A stopped sandbox is a thing to say, not a thing to hang on.
func TestSessionRefusesASandboxThatIsNotRunning(t *testing.T) {
	stopped := running("demo")
	stopped.State.Status = api.StatusStopped

	socket, seen := gateway(t, stopped)
	sess, err := dial(t, socket, "demo.jard").NewSession()
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer func() { _ = sess.Close() }()

	if err := sess.Run("true"); err == nil {
		t.Error("a session against a stopped sandbox succeeded")
	}
	if seen.req.Cmd != nil {
		t.Error("a stopped sandbox was still exec'd into")
	}
}

// Without a route there is nothing to serve, and the connection should not
// linger waiting for one.
func TestConnectionWithoutARouteIsRefused(t *testing.T) {
	socket, _ := gateway(t, running("demo"))

	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.Write([]byte("nosuch name\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	_, _, _, err = gossh.NewClientConn(conn, "demo", &gossh.ClientConfig{
		User:            "agent",
		Auth:            []gossh.AuthMethod{gossh.KeyboardInteractive(noPrompts)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(), //nolint:gosec // no key is exchanged here
		Timeout:         5 * time.Second,
	})
	if err == nil {
		t.Error("a connection naming no sandbox completed its handshake")
	}
}
