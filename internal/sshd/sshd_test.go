package sshd

import (
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// socketDir gives a directory short enough to put a unix socket in.
//
// t.TempDir() embeds the test's own name under $TMPDIR, and a nix shell points
// that at a generated directory — together they run past the ~104 byte cap the
// kernel puts on a socket path, and the bind fails with "invalid argument".
// The same cap is why the daemon's runtime directory is /tmp/planterbox-<uid>.
func socketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "plbx-test-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestSandboxName(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "myrepo.plbx", want: "myrepo"},
		{in: "myrepo", want: "myrepo"},
		{in: "  myrepo.plbx\r", want: "myrepo"},
		// a dot is legal in a sandbox name, so only the trailing suffix goes.
		{in: "plbx.myrepo.plbx", want: "plbx.myrepo"},
		{in: "", wantErr: true},
		{in: ".plbx", wantErr: true},
		{in: "../../etc/passwd", wantErr: true},
		{in: "has space", wantErr: true},
	} {
		t.Run(tc.in, func(t *testing.T) {
			got, err := sandboxName(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("sandboxName(%q) = %q, want an error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("sandboxName(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("sandboxName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// The header is read a byte at a time so the ssh bytes behind it are left for
// the handshake. Reading them here would leave no way to give them back.
func TestReadRouteLeavesWhatFollows(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _, _ = client.Close(), server.Close() })

	go func() {
		_, _ = client.Write([]byte("myrepo.plbx\nSSH-2.0-OpenSSH_9.0\r\n"))
	}()

	name, err := readRoute(server)
	if err != nil {
		t.Fatalf("readRoute: %v", err)
	}
	if name != "myrepo" {
		t.Fatalf("name = %q, want myrepo", name)
	}

	rest := make([]byte, len("SSH-2.0-OpenSSH_9.0\r\n"))
	if _, err := server.Read(rest); err != nil {
		t.Fatalf("reading what follows: %v", err)
	}
	if string(rest) != "SSH-2.0-OpenSSH_9.0\r\n" {
		t.Errorf("what followed the header = %q, want the ssh banner intact", rest)
	}
}

func TestReadRouteRefusesAHeaderThatNeverEnds(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _, _ = client.Close(), server.Close() })

	go func() {
		_, _ = client.Write([]byte(strings.Repeat("a", maxRouteLen+10)))
	}()
	if err := server.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}

	if _, err := readRoute(server); err == nil {
		t.Error("readRoute accepted a header with no end to it")
	}
}

// The variables worth refusing are the ones that change what runs, and the
// ones carrying credentials. Neither set can be enumerated, which is why this
// is an allowlist.
func TestAllowedEnv(t *testing.T) {
	env := allowedEnv([]string{
		"TERM=xterm-256color",
		"LC_ALL=en_GB.UTF-8",
		"COLORTERM=truecolor",
		"PATH=/tmp/evil:/usr/bin",
		"NODE_OPTIONS=--require /tmp/evil.js",
		"LD_PRELOAD=/tmp/evil.so",
		"ANTHROPIC_API_KEY=sk-secret",
		"AWS_SECRET_ACCESS_KEY=secret",
		"GITHUB_TOKEN=ghp_secret",
		"BASH_ENV=/tmp/evil",
	})

	want := map[string]string{
		"TERM":      "xterm-256color",
		"LC_ALL":    "en_GB.UTF-8",
		"COLORTERM": "truecolor",
	}
	if len(env) != len(want) {
		t.Fatalf("env = %v, want only %v", env, want)
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("env[%q] = %q, want %q", k, env[k], v)
		}
	}
}

func TestAllowedEnvRefusesTheOnesThatRedirectExecution(t *testing.T) {
	for _, name := range []string{"PATH", "NODE_OPTIONS", "LD_PRELOAD", "BASH_ENV", "PYTHONPATH"} {
		if envAllows(name) {
			t.Errorf("%s is allowed; it can change what a session runs", name)
		}
	}
}

func TestCommand(t *testing.T) {
	if got := command(""); !slices.Equal(got, []string{loginShell, "-l"}) {
		t.Errorf("command(\"\") = %v, want a login shell", got)
	}
	if got := command("   "); !slices.Equal(got, []string{loginShell, "-l"}) {
		t.Errorf("command(whitespace) = %v, want a login shell", got)
	}
	// through a shell, as sshd does, so quoting and the profile's PATH behave.
	want := []string{loginShell, "-lc", "ls -la 'my dir'"}
	if got := command("ls -la 'my dir'"); !slices.Equal(got, want) {
		t.Errorf("command = %v, want %v", got, want)
	}
}

// The key has to survive a restart: a new one each time would trip every
// client's known-hosts check.
func TestHostKeyIsGeneratedOnceAndReused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys", "ssh_host_ed25519_key")

	first, err := hostKey(path)
	if err != nil {
		t.Fatalf("hostKey: %v", err)
	}
	if !strings.Contains(string(first), "PRIVATE KEY") {
		t.Errorf("key = %q, want a PEM private key", first)
	}

	second, err := hostKey(path)
	if err != nil {
		t.Fatalf("hostKey again: %v", err)
	}
	if string(first) != string(second) {
		t.Error("a second call generated a new key; every client would see the host change")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("key mode = %04o, want 0600", perm)
	}
}

// The socket stands in for every sandbox's shell, so its permissions are the
// whole of the access control.
func TestListenSecuresTheSocket(t *testing.T) {
	socket := filepath.Join(socketDir(t), "ssh.sock")
	lis, err := listen(socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = lis.Close() })

	info, err := os.Stat(socket)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("socket mode = %04o, want 0600 — anyone else could open every sandbox", perm)
	}
}

// A socket left by a daemon that died must not stop the next one starting.
func TestListenClearsAStaleSocket(t *testing.T) {
	socket := filepath.Join(socketDir(t), "ssh.sock")
	if err := os.WriteFile(socket, nil, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	lis, err := listen(socket)
	if err != nil {
		t.Fatalf("listen over a stale socket: %v", err)
	}
	_ = lis.Close()
}
