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
	// Title is what the agent calls itself, and Vendor who makes it. The
	// registry knew neither, so every surface offering a choice offered four
	// slugs — which is fine once you know them and no help at all before.
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
	// shell has no agent to start; it is the bare sandbox, and the one to reach
	// for when you want to test the lifecycle without an agent in the way. No
	// vendor, because nobody makes a shell.
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
