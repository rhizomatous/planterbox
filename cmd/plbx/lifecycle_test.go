package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/rhizomatous/planterbox/internal/api"
)

// seeded returns a fake holding one sandbox in the given state.
func seeded(status api.Status) *api.Fake {
	return api.NewFake(api.Sandbox{
		Spec: api.Spec{
			Name:       "demo",
			Agent:      "claude",
			Image:      "base:1",
			Workspaces: []api.Workspace{{Host: "/home/viv/demo"}},
		},
		State: api.State{Status: status},
	})
}

func TestStopAndStart(t *testing.T) {
	fake := seeded(api.StatusRunning)
	out, err := runCLI(t, fake, "stop", "demo")
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !strings.Contains(out, "stopped demo") {
		t.Errorf("stop output = %q, want it to name the sandbox", out)
	}
	if fake.Sandboxes[0].State.Status != api.StatusStopped {
		t.Errorf("status = %q, want stopped", fake.Sandboxes[0].State.Status)
	}

	if _, err := runCLI(t, fake, "start", "demo"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if fake.Sandboxes[0].State.Status != api.StatusRunning {
		t.Errorf("status = %q, want running", fake.Sandboxes[0].State.Status)
	}
}

func TestStopReportsTheSandboxNameNotThePath(t *testing.T) {
	// resolving by cwd gives a path-shaped ref; echoing that back is unreadable.
	fake := seeded(api.StatusRunning)
	out, err := runCLI(t, fake, "stop", "demo")
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if strings.Contains(out, "/home/viv") {
		t.Errorf("stop output = %q, want the name rather than a path", out)
	}
}

func TestRmRefusesARunningSandbox(t *testing.T) {
	fake := seeded(api.StatusRunning)
	_, err := runCLI(t, fake, "rm", "demo")
	if !errors.Is(err, api.ErrRunning) {
		t.Fatalf("err = %v, want api.ErrRunning", err)
	}
	if len(fake.Sandboxes) != 1 {
		t.Error("the sandbox should still exist after a refused removal")
	}
}

func TestRmForceRemovesARunningSandbox(t *testing.T) {
	fake := seeded(api.StatusRunning)
	if _, err := runCLI(t, fake, "rm", "--force", "demo"); err != nil {
		t.Fatalf("rm --force: %v", err)
	}
	if len(fake.Sandboxes) != 0 {
		t.Error("rm --force should have removed the sandbox")
	}
}

func TestRmOffATerminalRefusesRatherThanAssumingYes(t *testing.T) {
	fake := seeded(api.StatusStopped)
	_, err := runCLI(t, fake, "rm", "demo")
	if err == nil {
		t.Fatal("rm with nobody to confirm to should refuse")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("err = %v, want it to name --force", err)
	}
	if len(fake.Sandboxes) != 1 {
		t.Error("the sandbox should still exist after a refused removal")
	}
}

func TestRmForceRemovesAStoppedSandboxWithoutAsking(t *testing.T) {
	fake := seeded(api.StatusStopped)
	if _, err := runCLI(t, fake, "rm", "--force", "demo"); err != nil {
		t.Fatalf("rm --force: %v", err)
	}
	if len(fake.Sandboxes) != 0 {
		t.Error("rm --force should have removed the stopped sandbox")
	}
}

func TestConfirmRemove(t *testing.T) {
	for _, tc := range []struct {
		name        string
		typed       string
		interactive bool
		want        bool
		wantErr     bool
	}{
		{name: "yes", typed: "y\n", interactive: true, want: true},
		{name: "spelled out", typed: "YES\n", interactive: true, want: true},
		{name: "no", typed: "n\n", interactive: true},
		{name: "just enter", typed: "\n", interactive: true},
		{name: "anything else", typed: "sure\n", interactive: true},
		{name: "eof", typed: "", interactive: true},
		{name: "no terminal", typed: "y\n", interactive: false, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			got, err := confirmRemove(&out, strings.NewReader(tc.typed), tc.interactive, "demo")
			if tc.wantErr {
				if err == nil {
					t.Fatal("want an error when there is no terminal to ask on")
				}
				if !strings.Contains(err.Error(), "--force") {
					t.Errorf("err = %v, want it to name --force", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("confirmRemove: %v", err)
			}
			if got != tc.want {
				t.Errorf("confirmRemove(%q) = %v, want %v", tc.typed, got, tc.want)
			}
			if tc.interactive && !strings.Contains(out.String(), "demo") {
				t.Errorf("the prompt should name the sandbox: %q", out.String())
			}
		})
	}
}

func TestInspect(t *testing.T) {
	out, err := runCLI(t, seeded(api.StatusRunning), "inspect", "demo")
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	for _, want := range []string{"demo", "running", "claude", "base:1", "/home/viv/demo"} {
		if !strings.Contains(out, want) {
			t.Errorf("inspect output missing %q:\n%s", want, out)
		}
	}
}

func TestInspectJSONRoundTrips(t *testing.T) {
	out, err := runCLI(t, seeded(api.StatusRunning), "inspect", "--json", "demo")
	if err != nil {
		t.Fatalf("inspect --json: %v", err)
	}
	var sb api.Sandbox
	if err := json.Unmarshal([]byte(out), &sb); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if sb.Spec.Name != "demo" || sb.State.Status != api.StatusRunning {
		t.Errorf("decoded %+v, want the seeded sandbox", sb)
	}
}

func TestInspectUnknownSandbox(t *testing.T) {
	if _, err := runCLI(t, api.NewFake(), "inspect", "nosuch"); !errors.Is(err, api.ErrNotFound) {
		t.Errorf("err = %v, want api.ErrNotFound", err)
	}
}

func TestExecPassesFlagsThroughToTheCommand(t *testing.T) {
	// `plbx exec demo bash -lc ...` must not have -lc read as plbx's own flag.
	if _, err := runCLI(t, seeded(api.StatusRunning), "exec", "demo", "bash", "-lc", "echo hi"); err != nil {
		t.Fatalf("exec: %v", err)
	}
}

func TestExecRequiresACommand(t *testing.T) {
	if _, err := runCLI(t, seeded(api.StatusRunning), "exec", "demo"); err == nil {
		t.Error("exec with no command should be rejected")
	}
}

func TestCpRoutesBothDirections(t *testing.T) {
	fake := seeded(api.StatusRunning)
	if _, err := runCLI(t, fake, "cp", "./notes.md", "demo:/home/agent/notes.md"); err != nil {
		t.Fatalf("cp in: %v", err)
	}
	if _, err := runCLI(t, fake, "cp", "demo:/home/agent/notes.md", "./notes.md"); err != nil {
		t.Fatalf("cp out: %v", err)
	}
}

func TestCpRefusesACopyNamingNoSandbox(t *testing.T) {
	if _, err := runCLI(t, seeded(api.StatusRunning), "cp", "./a", "./b"); err == nil {
		t.Error("a copy with no sandbox side should be rejected")
	}
}

// TestHelpNamesEveryAgent covers what `plbx agents` used to: the list is only
// useful where AGENT is typed, so it lives in the help of the two commands
// that take one.
func TestHelpNamesEveryAgent(t *testing.T) {
	for _, cmd := range []string{"create", "run"} {
		out, err := runCLI(t, api.NewFake(), cmd, "--help")
		if err != nil {
			t.Fatalf("%s --help: %v", cmd, err)
		}
		for _, name := range api.AgentNames() {
			if !strings.Contains(out, name) {
				t.Errorf("%s --help missing agent %q:\n%s", cmd, name, out)
			}
		}
		if !strings.Contains(out, "defaults to "+api.DefaultAgent) {
			t.Errorf("%s --help should name the default agent:\n%s", cmd, out)
		}
	}
}

func TestCutEnv(t *testing.T) {
	cases := []struct {
		in    string
		name  string
		value string
		ok    bool
	}{
		{"FOO=bar", "FOO", "bar", true},
		{"FOO=", "FOO", "", true},
		{"FOO=a=b", "FOO", "a=b", true},
		{"=bar", "", "", false},
		{"FOO", "", "", false},
	}
	for _, tc := range cases {
		name, value, ok := cutEnv(tc.in)
		if name != tc.name || value != tc.value || ok != tc.ok {
			t.Errorf("cutEnv(%q) = %q, %q, %v; want %q, %q, %v",
				tc.in, name, value, ok, tc.name, tc.value, tc.ok)
		}
	}
}

func TestExecCommand(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      []string
		want    []string
		wantErr bool
	}{
		{name: "plain", in: []string{"ls", "-la"}, want: []string{"ls", "-la"}},
		// what anyone who has used `docker exec` or `kubectl exec` will type.
		{name: "after a dash dash", in: []string{"--", "ls", "-la"}, want: []string{"ls", "-la"}},
		// only the separator goes; a later one belongs to the command.
		{
			name: "a second one is the command's", in: []string{"--", "git", "log", "--", "f"},
			want: []string{"git", "log", "--", "f"},
		},
		{name: "nothing but the separator", in: []string{"--"}, wantErr: true},
		{name: "nothing at all", in: nil, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := execCommand(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("execCommand(%v) = %v, want an error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("execCommand(%v): %v", tc.in, err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("execCommand(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
