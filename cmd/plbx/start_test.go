package main

import (
	"strings"
	"testing"

	"github.com/rhizomatous/planterbox/internal/api"
)

// TestStartTakesSeveralSandboxes pins what `start [SANDBOX...]` claims. The
// usage line said so while the argument count refused it, which is the one
// combination that is worse than either.
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
func TestUsageAndArgumentCountsAgree(t *testing.T) {
	for _, args := range [][]string{
		{"rm", "a", "b", "--force"},
		{"stop", "a", "b"},
		{"start", "a", "b"},
	} {
		_, err := runCLI(t, api.NewFake(), args...)
		if err != nil && strings.Contains(err.Error(), "arg(s)") {
			t.Errorf("plbx %s rejected a second name: %v", strings.Join(args, " "), err)
		}
	}
}
