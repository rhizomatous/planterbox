package tui

import (
	"testing"

	"github.com/rhizomatous/planterbox/internal/api"
	"github.com/rhizomatous/planterbox/internal/proxy"
)

// from returns a decision attributed to a sandbox, which the package's own
// decision helper leaves blank.
func from(sandbox string, e proxy.Entry) proxy.Entry {
	e.Sandbox = sandbox
	return e
}

// TestNetworkFilterNarrowsToTheSelectedSandbox covers the question the panel
// is there to answer once something has already failed: what did *this*
// sandbox try to reach.
func TestNetworkFilterNarrowsToTheSelectedSandbox(t *testing.T) {
	m := loaded(t, api.NewFake(), sandbox("alpha", api.StatusRunning), sandbox("beta", api.StatusRunning))
	m.connections = []proxy.Entry{
		from("alpha", decision(1, "github.com", true)),
		from("beta", decision(2, "evil.example.com", false)),
		from("alpha", decision(3, "evil.example.com", false)),
	}

	if got := len(m.visibleConnections()); got != 3 {
		t.Errorf("unfiltered = %d entries, want all 3", got)
	}
	if got := m.deniedCount(); got != 2 {
		t.Errorf("unfiltered denials = %d, want 2", got)
	}

	m.connFilter = true // cursor is on alpha
	visible := m.visibleConnections()
	if len(visible) != 2 {
		t.Fatalf("filtered = %d entries, want alpha's 2:\n%+v", len(visible), visible)
	}
	for _, e := range visible {
		if e.Sandbox != "alpha" {
			t.Errorf("beta's decision survived the filter: %+v", e)
		}
	}
	// the badge counts what is shown, or it argues with the list under it
	if got := m.deniedCount(); got != 1 {
		t.Errorf("filtered denials = %d, want alpha's 1", got)
	}
}

// the cursor indexes what is on screen, so allowing a host from a filtered
// list must allow the one that was highlighted.
func TestFilteredCursorSelectsFromWhatIsShown(t *testing.T) {
	m := loaded(t, api.NewFake(), sandbox("alpha", api.StatusRunning), sandbox("beta", api.StatusRunning))
	m.connections = []proxy.Entry{
		from("beta", decision(1, "beta-only.example.com", false)),
		from("alpha", decision(2, "alpha-only.example.com", false)),
	}
	m.connFilter = true
	m.connCursor = 0

	got, ok := m.selectedEntry()
	if !ok {
		t.Fatal("a filtered list with one entry should have a selection")
	}
	if got.Target.Host != "alpha-only.example.com" {
		t.Errorf("selected %q, want the entry actually on screen", got.Target.Host)
	}
}
