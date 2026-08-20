// Package daemon runs plbx's host-resident half and connects clients to it.
//
// The daemon exists because some of what plbx does has to outlive the command
// that asked for it: an egress proxy, forwarded ports, and sessions that a
// second client can be told about. It serves [api.Service] over a unix socket,
// so a CLI or TUI holds the same interface either way.
package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
)

// appDir is plbx's directory name under whichever runtime base the platform
// gives us. It matches the store's, one level down from a different root.
const appDir = "planterbox"

// socketFile and pidFile live side by side in the runtime directory.
const (
	socketFile = "plbxd.sock"
	sshFile    = "plbxd-ssh.sock"
	pidFile    = "plbxd.pid"
	logFile    = "plbxd.log"
)

// Env supplies the environment paths are resolved against. Injecting it keeps
// resolution testable without touching the real environment.
type Env struct {
	GOOS   string
	UID    int
	Getenv func(string) string
}

// HostEnv returns the running host's environment.
func HostEnv(goos string) Env {
	return Env{GOOS: goos, UID: os.Getuid(), Getenv: os.Getenv}
}

// tmpRoot is where the runtime directory goes when the platform names no
// better place. Literally /tmp, and deliberately not os.TempDir: that reads
// $TMPDIR, which nix-shell and others set to a fresh directory per shell. The
// socket would move every time you opened one, and a running daemon would be
// invisible to the next command.
const tmpRoot = "/tmp"

// RuntimeDir resolves the directory holding the socket, the pidfile, and the
// daemon's log.
//
// PLBX_RUNTIME_DIR wins outright. Otherwise XDG_RUNTIME_DIR where the platform
// sets one, and a per-user directory under /tmp where it does not, which is the
// macOS case.
//
// The /tmp fallback is deliberately short rather than tucked under the state
// directory: a unix socket path is capped near 104 bytes by the kernel, and
// "~/Library/Application Support/..." spends most of that budget before it
// reaches a filename.
func RuntimeDir(env Env) (string, error) {
	if dir := env.Getenv("PLBX_RUNTIME_DIR"); dir != "" {
		return dir, nil
	}
	if dir := env.Getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(dir) {
		return filepath.Join(dir, appDir), nil
	}
	if env.UID < 0 {
		return "", errors.New("cannot resolve a runtime directory: no user id")
	}
	return filepath.Join(tmpRoot, appDir+"-"+strconv.Itoa(env.UID)), nil
}

// Socket resolves where the daemon listens. PLBX_SOCKET names it outright, for
// running a daemon somewhere of your own choosing.
func Socket(env Env) (string, error) {
	if path := env.Getenv("PLBX_SOCKET"); path != "" {
		return path, nil
	}
	dir, err := RuntimeDir(env)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, socketFile), nil
}

// SSHSocket resolves where the ssh gateway listens. PLBX_SSH_SOCKET names it
// outright, alongside PLBX_SOCKET, so a daemon of your own is reachable both
// ways.
func SSHSocket(env Env) (string, error) {
	if path := env.Getenv("PLBX_SSH_SOCKET"); path != "" {
		return path, nil
	}
	return runtimePath(env, sshFile)
}

// PidPath resolves the file recording the running daemon's process id.
func PidPath(env Env) (string, error) { return runtimePath(env, pidFile) }

// LogPath resolves the file an autostarted daemon writes to. A daemon started
// by hand keeps its output.
func LogPath(env Env) (string, error) { return runtimePath(env, logFile) }

func runtimePath(env Env, name string) (string, error) {
	dir, err := RuntimeDir(env)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// ensureRuntimeDir creates the runtime directory, private to its owner, and
// refuses one that is not.
//
// The socket inside it is an unauthenticated door onto every sandbox. The
// default location is under /tmp, which anyone on the machine can write to, so
// a directory already sitting on that path is checked rather than trusted:
// MkdirAll is happy to reuse someone else's.
func ensureRuntimeDir(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if err := ownedPrivately(dir, info); err != nil {
		return err
	}
	// an existing directory keeps the mode it was made with, which MkdirAll
	// will not correct.
	return os.Chmod(dir, 0o700)
}
