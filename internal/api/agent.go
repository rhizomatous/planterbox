package api

import (
	"fmt"
	"slices"
	"strings"
)

// imageRepo is where plbx's base images are published. A sandbox may start from
// any image via Spec.Image; these are only the defaults.
const imageRepo = "ghcr.io/rhizomatous"

// Agent is a coding agent plbx knows how to start: which image it ships in and
// what command runs it.
type Agent struct {
	Name string
	// Title is what the agent calls itself, Vendor who makes it. Every surface
	// offering a choice shows both, because a slug alone means nothing to
	// someone who hasn't used plbx before.
	Title   string
	Vendor  string
	Image   string
	Command []string
}

// Label is the agent as a person picking one would recognise it.
func (a Agent) Label() string {
	if a.Title == "" {
		return a.Name
	}
	if a.Vendor == "" {
		return a.Title
	}
	return a.Title + " · " + a.Vendor
}

// agents are the supported agents, in the order the help and the create form
// offer them.
var agents = []Agent{
	{
		Name: "claude", Title: "Claude Code", Vendor: "Anthropic",
		Image: imageRepo + "/plbx-claude:latest", Command: []string{"claude"},
	},
	{
		Name: "codex", Title: "Codex", Vendor: "OpenAI",
		Image: imageRepo + "/plbx-codex:latest", Command: []string{"codex"},
	},
	{
		Name: "opencode", Title: "OpenCode", Vendor: "SST",
		Image: imageRepo + "/plbx-opencode:latest", Command: []string{"opencode"},
	},
	// shell is the bare sandbox: no agent to start, and nobody to credit.
	{
		Name: "shell", Title: "A plain shell",
		Image: imageRepo + "/plbx-shell:latest", Command: []string{"bash", "-l"},
	},
}

// DefaultAgent is what `plbx run` starts when no agent is named.
const DefaultAgent = "claude"

// Agents returns every supported agent.
func Agents() []Agent { return slices.Clone(agents) }

// AgentNames returns the supported agent names, for help text and errors.
func AgentNames() []string {
	names := make([]string, len(agents))
	for i, a := range agents {
		names[i] = a.Name
	}
	return names
}

// LookupAgent returns the named agent.
func LookupAgent(name string) (Agent, error) {
	for _, a := range agents {
		if a.Name == name {
			return a, nil
		}
	}
	return Agent{}, fmt.Errorf("unknown agent %q: must be one of %s",
		name, strings.Join(AgentNames(), ", "))
}
