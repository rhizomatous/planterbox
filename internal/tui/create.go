package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/rhizomatous/planterbox/internal/api"
)

// createForm collects what a new sandbox needs.
//
// Only what has no sensible default, or cannot be changed later. Resource
// limits stay on the CLI, where a flag is quicker than a form field, and ports
// are not asked for at all because `plbx ports` changes them whenever. Clone
// mode is here because it is neither: it is fixed when the sandbox is made,
// and it decides whether an agent can reach the files you keep outside it.
type createForm struct {
	form      *huh.Form
	workspace string
	agent     string
	name      string
	clone     bool
}

// newCreateForm builds the form, defaulting the workspace to the directory plbx
// was started in.
func newCreateForm(cwd string) *createForm {
	c := &createForm{workspace: cwd, agent: api.DefaultAgent}

	c.form = huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("workspace").
				Description("the directory to mount, at this same path inside").
				Value(&c.workspace).
				Validate(validWorkspace),

			huh.NewSelect[string]().
				Title("agent").
				Options(agentOptions()...).
				Value(&c.agent),

			huh.NewInput().
				Title("name").
				// the only field whose effective value is not the one on
				// screen: blank means the workspace's own name, and saying
				// which saves finding out after the sandbox exists.
				DescriptionFunc(c.nameDescription, &c.workspace).
				Value(&c.name).
				Validate(validOptionalName),

			huh.NewSelect[bool]().
				Title("workspace access").
				Description("clone mode cannot be changed later").
				Options(
					huh.NewOption("read-write — the agent edits your files directly", false),
					huh.NewOption("clone — read-only, the agent works in its own copy", true),
				).
				Value(&c.clone),
		),
	)
	return c
}

// nameDescription says what a blank name will actually produce, which follows
// the workspace as it is typed.
func (c *createForm) nameDescription() string {
	if c.name != "" {
		return "the sandbox's name"
	}
	abs, err := filepath.Abs(c.workspace)
	if err != nil || c.workspace == "" {
		return "leave blank to name it after the directory"
	}
	return "leave blank for " + api.SandboxName(abs)
}

// agentOptions offers each agent by the name it calls itself, and yields the
// slug the rest of plbx uses. A list of four slugs is fine once you know them
// and no help at all before.
func agentOptions() []huh.Option[string] {
	agents := api.Agents()
	options := make([]huh.Option[string], 0, len(agents))
	for _, a := range agents {
		options = append(options, huh.NewOption(a.Label(), a.Name))
	}
	return options
}

// validWorkspace rejects a path that is not a directory plbx can mount, so the
// form says so while the user is still in it.
func validWorkspace(path string) error {
	if path == "" {
		return errors.New("a workspace is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return errors.New("no such directory")
	}
	if !fi.IsDir() {
		return errors.New("not a directory")
	}
	return nil
}

// validOptionalName accepts an empty name, which means "derive one".
func validOptionalName(name string) error {
	if name == "" || api.ValidName(name) {
		return nil
	}
	return errors.New("letters, digits, dot, dash, or underscore, starting with a letter or digit")
}

// spec turns the answers into a Spec. Called only once the form completes, so
// the workspace has already been validated.
func (c *createForm) spec() (api.Spec, error) {
	abs, err := filepath.Abs(c.workspace)
	if err != nil {
		return api.Spec{}, err
	}
	def, err := api.LookupAgent(c.agent)
	if err != nil {
		return api.Spec{}, err
	}

	name := c.name
	if name == "" {
		name = api.SandboxName(abs)
	}
	return api.Spec{
		Name:       name,
		Agent:      c.agent,
		Image:      def.Image,
		Workspaces: []api.Workspace{{Host: abs}},
		Clone:      c.clone,
	}, nil
}

// startCreate opens the form.
func (m *Model) startCreate() tea.Cmd {
	cwd, err := os.Getwd()
	if err != nil {
		m.status = err.Error()
		return nil
	}
	m.create = newCreateForm(cwd)
	return m.create.form.Init()
}

// updateCreate feeds a message to the open form and acts on the result.
func (m *Model) updateCreate(msg tea.Msg) (tea.Model, tea.Cmd) {
	// esc backs out. huh does not bind it, and abandoning a form is the first
	// thing anyone will press it for.
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "esc", "escape":
			m.create = nil
			return m, nil
		}
	}

	form, cmd := m.create.form.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		m.create.form = f
	}

	switch m.create.form.State {
	case huh.StateCompleted:
		spec, err := m.create.spec()
		m.create = nil
		if err != nil {
			m.status = err.Error()
			return m, nil
		}
		m.building, m.buildStep = &spec, "creating…"
		return m, m.submitCreate(spec)

	case huh.StateAborted:
		m.create = nil
		return m, nil
	}
	return m, cmd
}

// submitCreate builds the sandbox.
func (m *Model) submitCreate(spec api.Spec) tea.Cmd {
	return func() tea.Msg {
		// the pull runs here rather than inside Create so the dashboard can
		// report it. On a first run for an agent it is a multi-gigabyte
		// download and the container that follows is instant, so a single
		// "creating…" would sit through minutes of the wrong explanation.
		lines, err := m.svc.PullImage(context.Background(), spec.Image)
		if err != nil {
			// Create pulls on its own if it must; let it fail properly
			// rather than reporting this instead.
			return pullMsg{spec: spec, done: true}
		}
		return pullMsg{spec: spec, lines: lines}
	}
}

// nextPullLine reads one line of an open pull.
//
// The channel travels in the message rather than living on the model: it
// belongs to this one create, and a model field would outlive it.
func (m *Model) nextPullLine(p pullMsg) tea.Cmd {
	return func() tea.Msg {
		line, ok := <-p.lines
		if !ok {
			return pullMsg{spec: p.spec, done: true}
		}
		return pullMsg{spec: p.spec, lines: p.lines, line: line}
	}
}

// buildSandbox makes the container, once its image is here.
func (m *Model) buildSandbox(spec api.Spec) tea.Cmd {
	return func() tea.Msg {
		_, err := m.svc.Create(context.Background(), spec)
		// the sandbox is made either way; what failed is writing a remote into
		// a repository that is the user's, not ours.
		if errors.Is(err, api.ErrRemoteNotAdded) {
			return actionMsg{verb: "created, without a git remote", name: spec.Name}
		}
		return actionMsg{verb: "created", name: spec.Name, err: err}
	}
}
