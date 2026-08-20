package tui

import (
	"context"
	"errors"
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/rhizomatous/planterbox/internal/api"
	"github.com/rhizomatous/planterbox/internal/proxy"
)

// Key is one binding, as the help panel lists it.
type Key struct {
	Keys []string
	Help string
}

// Keys are the dashboard's bindings, in the order `?` shows them.
var Keys = []Key{
	{Keys: []string{"↑/k", "↓/j"}, Help: "move"},
	{Keys: []string{"tab"}, Help: "switch between sandboxes and network"},
	{Keys: []string{"i"}, Help: "details"},
	{Keys: []string{"c"}, Help: "create a sandbox"},
	{Keys: []string{"enter"}, Help: "attach the agent"},
	{Keys: []string{"x"}, Help: "shell"},
	{Keys: []string{"s"}, Help: "start / stop"},
	{Keys: []string{"r"}, Help: "remove"},
	{Keys: []string{"a"}, Help: "allow the selected host (network)"},
	{Keys: []string{"d"}, Help: "deny the selected host (network)"},
	{Keys: []string{"f"}, Help: "show only the selected sandbox (network)"},
	{Keys: []string{"?"}, Help: "help"},
	{Keys: []string{"q"}, Help: "quit"},
}

// handleKey maps a keypress onto an action.
func (m *Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// while an action is in flight, only the keys that change nothing about a
	// sandbox are allowed, so a second keypress cannot race the first against
	// the same one.
	switch msg.String() {
	case "q", "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "?":
		m.showHelp = !m.showHelp
		return m, nil
	case "i":
		m.showDetail = !m.showDetail
		return m, nil
	case "tab":
		if m.panel == sandboxPanel {
			m.panel = networkPanel
		} else {
			m.panel = sandboxPanel
		}
		return m, nil
	}
	if m.pending != "" {
		return m, nil
	}

	if m.panel == networkPanel {
		return m.handleNetworkKey(msg)
	}

	switch msg.String() {
	case "up", "k":
		m.cursor--
		m.clampCursor()
	case "down", "j":
		m.cursor++
		m.clampCursor()
	case "g", "home":
		m.cursor = 0
	case "G", "end":
		m.cursor = len(m.sandboxes) - 1
		m.clampCursor()
	case "c":
		return m, m.startCreate()
	case "enter":
		return m, m.attachSelected(agentCommand)
	case "x":
		return m, m.attachSelected(shellCommand)
	case "s":
		return m, m.toggleSelected()
	case "r":
		return m, m.removeSelected()
	}
	return m, nil
}

// sessionCommand decides what a sandbox is attached with.
type sessionCommand func(api.Sandbox) ([]string, error)

// agentCommand runs the sandbox's own agent, which is the binary its image
// actually has.
func agentCommand(sb api.Sandbox) ([]string, error) {
	def, err := api.LookupAgent(sb.Spec.Agent)
	if err != nil {
		return nil, err
	}
	return def.Command, nil
}

// shellCommand opens a login shell, for poking at a sandbox without the agent.
func shellCommand(api.Sandbox) ([]string, error) {
	return []string{"bash", "-l"}, nil
}

// attachSelected leaves the dashboard so the terminal can go to the session.
// Starting a stopped sandbox first is what makes Enter work from any row.
func (m *Model) attachSelected(command sessionCommand) tea.Cmd {
	sb, ok := m.selected()
	if !ok {
		return nil
	}
	cmd, err := command(sb)
	if err != nil {
		m.status = err.Error()
		return nil
	}

	m.attach = &AttachRequest{
		Sandbox: sb.Spec.Name,
		Cmd:     cmd,
		Workdir: sb.Spec.Workdir(),
	}
	m.quitting = true
	return tea.Quit
}

// toggleSelected starts a stopped sandbox and stops a running one.
func (m *Model) toggleSelected() tea.Cmd {
	sb, ok := m.selected()
	if !ok {
		return nil
	}
	name := sb.Spec.Name
	m.pending = name

	if sb.State.Status == api.StatusRunning {
		return func() tea.Msg {
			err := m.svc.Stop(context.Background(), api.ByName(name))
			return actionMsg{verb: "stopped", name: name, err: err}
		}
	}
	return func() tea.Msg {
		err := m.svc.Start(context.Background(), api.ByName(name))
		// the sandbox is up either way; only its ports are not, and the
		// dashboard should not show a start as having failed.
		if errors.Is(err, api.ErrPortsUnavailable) {
			return actionMsg{verb: "started, without its ports", name: name}
		}
		return actionMsg{verb: "started", name: name, err: err}
	}
}

// removeSelected deletes the selected sandbox.
//
// A running one is refused rather than force-removed: the service's guard
// exists so a live session is not destroyed by a single keystroke, and the
// dashboard would defeat it by passing force.
func (m *Model) removeSelected() tea.Cmd {
	sb, ok := m.selected()
	if !ok {
		return nil
	}
	name := sb.Spec.Name
	m.pending = name

	return func() tea.Msg {
		err := m.svc.Remove(context.Background(), api.ByName(name), false)
		if errors.Is(err, api.ErrRunning) {
			return actionMsg{verb: "not removed", name: name, err: errors.New("stop it first")}
		}
		return actionMsg{verb: "removed", name: name, err: err}
	}
}

// handleNetworkKey drives the connection panel.
//
// The whole point of the panel is the loop from a denial to a rule: the agent
// stalls, the reason is on screen, and one keystroke fixes it without leaving
// the dashboard or restarting anything.
func (m *Model) handleNetworkKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		m.connCursor--
		m.clampConnCursor()
	case "down", "j":
		m.connCursor++
		m.clampConnCursor()
	case "g", "home":
		m.connCursor = 0
	case "G", "end":
		m.connCursor = len(m.visibleConnections()) - 1
		m.clampConnCursor()
	case "f":
		// narrowing to one sandbox is what turns a shared log into that
		// sandbox's own, which is the question being asked when an agent
		// has just failed to reach something.
		m.connFilter = !m.connFilter
		m.clampConnCursor()
	case "a":
		return m, m.ruleForSelected(true)
	case "d":
		return m, m.ruleForSelected(false)
	}
	return m, nil
}

// ruleForSelected writes a rule for the host under the cursor.
//
// The rule names the host without its port. A denial usually arrives on 443
// and pinning that would leave the same host blocked on 80, which reads as the
// allow having silently not worked.
func (m *Model) ruleForSelected(allow bool) tea.Cmd {
	entry, ok := m.selectedEntry()
	if !ok {
		return nil
	}
	pattern := entry.Target.Host
	m.pending = pattern

	return func() tea.Msg {
		ctx := context.Background()
		policy, err := m.svc.Policy(ctx)
		if err != nil {
			return ruleMsg{pattern: pattern, allow: allow, err: err}
		}
		// replace any rule already holding this host, or an allow lands beside
		// the deny it was meant to overturn and the deny still wins.
		policy.Rules = slices.DeleteFunc(policy.Rules, func(r proxy.Rule) bool {
			return r.Pattern == pattern
		})
		policy.Rules = append(policy.Rules, proxy.Rule{Pattern: pattern, Allow: allow})

		err = m.svc.SetPolicy(ctx, policy)
		return ruleMsg{pattern: pattern, allow: allow, err: err}
	}
}
