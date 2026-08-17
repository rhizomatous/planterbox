package tui

import (
	"strings"
	"testing"

	"github.com/rhizomatous/planterbox/internal/api"
)

// TestSandboxKeysFollowTheCursor guards a reminder line that used to promise
// the same things whatever was highlighted — `s start/stop` against a sandbox
// that could only be one of them, and attach, shell and remove on a fresh
// install with nothing to apply them to.
func TestSandboxKeysFollowTheCursor(t *testing.T) {
	m := loaded(t, api.NewFake(), sandbox("up", api.StatusRunning), sandbox("down", api.StatusStopped))

	running := m.sandboxKeys()
	if !strings.Contains(running, "s stop") || strings.Contains(running, "s start") {
		t.Errorf("a running sandbox should offer stop: %q", running)
	}

	m.cursor = 1
	stopped := m.sandboxKeys()
	if !strings.Contains(stopped, "s start") || strings.Contains(stopped, "s stop") {
		t.Errorf("a stopped sandbox should offer start: %q", stopped)
	}
}

// with nothing to act on, `c create` is the only key that does anything, and
// it should not be buried among nine that do not.
func TestSandboxKeysOnAnEmptyList(t *testing.T) {
	empty := loaded(t, api.NewFake()).sandboxKeys()
	if !strings.Contains(empty, "c create") {
		t.Errorf("an empty list should still offer create: %q", empty)
	}
	for _, absent := range []string{"attach", "shell", "remove", "details"} {
		if strings.Contains(empty, absent) {
			t.Errorf("an empty list should not offer %q: %q", absent, empty)
		}
	}
}
