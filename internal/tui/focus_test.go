package tui

import (
	"errors"
	"testing"

	"github.com/rhizomatous/planterbox/internal/api"
)

// TestCreateMovesTheCursorToWhatItMade keeps the next keypress off the wrong
// sandbox: a selection left where it was before the form opened acts on
// whatever happened to be highlighted.
func TestCreateMovesTheCursorToWhatItMade(t *testing.T) {
	m := loaded(t, api.NewFake(), sandbox("alpha", api.StatusRunning))
	if m.cursor != 0 {
		t.Fatalf("cursor = %d, want it to start on alpha", m.cursor)
	}

	updated, _ := m.Update(actionMsg{verb: "created", name: "zulu", created: true})
	m = updated.(*Model)
	m.applyListing([]api.Sandbox{sandbox("alpha", api.StatusRunning), sandbox("zulu", api.StatusCreated)})

	if got := m.selectedName(); got != "zulu" {
		t.Errorf("selected %q, want the sandbox just created", got)
	}
}

// every other action leaves the selection where it was: moving it after a
// stop or a remove would take it off whatever you were working through.
func TestOtherActionsLeaveTheCursorAlone(t *testing.T) {
	m := loaded(t, api.NewFake(), sandbox("alpha", api.StatusRunning), sandbox("zulu", api.StatusRunning))
	m.cursor = 1

	updated, _ := m.Update(actionMsg{verb: "stopped", name: "alpha"})
	m = updated.(*Model)
	m.applyListing([]api.Sandbox{sandbox("alpha", api.StatusStopped), sandbox("zulu", api.StatusRunning)})

	if got := m.selectedName(); got != "zulu" {
		t.Errorf("selected %q, want the cursor to have stayed on zulu", got)
	}
}

// a create that failed made nothing, so there is nothing to move to.
func TestAFailedCreateLeavesTheCursorAlone(t *testing.T) {
	m := loaded(t, api.NewFake(), sandbox("alpha", api.StatusRunning))

	updated, _ := m.Update(actionMsg{verb: "created", name: "zulu", created: true, err: errors.New("already exists")})
	m = updated.(*Model)
	m.applyListing([]api.Sandbox{sandbox("alpha", api.StatusRunning)})

	if got := m.selectedName(); got != "alpha" {
		t.Errorf("selected %q, want the cursor left where it was", got)
	}
}

// TestFocusSurvivesAListingTakenTooEarly is the race that made the first
// attempt at this look like it did nothing: the periodic refresh lands between
// the create finishing and the sandbox appearing, and a focus spent on a name
// that listing does not contain is a focus spent on nothing.
func TestFocusSurvivesAListingTakenTooEarly(t *testing.T) {
	m := loaded(t, api.NewFake(), sandbox("alpha", api.StatusRunning))

	updated, _ := m.Update(actionMsg{verb: "created", name: "zulu", created: true})
	m = updated.(*Model)

	// a refresh that predates the new sandbox
	m.applyListing([]api.Sandbox{sandbox("alpha", api.StatusRunning)})
	if m.focus != "zulu" {
		t.Fatalf("focus = %q, want it held until the sandbox arrives", m.focus)
	}

	// and the one that has it
	m.applyListing([]api.Sandbox{sandbox("alpha", api.StatusRunning), sandbox("zulu", api.StatusCreated)})
	if got := m.selectedName(); got != "zulu" {
		t.Errorf("selected %q, want the sandbox just created", got)
	}
	if m.focus != "" {
		t.Errorf("focus = %q, want it spent", m.focus)
	}
}
