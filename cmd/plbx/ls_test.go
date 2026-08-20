package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/rhizomatous/planterbox/internal/api"
)

// runCLI drives the whole command tree against svc and returns its stdout.
func runCLI(t *testing.T, svc api.Service, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := newRootCmd(withService(svc))
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return ansi.Strip(out.String()), err
}

func fixture() []api.Sandbox {
	return []api.Sandbox{
		{
			Spec: api.Spec{
				Name:       "myrepo",
				Agent:      "claude",
				Image:      "base:1",
				Workspaces: []api.Workspace{{Host: "/home/viv/myrepo"}},
				CreatedAt:  time.Now().Add(-2 * time.Hour),
			},
			State: api.State{Status: api.StatusRunning},
		},
		{
			Spec:  api.Spec{Name: "scratch", CreatedAt: time.Now().Add(-time.Hour)},
			State: api.State{Status: api.StatusStopped},
		},
	}
}

func TestLsEmpty(t *testing.T) {
	out, err := runCLI(t, api.NewFake(), "ls")
	if err != nil {
		t.Fatalf("ls: %v", err)
	}
	if strings.TrimSpace(out) != "no sandboxes yet" {
		t.Errorf("ls = %q, want the empty-state hint", out)
	}
}

func TestLsGoesThroughTheService(t *testing.T) {
	fake := api.NewFake()
	if _, err := runCLI(t, fake, "ls"); err != nil {
		t.Fatalf("ls: %v", err)
	}
	if len(fake.Calls) == 0 || fake.Calls[0] != "List" {
		t.Errorf("service calls = %v, want List — the CLI must not reach past api.Service", fake.Calls)
	}
}

func TestLsTable(t *testing.T) {
	out, err := runCLI(t, api.NewFake(fixture()...), "ls")
	if err != nil {
		t.Fatalf("ls: %v", err)
	}
	for _, want := range []string{"NAME", "myrepo", "running", "scratch", "stopped"} {
		if !strings.Contains(out, want) {
			t.Errorf("ls output missing %q:\n%s", want, out)
		}
	}
}

func TestLsQuietPrintsNamesOnly(t *testing.T) {
	out, err := runCLI(t, api.NewFake(fixture()...), "ls", "-q")
	if err != nil {
		t.Fatalf("ls -q: %v", err)
	}
	if got := strings.Fields(out); len(got) != 2 || got[0] != "myrepo" || got[1] != "scratch" {
		t.Errorf("ls -q = %q, want just the two names", out)
	}
}

func TestLsJSONEmptyIsAnArray(t *testing.T) {
	// this gets piped into jq. null would break every caller.
	out, err := runCLI(t, api.NewFake(), "ls", "--json")
	if err != nil {
		t.Fatalf("ls --json: %v", err)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("ls --json = %q, want []", out)
	}
}

func TestLsJSONRoundTrips(t *testing.T) {
	out, err := runCLI(t, api.NewFake(fixture()...), "ls", "--json")
	if err != nil {
		t.Fatalf("ls --json: %v", err)
	}
	var got []api.RedactedSandbox
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if len(got) != 2 || got[0].Spec.Name != "myrepo" || got[0].State.Status != api.StatusRunning {
		t.Errorf("decoded %+v, want the two fixture sandboxes", got)
	}
}

func TestLsSurfacesServiceErrors(t *testing.T) {
	fake := api.NewFake()
	fake.Err = errors.New("daemon unreachable")
	if _, err := runCLI(t, fake, "ls"); err == nil {
		t.Error("ls should surface a service error rather than print an empty table")
	}
}

func TestLsRejectsArguments(t *testing.T) {
	if _, err := runCLI(t, api.NewFake(), "ls", "extra"); err == nil {
		t.Error("ls takes no arguments")
	}
}

func TestBareCommandFallsBackToTheListingWithoutATerminal(t *testing.T) {
	// bare `plbx` opens the dashboard, which needs a terminal. Piped or run
	// from a script there isn't one, and failing on a missing TTY would be
	// worse than printing what `plbx ls` prints.
	out, err := runCLI(t, api.NewFake(fixture()...))
	if err != nil {
		t.Fatalf("bare plbx: %v", err)
	}
	for _, want := range []string{"NAME", "myrepo", "scratch"} {
		if !strings.Contains(out, want) {
			t.Errorf("bare plbx output missing %q:\n%s", want, out)
		}
	}
}

func TestBareCommandRejectsArguments(t *testing.T) {
	// a typo'd subcommand should say so rather than opening the dashboard.
	if _, err := runCLI(t, api.NewFake(), "lss"); err == nil {
		t.Error("an unknown subcommand should be rejected")
	}
}
