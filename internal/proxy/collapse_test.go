package proxy

import (
	"testing"
	"time"
)

func at(base time.Time, d time.Duration) time.Time { return base.Add(d) }

func TestCollapseFoldsRepeatsAndCountsThem(t *testing.T) {
	base := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	github := Target{Host: "github.com", Port: 443}
	evil := Target{Host: "evil.example.com", Port: 443}

	groups := Collapse([]Entry{
		{At: at(base, 0), Target: github, Sandbox: "one", Allowed: true, Reason: "preset"},
		{At: at(base, time.Minute), Target: evil, Sandbox: "one", Reason: "denied by default"},
		{At: at(base, 2*time.Minute), Target: evil, Sandbox: "one", Reason: "denied by default"},
		{At: at(base, 3*time.Minute), Target: evil, Sandbox: "one", Reason: "denied by default"},
	})

	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2:\n%+v", len(groups), groups)
	}
	// oldest last-seen first, so it reads like the log it came from
	if groups[0].Target != github || groups[0].Hits != 1 {
		t.Errorf("first group = %+v, want one hit on github", groups[0])
	}
	if groups[1].Target != evil || groups[1].Hits != 3 {
		t.Errorf("second group = %+v, want three hits on evil", groups[1])
	}
	if !groups[1].First.Equal(at(base, time.Minute)) || !groups[1].Last.Equal(at(base, 3*time.Minute)) {
		t.Errorf("group span = %s..%s, want the first and last attempt", groups[1].First, groups[1].Last)
	}
}

// the same host from two sandboxes is two facts, not one.
func TestCollapseKeepsSandboxesApart(t *testing.T) {
	base := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	host := Target{Host: "github.com", Port: 443}
	groups := Collapse([]Entry{
		{At: base, Target: host, Sandbox: "one", Allowed: true},
		{At: base, Target: host, Sandbox: "two", Allowed: true},
	})
	if len(groups) != 2 {
		t.Errorf("got %d groups, want one per sandbox:\n%+v", len(groups), groups)
	}
}

// a host allowed and later denied is two decisions, and both are worth seeing.
func TestCollapseKeepsVerdictsApart(t *testing.T) {
	base := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	host := Target{Host: "github.com", Port: 443}
	groups := Collapse([]Entry{
		{At: base, Target: host, Allowed: true, Reason: "a rule you added"},
		{At: at(base, time.Minute), Target: host, Allowed: false, Reason: "you removed it"},
	})
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want one per verdict:\n%+v", len(groups), groups)
	}
}

// a decision that changed because the policy changed should report what the
// policy says now, not what it said the first time.
func TestCollapseKeepsTheMostRecentReason(t *testing.T) {
	base := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	host := Target{Host: "github.com", Port: 443}
	groups := Collapse([]Entry{
		{At: base, Target: host, Allowed: true, Reason: "the balanced preset"},
		{At: at(base, time.Minute), Target: host, Allowed: true, Reason: "a rule you added"},
	})
	if len(groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(groups))
	}
	if groups[0].Reason != "a rule you added" {
		t.Errorf("reason = %q, want the most recent one", groups[0].Reason)
	}
}

func TestCollapseOfNothingIsNothing(t *testing.T) {
	if got := Collapse(nil); len(got) != 0 {
		t.Errorf("Collapse(nil) = %+v, want empty", got)
	}
}
