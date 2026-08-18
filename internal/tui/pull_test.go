package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/rhizomatous/planterbox/internal/api"
)

// TestPullLinesReachTheScreen covers the long half of a create. A first run
// for an agent is a multi-gigabyte download and the container that follows is
// instant, so a single "creating…" would sit through minutes of the wrong
// explanation.
func TestPullLinesReachTheScreen(t *testing.T) {
	lines := make(chan string, 2)
	lines <- "5711127a7748: Pulling fs layer"
	lines <- "5711127a7748: Pull complete"
	close(lines)

	m := loaded(t, api.NewFake())
	spec := api.Spec{Name: "myrepo", Agent: "shell", Image: "base:1"}
	m.building = &spec

	msg := m.nextPullLine(pullMsg{spec: spec, lines: lines})()
	first, ok := msg.(pullMsg)
	if !ok || first.line != "5711127a7748: Pulling fs layer" {
		t.Fatalf("first read = %+v, want the first pull line", msg)
	}

	// the update loop puts it on the row that stands in for the sandbox
	updated, _ := m.Update(first)
	m = updated.(*Model)
	if got := view(m); !strings.Contains(got, "Pulling fs layer") {
		t.Errorf("the pull should be on screen:\n%s", got)
	}

	// drained, it hands over to the build
	second := m.nextPullLine(first)().(pullMsg)
	if second.line != "5711127a7748: Pull complete" {
		t.Errorf("second read = %q, want the second line", second.line)
	}
	done := m.nextPullLine(second)().(pullMsg)
	if !done.done {
		t.Errorf("a closed pull should report done: %+v", done)
	}
}

// a pull that cannot even start must not strand the create: Create pulls on
// its own if it has to.
func TestAFailedPullStillBuilds(t *testing.T) {
	fake := api.NewFake()
	fake.Err = errors.New("no runtime here")
	m := New(fake)

	spec := api.Spec{Name: "myrepo", Agent: "shell", Image: "base:1"}
	msg, ok := m.submitCreate(spec)().(pullMsg)
	if !ok || !msg.done {
		t.Fatalf("a failed pull should hand straight to the build: %+v", msg)
	}
}
