package api

import (
	"strings"
	"testing"
)

// valid returns a spec that passes, for tests that break one field at a time.
func valid() Spec {
	return Spec{
		Name:       "demo",
		Image:      "base:1",
		Workspaces: []Workspace{{Host: "/home/viv/demo"}},
	}
}

func TestValidateAcceptsAWellFormedSpec(t *testing.T) {
	spec := valid()
	spec.Agent = "claude"
	spec.Env = map[string]string{"FOO": "bar", "EMPTY_VALUE": ""}
	spec.Resources = Resources{CPUs: 2.5, Memory: 8 << 30}
	if err := spec.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestValidateAcceptsASpecWithNoWorkspaces(t *testing.T) {
	spec := valid()
	spec.Workspaces = nil
	if err := spec.Validate(); err != nil {
		t.Errorf("a sandbox with no workspace is legal: %v", err)
	}
}

func TestValidateRejectsBadNames(t *testing.T) {
	for _, name := range []string{"", "-leading", ".leading", "has space", "has/slash", "has:colon", strings.Repeat("x", 64)} {
		spec := valid()
		spec.Name = name
		if err := spec.Validate(); err == nil {
			t.Errorf("name %q should be rejected", name)
		}
	}
}

func TestValidateRejectsEmptyImage(t *testing.T) {
	for _, image := range []string{"", "   "} {
		spec := valid()
		spec.Image = image
		if err := spec.Validate(); err == nil {
			t.Errorf("image %q should be rejected", image)
		}
	}
}

func TestValidateRejectsUnknownAgent(t *testing.T) {
	spec := valid()
	spec.Agent = "gemini"
	err := spec.Validate()
	if err == nil {
		t.Fatal("an unsupported agent should be rejected")
	}
	if !strings.Contains(err.Error(), "claude") {
		t.Errorf("err = %v, want it to list the supported agents", err)
	}
}

func TestValidateRejectsAColonInAWorkspacePath(t *testing.T) {
	// the one that matters: a mount spec is colon-delimited, so this would bind
	// a different directory than the one asked for rather than erroring.
	spec := valid()
	spec.Workspaces = []Workspace{{Host: "/home/viv/a:b"}}
	err := spec.Validate()
	if err == nil {
		t.Fatal("a workspace path containing a colon should be rejected")
	}
	if !strings.Contains(err.Error(), "colon") {
		t.Errorf("err = %v, want it to name the problem", err)
	}
}

func TestValidateRejectsBadWorkspaces(t *testing.T) {
	cases := []struct {
		name string
		in   []Workspace
	}{
		{name: "empty path", in: []Workspace{{Host: ""}}},
		{name: "relative path", in: []Workspace{{Host: "relative/dir"}}},
		{name: "duplicate", in: []Workspace{{Host: "/a"}, {Host: "/a"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := valid()
			spec.Workspaces = tc.in
			if err := spec.Validate(); err == nil {
				t.Errorf("%v should be rejected", tc.in)
			}
		})
	}
}

func TestValidateRejectsBadEnvKeys(t *testing.T) {
	// "K=V=x" would bind K to "V=x" rather than failing.
	for _, key := range []string{"", "HAS=EQUALS", "HAS\x00NULL"} {
		spec := valid()
		spec.Env = map[string]string{key: "v"}
		if err := spec.Validate(); err == nil {
			t.Errorf("env key %q should be rejected", key)
		}
	}
}

func TestValidateRejectsBadPorts(t *testing.T) {
	cases := []struct {
		name string
		in   Port
	}{
		{name: "zero host", in: Port{Host: 0, Sandbox: 80}},
		{name: "zero sandbox", in: Port{Host: 80, Sandbox: 0}},
		{name: "negative", in: Port{Host: -1, Sandbox: 80}},
		{name: "above range", in: Port{Host: 70000, Sandbox: 80}},
		{name: "bad proto", in: Port{Host: 80, Sandbox: 80, Proto: "sctp"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidatePorts([]Port{tc.in}); err == nil {
				t.Errorf("port %+v should be rejected", tc.in)
			}
		})
	}
}

func TestValidateRejectsNegativeResources(t *testing.T) {
	// the renderer omits any limit that is not positive, so an unrejected
	// negative would leave the user having asked for a limit and got none.
	for _, r := range []Resources{{CPUs: -1}, {Memory: -1}} {
		spec := valid()
		spec.Resources = r
		if err := spec.Validate(); err == nil {
			t.Errorf("resources %+v should be rejected", r)
		}
	}
}

func TestLookupAgent(t *testing.T) {
	for _, name := range AgentNames() {
		a, err := LookupAgent(name)
		if err != nil {
			t.Errorf("LookupAgent(%q): %v", name, err)
			continue
		}
		if a.Image == "" || len(a.Command) == 0 {
			t.Errorf("agent %q = %+v, want an image and a command", name, a)
		}
	}
	if _, err := LookupAgent("nope"); err == nil {
		t.Error("an unknown agent should error")
	}
}

func TestDefaultAgentIsSupported(t *testing.T) {
	if _, err := LookupAgent(DefaultAgent); err != nil {
		t.Errorf("DefaultAgent %q is not in the registry: %v", DefaultAgent, err)
	}
}

func TestAgentsIsACopy(t *testing.T) {
	got := Agents()
	got[0].Name = "clobbered"
	if Agents()[0].Name == "clobbered" {
		t.Error("Agents returned the package's own slice")
	}
}

// A sandbox is alone on an internal network and cannot publish for itself, so
// its ports ride a tcp forwarder. Refusing udp when the ports are asked for
// means the command naming the port is the one that fails.
func TestValidateRejectsUDPPorts(t *testing.T) {
	err := ValidatePorts([]Port{{Host: 5353, Sandbox: 53, Proto: "udp"}})
	if err == nil {
		t.Fatal("Validate accepted a udp port; only tcp can be published")
	}
	if !strings.Contains(err.Error(), "tcp") {
		t.Errorf("error = %q, want it to name the protocol that does work", err)
	}
}

// A host port can only be published once; two rules for the same one is a
// mistake the runtime would report much later and much less clearly.
func TestValidatePortsRejectsADuplicateHostPort(t *testing.T) {
	err := ValidatePorts([]Port{{Host: 8080, Sandbox: 80}, {Host: 8080, Sandbox: 3000}})
	if err == nil {
		t.Fatal("ValidatePorts accepted the same host port twice")
	}
}

// Clone mode clones the primary workspace, so there has to be one.
func TestValidateRejectsCloneWithNoWorkspace(t *testing.T) {
	spec := valid()
	spec.Workspaces = nil
	spec.Clone = true
	if err := spec.Validate(); err == nil {
		t.Error("Validate accepted --clone with nothing to clone")
	}
}

// The clone cannot live at the workspace's own path: the read-only mount of
// the original is already there.
func TestCloneDirAndWorkdir(t *testing.T) {
	spec := valid()
	spec.Workspaces = []Workspace{{Host: "/home/viv/myrepo"}}

	if got := spec.Workdir(); got != "/home/viv/myrepo" {
		t.Errorf("Workdir() = %q, want the workspace itself outside clone mode", got)
	}

	spec.Clone = true
	if got := spec.CloneDir(); got != "/home/agent/myrepo" {
		t.Errorf("CloneDir() = %q, want it under the home volume", got)
	}
	if got := spec.Workdir(); got != spec.CloneDir() {
		t.Errorf("Workdir() = %q, want the clone — the original is read-only", got)
	}
}
