package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/rhizomatous/jardiniere/internal/api"
)

func TestApplyPorts(t *testing.T) {
	current := []api.Port{{Host: 3000, Sandbox: 3000}, {Host: 8080, Sandbox: 80}}

	for _, tc := range []struct {
		name    string
		added   []api.Port
		removed []int
		want    []api.Port
	}{
		{
			name:  "adding keeps what was there",
			added: []api.Port{{Host: 5432, Sandbox: 5432}},
			want: []api.Port{
				{Host: 3000, Sandbox: 3000}, {Host: 5432, Sandbox: 5432}, {Host: 8080, Sandbox: 80},
			},
		},
		{
			// a host port can only go one place, so the later mapping wins
			// rather than both being sent to the runtime to argue about.
			name:  "republishing a host port replaces it",
			added: []api.Port{{Host: 8080, Sandbox: 9000}},
			want:  []api.Port{{Host: 3000, Sandbox: 3000}, {Host: 8080, Sandbox: 9000}},
		},
		{
			name:    "unpublishing names the host side",
			removed: []int{8080},
			want:    []api.Port{{Host: 3000, Sandbox: 3000}},
		},
		{
			name:    "removal wins over an addition in the same command",
			added:   []api.Port{{Host: 5432, Sandbox: 5432}},
			removed: []int{5432},
			want:    []api.Port{{Host: 3000, Sandbox: 3000}, {Host: 8080, Sandbox: 80}},
		},
		{
			name:    "everything can go",
			removed: []int{3000, 8080},
			want:    []api.Port{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := applyPorts(current, tc.added, tc.removed)
			if !slices.Equal(got, tc.want) {
				t.Errorf("applyPorts = %+v, want %+v", got, tc.want)
			}
			// the input is a sandbox's stored set; editing it in place would
			// change the record before the service agreed to it.
			if len(current) != 2 || current[1].Host != 8080 {
				t.Errorf("the current set was modified: %+v", current)
			}
		})
	}
}

func TestPortsListsWhatASandboxPublishes(t *testing.T) {
	fake := api.NewFake(api.Sandbox{
		Spec:  api.Spec{Name: "demo"},
		State: api.State{Status: api.StatusRunning},
		Ports: []api.Port{{Host: 8080, Sandbox: 80}},
	})

	out, err := runCLI(t, fake, "ports", "demo")
	if err != nil {
		t.Fatalf("ports: %v", err)
	}
	if !strings.Contains(out, "8080") || !strings.Contains(out, "80") {
		t.Errorf("output = %q, want the mapping", out)
	}
}

func TestPortsPublishAndUnpublish(t *testing.T) {
	fake := api.NewFake(api.Sandbox{
		Spec:  api.Spec{Name: "demo"},
		State: api.State{Status: api.StatusRunning},
	})

	if _, err := runCLI(t, fake, "ports", "demo", "--publish", "8080:80"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if got := fake.Sandboxes[0].Ports; len(got) != 1 || got[0] != (api.Port{Host: 8080, Sandbox: 80}) {
		t.Fatalf("ports = %+v, want 8080 → 80", got)
	}

	if _, err := runCLI(t, fake, "ports", "demo", "--unpublish", "8080"); err != nil {
		t.Fatalf("unpublish: %v", err)
	}
	if got := fake.Sandboxes[0].Ports; len(got) != 0 {
		t.Errorf("ports = %+v, want none left", got)
	}
}

// A stopped sandbox records its ports without binding them, so the listing has
// to say which of the two it is showing.
func TestPortsSaysWhenASandboxIsNotRunning(t *testing.T) {
	fake := api.NewFake(api.Sandbox{
		Spec:  api.Spec{Name: "demo"},
		State: api.State{Status: api.StatusStopped},
		Ports: []api.Port{{Host: 8080, Sandbox: 80}},
	})

	out, err := runCLI(t, fake, "ports", "demo")
	if err != nil {
		t.Fatalf("ports: %v", err)
	}
	if !strings.Contains(out, "starts") {
		t.Errorf("output = %q, want it to say the ports are not bound yet", out)
	}
}

func TestPortsRejectsUDP(t *testing.T) {
	fake := api.NewFake(api.Sandbox{Spec: api.Spec{Name: "demo"}})
	if _, err := runCLI(t, fake, "ports", "demo", "--publish", "5353:53/udp"); err == nil {
		t.Error("ports accepted a udp mapping; only tcp can be published")
	}
}
