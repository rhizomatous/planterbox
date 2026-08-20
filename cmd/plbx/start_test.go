package main

import (
	"strings"
	"testing"

	"github.com/rhizomatous/planterbox/internal/api"
)

// TestStartTakesSeveralSandboxes pins what `start [SANDBOX...]` claims: a
// usage line advertising several while the argument count refuses them is
// worse than either alone.
func TestStartTakesSeveralSandboxes(t *testing.T) {
	fake := api.NewFake(
		api.Sandbox{Spec: api.Spec{Name: "one", Workspaces: []api.Workspace{{Host: "/a"}}}, State: api.State{Status: api.StatusStopped}},
		api.Sandbox{Spec: api.Spec{Name: "two", Workspaces: []api.Workspace{{Host: "/b"}}}, State: api.State{Status: api.StatusStopped}},
	)
	out, err := runCLI(t, fake, "start", "one", "two")
	if err != nil {
		t.Fatalf("start one two: %v", err)
	}
	for _, want := range []string{"one", "two"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q should name %q", out, want)
		}
	}
}

// every command whose usage advertises SANDBOX... must actually accept it.
//
// Run against sandboxes that exist, so the command has to succeed outright.
// Matching on the wording of an argument-count error would pass the day cobra
// rephrases it.
func TestUsageAndArgumentCountsAgree(t *testing.T) {
	for _, args := range [][]string{
		{"rm", "a", "b", "--force"},
		{"stop", "a", "b"},
		{"start", "a", "b"},
	} {
		fake := api.NewFake(
			api.Sandbox{Spec: api.Spec{Name: "a", Workspaces: []api.Workspace{{Host: "/a"}}}, State: api.State{Status: api.StatusRunning}},
			api.Sandbox{Spec: api.Spec{Name: "b", Workspaces: []api.Workspace{{Host: "/b"}}}, State: api.State{Status: api.StatusRunning}},
		)
		if _, err := runCLI(t, fake, args...); err != nil {
			t.Errorf("plbx %s: %v", strings.Join(args, " "), err)
		}
	}
}
