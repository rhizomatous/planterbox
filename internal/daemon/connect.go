package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/rhizomatous/planterbox/internal/api"
	"github.com/rhizomatous/planterbox/internal/api/rpc"
)

// startTimeout is how long a freshly spawned daemon has to answer before we
// give up on it. Generous: the first start may be paging the binary in.
const startTimeout = 10 * time.Second

// pollEvery is how often a starting daemon is checked for.
const pollEvery = 20 * time.Millisecond

// binaryName is the daemon executable autostart looks for.
const binaryName = "plbxd"

// ConnectOptions configures how a client reaches the daemon.
type ConnectOptions struct {
	// StateDir is passed to a daemon this call starts. It has no effect on one
	// already running, which keeps the store it was started with.
	StateDir string
	// NoStart connects to a running daemon but does not start one.
	NoStart bool
	// Env overrides the environment paths resolve against. Tests set it.
	Env *Env
}

// Connect returns an [api.Service] backed by the daemon, starting one if
// nothing is listening.
func Connect(ctx context.Context, opts ConnectOptions) (api.Service, error) {
	env := HostEnv(runtime.GOOS)
	if opts.Env != nil {
		env = *opts.Env
	}
	socket, err := Socket(env)
	if err != nil {
		return nil, err
	}

	if alive(ctx, socket) {
		return rpc.Dial(socket)
	}
	if opts.NoStart {
		return nil, fmt.Errorf("no plbx daemon is running (expected one at %s)", socket)
	}

	if err := Start(ctx, env, opts.StateDir); err != nil {
		return nil, err
	}
	return rpc.Dial(socket)
}

// Start launches a daemon and waits for it to answer.
//
// The child is detached into its own session so it outlives the command that
// started it, and its output goes to a log in the runtime directory — a daemon
// that dies at startup should leave a reason behind rather than writing over
// whatever the user was looking at.
func Start(ctx context.Context, env Env, stateDir string) error {
	socket, err := Socket(env)
	if err != nil {
		return err
	}
	if alive(ctx, socket) {
		return fmt.Errorf("%w at %s", ErrAlreadyRunning, socket)
	}
	if err := ensureRuntimeDir(socket); err != nil {
		return fmt.Errorf("preparing the runtime directory: %w", err)
	}

	bin, err := findDaemon()
	if err != nil {
		return err
	}

	args := []string{"--socket", socket}
	if stateDir != "" {
		args = append(args, "--state-dir", stateDir)
	}
	cmd := exec.Command(bin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	logPath, err := LogPath(env)
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("opening the daemon log: %w", err)
	}
	defer func() { _ = logFile.Close() }()
	cmd.Stdout, cmd.Stderr = logFile, logFile

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting %s: %w", bin, err)
	}
	// nothing waits on it, so let the OS reap it rather than leaving a zombie
	// parented to a CLI that is about to exit anyway.
	go func() { _ = cmd.Process.Release() }()

	return waitForSocket(ctx, socket, logPath)
}

// waitForSocket blocks until the daemon answers or the deadline passes.
func waitForSocket(ctx context.Context, socket, logPath string) error {
	deadline := time.Now().Add(startTimeout)
	for {
		if alive(ctx, socket) {
			return nil
		}
		if time.Now().After(deadline) {
			// the daemon writes why it gave up before it dies, and a message
			// pointing at a log file leaves the reason one step away from
			// someone who is already stuck.
			if reason := lastLogLine(logPath); reason != "" {
				return fmt.Errorf("the plbx daemon did not start: %s", reason)
			}
			return fmt.Errorf("the plbx daemon did not come up within %s; see %s", startTimeout, logPath)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollEvery):
		}
	}
}

// Stop signals the running daemon to exit and waits for its socket to go quiet.
func Stop(ctx context.Context, env Env) error {
	pid, ok := Running(ctx, env)
	if !ok {
		return errors.New("no plbx daemon is running")
	}
	if pid == 0 {
		return errors.New("a plbx daemon is running, but its process id is unknown")
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("finding the daemon process: %w", err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("signalling the daemon: %w", err)
	}

	socket, err := Socket(env)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(startTimeout)
	for alive(ctx, socket) {
		if time.Now().After(deadline) {
			return fmt.Errorf("the plbx daemon did not stop within %s", startTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollEvery):
		}
	}
	return nil
}

// findDaemon locates the daemon binary, preferring the one shipped alongside
// the running plbx. A plbx from one install must not autostart a plbxd from
// another: the two speak a versioned contract to each other.
func findDaemon() (string, error) {
	if self, err := os.Executable(); err == nil {
		beside := filepath.Join(filepath.Dir(self), binaryName)
		if info, err := os.Stat(beside); err == nil && !info.IsDir() {
			return beside, nil
		}
	}
	path, err := exec.LookPath(binaryName)
	if err != nil {
		return "", fmt.Errorf("cannot find %s, which plbx needs to run sandboxes: %w", binaryName, err)
	}
	return path, nil
}

// lastLogLine reads the final non-empty line of the daemon's log, which is
// where a daemon that failed to start says so.
func lastLogLine(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}
