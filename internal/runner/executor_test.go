package runner

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rhizomatous/planterbox/internal/api"
)

// These drive hostExecutor against /bin/sh rather than a container runtime, so
// they stay as pure as the rest of the suite: the pty handling is the thing
// under test, and it is the same code whichever command runs on the far side.

// shell builds an invocation running one shell command.
func shell(script string) Invocation {
	return Invocation{Path: "/bin/sh", Args: []string{"-c", script}}
}

// session runs one, collecting everything it wrote.
func session(t *testing.T, inv Invocation, streams api.Streams, tty bool) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var out strings.Builder
	if streams.Stdout == nil {
		streams.Stdout = &out
	}
	if streams.Stderr == nil {
		streams.Stderr = streams.Stdout
	}
	if streams.Stdin == nil {
		streams.Stdin = strings.NewReader("")
	}

	code, err := hostExecutor{}.Session(ctx, inv, streams, tty)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	return out.String(), code
}

func TestSessionSizesTheTerminalBeforeTheCommandRuns(t *testing.T) {
	// a pty opens at 0x0, and a full-screen program reads its dimensions once
	// at startup. A size that lands a moment later leaves it laid out against
	// nothing, which is what the agent would show.
	sizes := make(chan api.Size, 1)
	sizes <- api.Size{Rows: 24, Cols: 80}
	close(sizes)

	out, code := session(t, shell("stty size"), api.Streams{Resize: sizes}, true)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (output: %q)", code, out)
	}
	if !strings.Contains(out, "24 80") {
		t.Errorf("terminal size = %q, want 24 80 — the pty was never sized", strings.TrimSpace(out))
	}
}

func TestSessionGivesATerminalWhenOneIsAskedFor(t *testing.T) {
	out, _ := session(t, shell("test -t 1 && echo yes-tty || echo no-tty"), api.Streams{}, true)
	if !strings.Contains(out, "yes-tty") {
		t.Errorf("out = %q, want the command to see a terminal", strings.TrimSpace(out))
	}
}

func TestSessionWithoutATTYUsesPlainPipes(t *testing.T) {
	// nothing asked for a terminal, so allocating one would misreport the
	// environment to anything that checks.
	out, _ := session(t, shell("test -t 1 && echo yes-tty || echo no-tty"), api.Streams{}, false)
	if !strings.Contains(out, "no-tty") {
		t.Errorf("out = %q, want no terminal", strings.TrimSpace(out))
	}
}

func TestSessionCarriesStdinToTheCommand(t *testing.T) {
	out, _ := session(t, shell("cat"), api.Streams{Stdin: strings.NewReader("piped in")}, false)
	if !strings.Contains(out, "piped in") {
		t.Errorf("out = %q, want the input echoed back", out)
	}
}

func TestSessionCarriesStdinThroughAPTY(t *testing.T) {
	// the command decides when it is done, not the input running out: a pty
	// has no EOF to deliver, because its slave stays open after the reader
	// feeding the master is spent. `cat` here would wait forever, and so would
	// it on a real terminal — which is exactly why the CLI only asks for a tty
	// when stdin actually is one.
	out, _ := session(t, shell("read line; echo got:$line"),
		api.Streams{Stdin: strings.NewReader("typed in\n")}, true)
	if !strings.Contains(out, "got:typed in") {
		t.Errorf("out = %q, want the line to reach the command", out)
	}
}

func TestSessionKeepsStderrSeparateWithoutATTY(t *testing.T) {
	var stdout, stderr strings.Builder
	_, _ = session(t, shell("echo out; echo err >&2"),
		api.Streams{Stdout: &stdout, Stderr: &stderr}, false)

	if !strings.Contains(stdout.String(), "out") || strings.Contains(stdout.String(), "err") {
		t.Errorf("stdout = %q, want only the stdout line", stdout.String())
	}
	if !strings.Contains(stderr.String(), "err") {
		t.Errorf("stderr = %q, want the stderr line", stderr.String())
	}
}

func TestSessionReportsTheCommandsExitCode(t *testing.T) {
	for _, tty := range []bool{false, true} {
		// a command exiting non-zero is its own answer, not a plbx failure,
		// and that has to hold on both paths.
		_, code := session(t, shell("exit 7"), api.Streams{}, tty)
		if code != 7 {
			t.Errorf("tty=%v: exit code = %d, want 7", tty, code)
		}
	}
}

func TestSessionSurvivesNoResizeChannel(t *testing.T) {
	// a terminal session whose caller tracks no size still has to run.
	out, code := session(t, shell("echo fine"), api.Streams{Resize: nil}, true)
	if code != 0 || !strings.Contains(out, "fine") {
		t.Errorf("out = %q, code = %d", out, code)
	}
}
