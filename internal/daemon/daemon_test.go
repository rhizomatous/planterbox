package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rhizomatous/planterbox/internal/api/rpc"
)

// env builds a lookup over a fixed set of variables.
func env(goos string, vars map[string]string) Env {
	return Env{
		GOOS:   goos,
		UID:    501,
		Getenv: func(k string) string { return vars[k] },
	}
}

func TestSocketPrefersAnExplicitPath(t *testing.T) {
	got, err := Socket(env("linux", map[string]string{
		"PLBX_SOCKET":     "/run/mine.sock",
		"XDG_RUNTIME_DIR": "/run/user/501",
	}))
	if err != nil {
		t.Fatalf("Socket: %v", err)
	}
	if got != "/run/mine.sock" {
		t.Errorf("Socket = %q, want the explicit path to win", got)
	}
}

func TestSocketUsesTheXDGRuntimeDirWhenThereIsOne(t *testing.T) {
	got, err := Socket(env("linux", map[string]string{"XDG_RUNTIME_DIR": "/run/user/501"}))
	if err != nil {
		t.Fatalf("Socket: %v", err)
	}
	if got != "/run/user/501/planterbox/plbxd.sock" {
		t.Errorf("Socket = %q", got)
	}
}

func TestSocketIgnoresARelativeRuntimeDir(t *testing.T) {
	// a relative XDG_RUNTIME_DIR would put the socket somewhere that depends on
	// the working directory, which is nobody's intent.
	got, err := Socket(env("linux", map[string]string{"XDG_RUNTIME_DIR": "relative/path"}))
	if err != nil {
		t.Fatalf("Socket: %v", err)
	}
	if strings.Contains(got, "relative") {
		t.Errorf("Socket = %q, want the relative dir ignored", got)
	}
}

func TestSocketDoesNotMoveWithTMPDIR(t *testing.T) {
	// nix-shell and friends point $TMPDIR at a fresh directory per shell.
	// Resolving through it would put the socket somewhere new every time a
	// shell opened, leaving a running daemon invisible to the next command.
	t.Setenv("TMPDIR", "/var/folders/xx/nix-shell.AbC123/T")
	first, err := Socket(env("darwin", nil))
	if err != nil {
		t.Fatalf("Socket: %v", err)
	}

	t.Setenv("TMPDIR", "/var/folders/xx/nix-shell.ZyX987/T")
	second, err := Socket(env("darwin", nil))
	if err != nil {
		t.Fatalf("Socket: %v", err)
	}

	if first != second {
		t.Errorf("the socket moved with TMPDIR: %q then %q", first, second)
	}
	if strings.Contains(first, "nix-shell") {
		t.Errorf("Socket = %q, want a path that does not depend on TMPDIR", first)
	}
	if first != "/tmp/planterbox-501/plbxd.sock" {
		t.Errorf("Socket = %q, want the stable per-user path", first)
	}
}

func TestSocketFallsBackPerUser(t *testing.T) {
	// macOS sets no XDG_RUNTIME_DIR. Two users on one machine must not land on
	// the same socket, or either could drive the other's sandboxes.
	first, err := Socket(env("darwin", nil))
	if err != nil {
		t.Fatalf("Socket: %v", err)
	}
	other := Env{GOOS: "darwin", UID: 502, Getenv: func(string) string { return "" }}
	second, err := Socket(other)
	if err != nil {
		t.Fatalf("Socket: %v", err)
	}
	if first == second {
		t.Errorf("two users share the socket %q", first)
	}
}

func TestSocketStaysUnderTheKernelsPathLimit(t *testing.T) {
	// a unix socket path is capped near 104 bytes, and the failure when it is
	// exceeded is an obscure bind error rather than anything that names a path.
	got, err := Socket(env("darwin", nil))
	if err != nil {
		t.Fatalf("Socket: %v", err)
	}
	if len(got) >= 104 {
		t.Errorf("Socket = %q is %d bytes, too long to bind", got, len(got))
	}
}

func TestPidAndLogSitBesideTheSocket(t *testing.T) {
	vars := map[string]string{"XDG_RUNTIME_DIR": "/run/user/501"}
	socket, _ := Socket(env("linux", vars))
	pid, err := PidPath(env("linux", vars))
	if err != nil {
		t.Fatalf("PidPath: %v", err)
	}
	logPath, err := LogPath(env("linux", vars))
	if err != nil {
		t.Fatalf("LogPath: %v", err)
	}
	if filepath.Dir(pid) != filepath.Dir(socket) || filepath.Dir(logPath) != filepath.Dir(socket) {
		t.Errorf("pid %q and log %q should sit beside socket %q", pid, logPath, socket)
	}
}

// serve starts a daemon on a private socket and returns its path, stopping it
// when the test ends.
func serve(t *testing.T) string {
	t.Helper()

	// a short base, not t.TempDir(): the socket path has a hard length limit
	// and a test name spends most of the budget.
	dir, err := os.MkdirTemp("", "plbx")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	socket := filepath.Join(dir, "d.sock")
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		done <- Serve(ctx, Options{
			Socket:    socket,
			StateDir:  filepath.Join(dir, "state"),
			ProxyAddr: "127.0.0.1:0",
			Ready:     func() { close(ready) },
		})
	}()

	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("the daemon stopped before it was ready: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("the daemon never became ready")
	}

	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("the daemon did not stop when its context was cancelled")
		}
	})
	return socket
}

func TestServeAnswersOverTheSocket(t *testing.T) {
	socket := serve(t)

	client, err := rpc.Dial(socket)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = client.Close() }()

	sandboxes, err := client.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sandboxes) != 0 {
		t.Errorf("a fresh daemon listed %d sandboxes, want none", len(sandboxes))
	}
}

func TestServeWritesItsPidBeforeItAnswers(t *testing.T) {
	// anything that finds the socket answering has to be able to learn who is
	// serving; written afterwards, the record would race every client.
	socket := serve(t)

	raw, err := os.ReadFile(replaceBase(socket, pidFile))
	if err != nil {
		t.Fatalf("reading the pidfile: %v", err)
	}
	if strings.TrimSpace(string(raw)) == "" {
		t.Error("the pidfile is empty")
	}
}

func TestServeRefusesASecondDaemon(t *testing.T) {
	socket := serve(t)

	err := Serve(context.Background(), Options{Socket: socket, StateDir: t.TempDir(), ProxyAddr: "127.0.0.1:0"})
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Errorf("err = %v, want ErrAlreadyRunning", err)
	}
}

func TestServeClearsASocketLeftBehindByADeadDaemon(t *testing.T) {
	// a daemon that was killed leaves the file in place, and it is
	// indistinguishable from a live one until you try to connect.
	dir, err := os.MkdirTemp("", "plbx")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	socket := filepath.Join(dir, "d.sock")
	if err := os.WriteFile(socket, nil, 0o600); err != nil {
		t.Fatalf("planting a stale socket: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, Options{
			Socket:    socket,
			StateDir:  filepath.Join(dir, "state"),
			ProxyAddr: "127.0.0.1:0",
			Ready:     func() { close(ready) },
		})
	}()

	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("the daemon refused to start over a stale socket: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("the daemon never became ready")
	}
	cancel()
	<-done
}

func TestServeCleansUpAfterItself(t *testing.T) {
	dir, err := os.MkdirTemp("", "plbx")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	socket := filepath.Join(dir, "d.sock")
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, Options{
			Socket:    socket,
			StateDir:  filepath.Join(dir, "state"),
			ProxyAddr: "127.0.0.1:0",
			Ready:     func() { close(ready) },
		})
	}()
	<-ready
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Serve: %v", err)
	}

	// leaving either behind would have the next start think a daemon is up, or
	// report a pid belonging to nothing.
	for _, name := range []string{"d.sock", pidFile} {
		if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s outlived the daemon", name)
		}
	}
}

func TestConnectRefusesToStartWhenToldNotTo(t *testing.T) {
	dir, err := os.MkdirTemp("", "plbx")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	e := Env{GOOS: "linux", UID: 501, Getenv: func(k string) string {
		if k == "PLBX_SOCKET" {
			return filepath.Join(dir, "absent.sock")
		}
		return ""
	}}

	_, err = Connect(context.Background(), ConnectOptions{NoStart: true, Env: &e})
	if err == nil {
		t.Fatal("Connect should have failed with no daemon to reach")
	}
	if !strings.Contains(err.Error(), "no plbx daemon is running") {
		t.Errorf("err = %v, want it to say no daemon is running", err)
	}
}

func TestConnectReachesARunningDaemon(t *testing.T) {
	socket := serve(t)
	e := Env{GOOS: "linux", UID: 501, Getenv: func(k string) string {
		if k == "PLBX_SOCKET" {
			return socket
		}
		return ""
	}}

	svc, err := Connect(context.Background(), ConnectOptions{NoStart: true, Env: &e})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = svc.Close() }()

	if _, err := svc.List(context.Background()); err != nil {
		t.Fatalf("List through the daemon: %v", err)
	}
}

// TestDaemonNearFollowsASymlinkToTheRealInstall guards what a brew install
// looks like: plbx on $PATH is a link into the Caskroom directory where both
// binaries were unpacked, and nothing named plbxd sits beside the link.
func TestDaemonNearFollowsASymlinkToTheRealInstall(t *testing.T) {
	root := tempDir(t)
	install := filepath.Join(root, "Caskroom", "plbx", "0.6.0")
	bin := filepath.Join(root, "bin")
	for _, dir := range []string{install, bin} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", dir, err)
		}
	}
	for _, name := range []string{"plbx", binaryName} {
		if err := os.WriteFile(filepath.Join(install, name), nil, 0o755); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	link := filepath.Join(bin, "plbx")
	if err := os.Symlink(filepath.Join(install, "plbx"), link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	got, ok := daemonNear(link)
	if !ok {
		t.Fatal("daemonNear found no daemon through the symlink")
	}
	if filepath.Dir(got) != install {
		t.Errorf("daemonNear = %q, want one in %q", got, install)
	}
}

// TestDaemonNearPrefersTheDirectNeighbour keeps a plbx run straight out of a
// build directory finding the plbxd built next to it.
func TestDaemonNearPrefersTheDirectNeighbour(t *testing.T) {
	dir := tempDir(t)
	self := filepath.Join(dir, "plbx")
	for _, name := range []string{"plbx", binaryName} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o755); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	got, ok := daemonNear(self)
	if !ok {
		t.Fatal("daemonNear found no daemon beside plbx")
	}
	if got != filepath.Join(dir, binaryName) {
		t.Errorf("daemonNear = %q, want %q", got, filepath.Join(dir, binaryName))
	}
}

// TestDaemonNearReportsNothingWhenThereIsNoDaemon leaves the fallback to $PATH
// as the only remaining route.
func TestDaemonNearReportsNothingWhenThereIsNoDaemon(t *testing.T) {
	dir := tempDir(t)
	self := filepath.Join(dir, "plbx")
	if err := os.WriteFile(self, nil, 0o755); err != nil {
		t.Fatalf("writing plbx: %v", err)
	}
	if got, ok := daemonNear(self); ok {
		t.Errorf("daemonNear = %q, want none", got)
	}
}

// tempDir is t.TempDir with the symlinks resolved out of it. On macOS it hands
// back a path under /var, which is itself a link to /private/var, so a test
// comparing against a resolved path would fail on the wrapper rather than on
// anything it meant to check.
func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolving the temp dir: %v", err)
	}
	return dir
}

// replaceBase swaps a path's filename, keeping its directory.
// Used by tests to verify path layout.
func replaceBase(path, name string) string {
	return filepath.Join(filepath.Dir(path), name)
}
