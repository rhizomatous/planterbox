package runner

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"

	"github.com/rhizomatous/planterbox/internal/api"
)

// Executor runs built invocations. Swapping it is how --dry-run and unit tests
// avoid a live runtime.
//
// The three methods exist because plbx needs three shapes: Output for the
// commands whose result it parses, Stream for the ones it watches, and Session
// for the ones a user is sitting in front of.
type Executor interface {
	// Output runs inv and returns its stdout.
	Output(ctx context.Context, inv Invocation) ([]byte, error)
	// Session runs inv with streams wired to its stdio and returns its exit
	// status. A non-zero status is the command's answer, not an error. tty asks
	// for a terminal, which the session provides however it must.
	Session(ctx context.Context, inv Invocation, streams api.Streams, tty bool) (int, error)
	// Stream runs inv and yields its stdout a line at a time, closing the
	// channel when the command exits or ctx is cancelled.
	Stream(ctx context.Context, inv Invocation) (<-chan string, error)
}

// hostExecutor runs invocations as real subprocesses.
type hostExecutor struct{}

func (hostExecutor) Output(ctx context.Context, inv Invocation) ([]byte, error) {
	cmd := exec.CommandContext(ctx, inv.Path, inv.Args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, invocationError(inv, stderr.Bytes(), err)
	}
	return out, nil
}

// Session takes one of two shapes, decided by what it is handed.
//
// When stdin is already this process's terminal, the child inherits it and the
// runtime talks to a real tty with nothing copied in between: the in-process
// CLI's case. When it isn't, and a terminal was asked for, one is allocated
// here and pumped: the daemon's case, where the terminal is on the far end of
// a socket and the runtime would otherwise refuse `-t` outright.
func (hostExecutor) Session(ctx context.Context, inv Invocation, streams api.Streams, tty bool) (int, error) {
	cmd := exec.CommandContext(ctx, inv.Path, inv.Args...)

	if !tty || isTerminal(streams.Stdin) {
		cmd.Stdout, cmd.Stderr = streams.Stdout, streams.Stderr
		if f, ok := streams.Stdin.(*os.File); ok {
			// the child inherits the descriptor, so os/exec copies nothing
			// and Wait has only the process to wait for.
			cmd.Stdin = f
			return waitFor(cmd, inv)
		}
		return pipedStdinSession(cmd, inv, streams.Stdin)
	}
	return ptySession(cmd, inv, streams)
}

// pipedStdinSession runs cmd with stdin fed through a pipe this owns, rather
// than handing the reader to os/exec.
//
// exec.Cmd.Wait waits for its own stdin-copying goroutine whenever Stdin is
// not an *os.File, and through the daemon it never is: it is the read end of a
// pipe fed by the client's socket, which stays open as long as the client's
// terminal does. A terminal sends no EOF, so the command exits, the copier
// stays blocked on a read, and Wait never returns: `plbx exec --no-tty` from
// a real terminal hangs exactly there. Owning the copy leaves Wait waiting
// only for the process.
func pipedStdinSession(cmd *exec.Cmd, inv Invocation, stdin io.Reader) (int, error) {
	w, err := cmd.StdinPipe()
	if err != nil {
		return 0, invocationError(inv, nil, err)
	}
	if err := cmd.Start(); err != nil {
		return 0, invocationError(inv, nil, err)
	}
	go func() {
		defer func() { _ = w.Close() }()
		_, _ = io.Copy(w, stdin)
	}()
	return exitStatus(cmd.Wait(), inv)
}

// ptySession gives the child a pseudo-terminal and relays it to streams.
func ptySession(cmd *exec.Cmd, inv Invocation, streams api.Streams) (int, error) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		return 0, invocationError(inv, nil, err)
	}
	defer func() { _ = ptmx.Close() }()

	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	// the child leads its own session with the pty as controlling terminal, so
	// job control and signals behave as they would in a real one.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}

	// size the terminal before the child exists. A pty opens at 0x0, and a
	// full-screen program reads its dimensions once at startup, so arriving a
	// moment later leaves it laid out against nothing.
	if size, ok := pendingSize(streams.Resize); ok {
		_ = pty.Setsize(ptmx, &pty.Winsize{Rows: size.Rows, Cols: size.Cols})
	}

	if err := cmd.Start(); err != nil {
		_ = tty.Close()
		return 0, invocationError(inv, nil, err)
	}
	// the child holds the slave now. Keeping our copy open would stop reads on
	// the master from ever seeing the session end.
	_ = tty.Close()

	go resize(ptmx, streams.Resize)
	go func() { _, _ = io.Copy(ptmx, streams.Stdin) }()

	// draining before waiting is deliberate: the read ends on its own when the
	// last writer of the slave goes away, and closing the master first would
	// throw away whatever the command printed on its way out.
	_, _ = io.Copy(streams.Stdout, ptmx)
	return exitStatus(cmd.Wait(), inv)
}

// pendingSize takes a terminal size that is already known, without waiting for
// one that may never come.
func pendingSize(sizes <-chan api.Size) (api.Size, bool) {
	if sizes == nil {
		return api.Size{}, false
	}
	select {
	case size, ok := <-sizes:
		return size, ok
	default:
		return api.Size{}, false
	}
}

// resize applies terminal dimensions as they arrive, until the channel closes.
func resize(ptmx *os.File, sizes <-chan api.Size) {
	for size := range sizes {
		_ = pty.Setsize(ptmx, &pty.Winsize{Rows: size.Rows, Cols: size.Cols})
	}
}

// isTerminal reports whether r is this process's own terminal, which is the one
// case where a session needs no pty of its own.
func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	return ok && term.IsTerminal(f.Fd())
}

func waitFor(cmd *exec.Cmd, inv Invocation) (int, error) {
	return exitStatus(cmd.Run(), inv)
}

// exitStatus separates a command that ran and failed from one that could not be
// run. An agent exiting non-zero is the agent's business, not a plbx failure.
func exitStatus(err error, inv Invocation) (int, error) {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	if err != nil {
		return 0, invocationError(inv, nil, err)
	}
	return 0, nil
}

func (hostExecutor) Stream(ctx context.Context, inv Invocation) (<-chan string, error) {
	cmd := exec.CommandContext(ctx, inv.Path, inv.Args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, invocationError(inv, nil, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, invocationError(inv, nil, err)
	}

	lines := make(chan string)
	go func() {
		defer close(lines)
		defer func() { _ = cmd.Wait() }()

		scan := bufio.NewScanner(stdout)
		for scan.Scan() {
			select {
			case lines <- scan.Text():
			case <-ctx.Done():
				return
			}
		}
	}()
	return lines, nil
}

// dryRunExecutor renders invocations instead of running them.
type dryRunExecutor struct{ w io.Writer }

func (d dryRunExecutor) Output(_ context.Context, inv Invocation) ([]byte, error) {
	_, err := fmt.Fprintln(d.w, inv)
	return nil, err
}

func (d dryRunExecutor) Session(_ context.Context, inv Invocation, _ api.Streams, _ bool) (int, error) {
	_, err := fmt.Fprintln(d.w, inv)
	return 0, err
}

func (d dryRunExecutor) Stream(_ context.Context, inv Invocation) (<-chan string, error) {
	if _, err := fmt.Fprintln(d.w, inv); err != nil {
		return nil, err
	}
	lines := make(chan string)
	close(lines)
	return lines, nil
}

// invocationError turns a failed invocation into a message that names the
// subcommand and carries the runtime's own complaint, which is almost always
// more useful than the exit status.
func invocationError(inv Invocation, stderr []byte, err error) error {
	label := filepath.Base(inv.Path)
	if len(inv.Args) > 0 {
		label += " " + inv.Args[0]
	}
	if msg := strings.TrimSpace(string(stderr)); msg != "" {
		return fmt.Errorf("%s: %s (%w)", label, msg, err)
	}
	return fmt.Errorf("%s: %w", label, err)
}

// runtimeSays reports whether err carries any of the given phrases, compared
// lowercased. The runtimes give these conditions no distinguishable exit
// status, so the message is all there is to match on, and each of them words
// it differently.
func runtimeSays(err error, phrases ...string) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, p := range phrases {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

// isNotFound reports whether err is a runtime complaining that a container or
// volume does not exist. docker says "no such", podman "not found".
func isNotFound(err error) bool {
	return runtimeSays(err, "no such", "not found")
}
