package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/rhizomatous/planterbox/internal/api"
)

var now = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

// plain strips styling so assertions read against the text alone.
func plain(s string) string { return ansi.Strip(s) }

func TestRenderSandboxesEmpty(t *testing.T) {
	got := plain(RenderSandboxes(nil, now))
	if got != "no sandboxes yet" {
		t.Errorf("RenderSandboxes(nil) = %q, want a hint rather than a bare header", got)
	}
}

func TestRenderSandboxesTable(t *testing.T) {
	out := plain(RenderSandboxes([]api.Sandbox{
		{
			Spec: api.Spec{
				Name:       "myrepo",
				Agent:      "claude",
				Image:      "ghcr.io/acme/base:1",
				Workspaces: []api.Workspace{{Host: "/home/viv/myrepo"}},
				CreatedAt:  now.Add(-3 * time.Hour),
			},
			State: api.State{Status: api.StatusRunning},
		},
		{
			Spec:  api.Spec{Name: "scratch", CreatedAt: now.Add(-50 * time.Hour)},
			State: api.State{Status: api.StatusStopped},
		},
	}, now))

	lines := strings.Split(out, "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want a header plus 2 rows:\n%s", len(lines), out)
	}
	for _, col := range SandboxColumns {
		if !strings.Contains(lines[0], col) {
			t.Errorf("header %q missing column %q", lines[0], col)
		}
	}
	for _, want := range []string{"myrepo", "running", "claude", "ghcr.io/acme/base:1", "/home/viv/myrepo", "3 hours ago"} {
		if !strings.Contains(lines[1], want) {
			t.Errorf("row %q missing %q", lines[1], want)
		}
	}
	if !strings.Contains(lines[2], "stopped") || !strings.Contains(lines[2], "2 days ago") {
		t.Errorf("row %q missing its status or age", lines[2])
	}
}

func TestRenderSandboxesFillsEmptyCells(t *testing.T) {
	// a blank cell reads as a rendering bug; a dash reads as "not set".
	out := plain(RenderSandboxes([]api.Sandbox{
		{Spec: api.Spec{Name: "bare"}, State: api.State{Status: api.StatusCreated}},
	}, now))
	row := strings.Split(out, "\n")[1]
	if strings.Count(row, "-") < 3 {
		t.Errorf("row %q should show a placeholder for each unset column", row)
	}
}

func TestRenderSandboxesColumnsAlign(t *testing.T) {
	out := plain(RenderSandboxes([]api.Sandbox{
		{Spec: api.Spec{Name: "a", CreatedAt: now}, State: api.State{Status: api.StatusRunning}},
		{Spec: api.Spec{Name: "a-much-longer-name", CreatedAt: now}, State: api.State{Status: api.StatusStopped}},
	}, now))

	lines := strings.Split(out, "\n")
	col := strings.Index(lines[0], "STATUS")
	for i, line := range lines[1:] {
		if idx := strings.Index(line, string(api.StatusRunning)); i == 0 && idx != col {
			t.Errorf("row 0 status starts at %d, want the header's column %d:\n%s", idx, col, out)
		}
		if idx := strings.Index(line, string(api.StatusStopped)); i == 1 && idx != col {
			t.Errorf("row 1 status starts at %d, want the header's column %d:\n%s", idx, col, out)
		}
	}
}

func TestRenderSandboxesHasNoTrailingWhitespace(t *testing.T) {
	out := plain(RenderSandboxes([]api.Sandbox{
		{Spec: api.Spec{Name: "a", CreatedAt: now}, State: api.State{Status: api.StatusRunning}},
	}, now))
	for _, line := range strings.Split(out, "\n") {
		if line != strings.TrimRight(line, " ") {
			t.Errorf("line %q has trailing whitespace", line)
		}
	}
}

// detailed is a sandbox with every optional field set, so a renderer that
// drops one is caught.
func detailed() api.Sandbox {
	return api.Sandbox{
		Spec: api.Spec{
			Name:  "myrepo",
			Agent: "claude",
			Image: "ghcr.io/acme/base:1",
			Workspaces: []api.Workspace{
				{Host: "/home/viv/myrepo"},
				{Host: "/home/viv/shared", ReadOnly: true},
			},
			Resources: api.Resources{CPUs: 4, Memory: 2 << 30},
			Env:       map[string]string{"FOO": "bar"},
			CreatedAt: now.Add(-2 * time.Hour),
		},
		State: api.State{Status: api.StatusRunning},
		Ports: []api.Port{{Host: 3000, Sandbox: 3000}},
	}
}

func TestRenderSandboxCoversTheSpec(t *testing.T) {
	out := plain(RenderSandbox(detailed(), now))
	for _, want := range []string{
		"myrepo", "running", "claude", "ghcr.io/acme/base:1", "2 hours ago",
		"/home/viv/myrepo", "/home/viv/shared", "read-only",
		"4 cpu", "2GiB", "3000 → 3000", "FOO=bar",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("detail view missing %q:\n%s", want, out)
		}
	}
}

func TestRenderSandboxFieldsLeavesTheNamingToItsCaller(t *testing.T) {
	fields := plain(RenderSandboxFields(detailed(), now))
	// the workspace paths end in the name too, so this checks the first line
	// rather than the whole block.
	if first := strings.SplitN(fields, "\n", 2)[0]; !strings.Contains(first, "status") {
		t.Errorf("the fields should open on the definition, not a heading of their own:\n%s", fields)
	}
	if !strings.Contains(fields, "running") || !strings.Contains(fields, "claude") {
		t.Errorf("the fields should still carry the definition:\n%s", fields)
	}
	if !strings.HasPrefix(plain(RenderSandbox(detailed(), now)), "🪴 myrepo\n") {
		t.Error("RenderSandbox should head those same fields with the name")
	}
}

func TestAge(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "just now"},
		{30 * time.Second, "just now"},
		{time.Minute, "1 minute ago"},
		{5 * time.Minute, "5 minutes ago"},
		{time.Hour, "1 hour ago"},
		{3 * time.Hour, "3 hours ago"},
		{24 * time.Hour, "1 day ago"},
		{50 * time.Hour, "2 days ago"},
	}
	for _, tc := range cases {
		if got := Age(now.Add(-tc.in), now); got != tc.want {
			t.Errorf("Age(-%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAgeHandlesZeroAndFutureTimes(t *testing.T) {
	if got := Age(time.Time{}, now); got != "-" {
		t.Errorf("Age(zero) = %q, want a placeholder", got)
	}
	// clock skew shouldn't produce "-3 minutes ago".
	if got := Age(now.Add(time.Hour), now); got != "just now" {
		t.Errorf("Age(future) = %q, want just now", got)
	}
}

func TestStatusStyleDistinguishesPostures(t *testing.T) {
	// running, stopped, and missing must not all render identically.
	running := StatusStyle(api.StatusRunning).Render("x")
	missing := StatusStyle(api.StatusMissing).Render("x")
	if running == missing {
		t.Error("running and missing should not render the same")
	}
}
