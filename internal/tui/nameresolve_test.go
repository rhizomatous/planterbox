package tui

import (
	"strings"
	"testing"
)

// TestNameDescriptionSaysWhatBlankWillProduce covers the one field whose
// effective value is not the one on screen. The sanitising matters: a
// workspace called jardinière makes a sandbox called jardini-re, and finding
// that out after the sandbox exists is too late to pick something else.
func TestNameDescriptionSaysWhatBlankWillProduce(t *testing.T) {
	c := &createForm{workspace: "/Users/viv/dev/jardinière"}
	got := c.nameDescription()
	if !strings.Contains(got, "jardini-re") {
		t.Errorf("description %q should name the sandbox blank would produce", got)
	}

	c.name = "chosen"
	if after := c.nameDescription(); strings.Contains(after, "jardini-re") {
		t.Errorf("a typed name needs no default: %q", after)
	}
}

// an empty workspace has no directory to name a sandbox after, so it falls
// back to saying what the rule is.
func TestNameDescriptionWithNoWorkspaceYet(t *testing.T) {
	c := &createForm{}
	if got := c.nameDescription(); !strings.Contains(got, "after the directory") {
		t.Errorf("got %q, want the general rule", got)
	}
}
