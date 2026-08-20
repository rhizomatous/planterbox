package main

import (
	"strings"
	"testing"
)

// TestVersionSkewSpeaksUpOnlyWhenTheBuildsDisagree guards the failure that
// hides: every command works and answers the way the older build did.
func TestVersionSkewSpeaksUpOnlyWhenTheBuildsDisagree(t *testing.T) {
	cli := buildVersion()

	if got := versionSkew(cli); got != "" {
		t.Errorf("a matching daemon should say nothing, got %q", got)
	}

	// a daemon too old to answer at all still has to be named and pointed
	// at the fix.
	old := versionSkew("")
	if !strings.Contains(old, "predates") || !strings.Contains(old, "daemon restart") {
		t.Errorf("an unanswering daemon should be called out and pointed at the fix, got %q", old)
	}

	mismatch := versionSkew("0.0.1")
	for _, want := range []string{cli, "0.0.1", "daemon restart"} {
		if !strings.Contains(mismatch, want) {
			t.Errorf("a mismatch should name %q, got %q", want, mismatch)
		}
	}
}

func TestDaemonVersionLabelSaysWhenItCannotSay(t *testing.T) {
	if got := daemonVersionLabel(""); !strings.Contains(got, "too old") {
		t.Errorf("an empty version should explain itself, got %q", got)
	}
	if got := daemonVersionLabel("1.2.3"); got != "1.2.3" {
		t.Errorf("a real version should pass through, got %q", got)
	}
}
