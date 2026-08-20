package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/rhizomatous/planterbox/internal/api"
	"github.com/rhizomatous/planterbox/internal/proxy"
	"github.com/rhizomatous/planterbox/internal/ui"
)

const (
	// gaugeWidth is how many cells the CPU and memory bars occupy.
	gaugeWidth = 12
	// detailIndent aligns the detail pane's rule with the rows' own indent.
	detailIndent = 2
)

var (
	cursorStyle   = lipgloss.NewStyle().Bold(true)
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	gaugeFull     = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	gaugeEmpty    = lipgloss.NewStyle().Faint(true)
)

// View renders the dashboard.
func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	// the dashboard owns the whole screen and restores what was there on exit.
	v.AltScreen = !m.quitting
	return v
}

func (m *Model) render() string {
	var b strings.Builder
	b.WriteString(ui.Title.Render("🪴 planterbox"))
	b.WriteString("\n\n")

	// the form replaces the list rather than sitting beside it: it owns the
	// keyboard while open, and showing a list you cannot move through invites
	// the user to try.
	if m.create != nil {
		b.WriteString(m.create.form.View())
		b.WriteString("\n" + ui.Faint.Render("esc cancel"))
		return b.String()
	}

	b.WriteString(m.renderTabs())
	b.WriteString("\n\n")

	if m.panel == networkPanel {
		b.WriteString(m.renderConnections())
		if m.status != "" {
			b.WriteString("\n\n" + ui.Faint.Render(m.status))
		}
		b.WriteString("\n\n" + m.renderFooter())
		return b.String()
	}

	switch {
	case m.err != nil:
		b.WriteString(ui.Bad.Render("could not read sandboxes: " + m.err.Error()))
	case len(m.sandboxes) == 0 && m.building == nil:
		b.WriteString(ui.Faint.Render("no sandboxes yet: run `plbx run` in a repo to make one"))
	default:
		list := m.renderList()
		b.WriteString(list)
		if detail := m.renderDetail(list); detail != "" {
			b.WriteString("\n\n" + detail)
		}
	}

	if m.status != "" {
		b.WriteString("\n\n" + ui.Faint.Render(m.status))
	}
	b.WriteString("\n\n")
	b.WriteString(m.renderFooter())
	return b.String()
}

// renderList draws one row per sandbox.
func (m *Model) renderList() string {
	rows := make([]string, 0, len(m.sandboxes)+1)
	for i, sb := range m.sandboxes {
		rows = append(rows, m.renderRow(sb, i == m.cursor))
	}
	if row := m.buildingRow(); row != "" {
		rows = append(rows, row)
	}
	return strings.Join(rows, "\n")
}

// buildingRow stands in for a sandbox that is being made.
//
// It has no record to render from until it exists, so without this the form
// closes onto an unchanged list and nothing happens for as long as the work
// takes. On a first run for an agent that is a multi-gigabyte download, which
// reads as the action having quietly failed.
func (m *Model) buildingRow() string {
	if m.building == nil {
		return ""
	}
	for _, sb := range m.sandboxes {
		if sb.Spec.Name == m.building.Name {
			return "" // it exists now; the real row has it
		}
	}
	step := m.buildStep
	if step == "" {
		step = "creating…"
	}
	return "  " + pad(m.building.Name, 20) + " " +
		ui.Warn.Render(pad("building", 9)) + " " +
		ui.Faint.Render(pad(dash(m.building.Agent), 10)) + " " +
		ui.Faint.Render(ui.Elide(step, m.stepWidth()))
}

// stepWidth is what is left of the row for the step to describe itself in.
func (m *Model) stepWidth() int {
	const used = 2 + 20 + 1 + 9 + 1 + 10 + 1
	if m.width <= used {
		return 0
	}
	return m.width - used
}

func (m *Model) renderRow(sb api.Sandbox, selected bool) string {
	marker, name := "  ", sb.Spec.Name
	if selected {
		marker = cursorStyle.Render("▸ ")
		name = selectedStyle.Render(name)
	}

	status := string(sb.State.Status)
	if m.pending == sb.Spec.Name {
		status = "working…"
	}

	line := marker + pad(name, 20) + " " +
		ui.StatusStyle(sb.State.Status).Render(pad(status, 9)) + " " +
		ui.Faint.Render(pad(dash(sb.Spec.Agent), 10))

	if sample, ok := m.stats[sb.Spec.Name]; ok {
		line += " " + gauge(sample.CPUPercent) + " " + ui.Faint.Render(pad(cpuLabel(sample), 7))
		line += " " + gauge(sample.MemoryPercent()) + " " + ui.Faint.Render(memLabel(sample))
	} else if ws := sb.Spec.Primary().Host; ws != "" {
		line += " " + ui.Faint.Render(ws)
	}
	// how long it has been up, when the row has room left. The detail pane
	// always has it; this is the glance version, and a glance that wraps is
	// worse than one that stops short.
	if sb.State.Status == api.StatusRunning && !sb.State.StartedAt.IsZero() {
		up := ui.Faint.Render("  up " + ui.Uptime(sb.State.StartedAt, m.now()))
		if m.width <= 0 || lipgloss.Width(line)+lipgloss.Width(up) <= m.width {
			line += up
		}
	}
	return line
}

// renderDetail draws the selected sandbox's definition below the list: the
// same fields `plbx inspect` prints, minus its heading, since the row above
// already says which sandbox this is.
//
// It reads the selection rather than a sandbox captured when the pane opened,
// so moving the cursor moves the detail with it.
func (m *Model) renderDetail(list string) string {
	sb, ok := m.selected()
	if !ok || !m.showDetail {
		return ""
	}
	return rule(m.ruleWidth(list)) + "\n" + ui.RenderSandboxFields(sb, m.now(), m.width)
}

// ruleWidth spans the list, so the divider frames the rows rather than the
// terminal, and narrows to the terminal when the list is wider than it.
func (m *Model) ruleWidth(list string) int {
	width := 0
	for _, line := range strings.Split(list, "\n") {
		if w := lipgloss.Width(line); w > width {
			width = w
		}
	}
	if m.width > 0 && width > m.width {
		width = m.width
	}
	return width - detailIndent
}

func rule(width int) string {
	if width < 1 {
		return ""
	}
	return strings.Repeat(" ", detailIndent) + ui.Faint.Render(strings.Repeat("─", width))
}

// gauge draws a proportion as a bar. CPU can exceed 100% on a multi-core
// sandbox, so the bar saturates rather than overflowing its width.
func gauge(percent float64) string {
	filled := int(percent / 100 * gaugeWidth)
	if filled > gaugeWidth {
		filled = gaugeWidth
	}
	if filled < 0 {
		filled = 0
	}
	return gaugeFull.Render(strings.Repeat("█", filled)) +
		gaugeEmpty.Render(strings.Repeat("─", gaugeWidth-filled))
}

func cpuLabel(s api.Stats) string {
	return fmt.Sprintf("%.0f%% cpu", s.CPUPercent)
}

func memLabel(s api.Stats) string {
	if s.MemoryLimit <= 0 {
		return api.FormatBytes(s.MemoryBytes)
	}
	return api.FormatBytes(s.MemoryBytes) + " / " + api.FormatBytes(s.MemoryLimit)
}

// renderTabs names the two panels and marks which has the keyboard.
func (m *Model) renderTabs() string {
	sandboxes, network := "sandboxes", "network"
	if m.connFilter {
		if name := m.selectedName(); name != "" {
			network += " · " + name
		}
	}
	if m.panel == networkPanel {
		if denied := m.deniedCount(); denied > 0 {
			// a count on the tab is what makes the panel worth switching to
			// without having to look first.
			network += " (" + strconv.Itoa(denied) + " denied)"
		}
		return ui.Faint.Render(sandboxes) + "   " + selectedStyle.Render(network)
	}
	if denied := m.deniedCount(); denied > 0 {
		network += " (" + strconv.Itoa(denied) + " denied)"
	}
	return selectedStyle.Render(sandboxes) + "   " + ui.Faint.Render(network)
}

// deniedCount is how many of the held decisions were refusals.
func (m *Model) deniedCount() int {
	var n int
	for _, e := range m.visibleConnections() {
		if !e.Allowed {
			n++
		}
	}
	return n
}

// renderConnections draws the network panel: what has been reached for, and
// what was refused.
func (m *Model) renderConnections() string {
	entries := m.visibleConnections()
	if len(entries) == 0 {
		if m.connFilter {
			return ui.Faint.Render(m.selectedName() + " has not tried to reach anything yet")
		}
		return ui.Faint.Render("nothing has tried to reach out yet")
	}

	// show the tail, newest last, so the most recent decision is nearest the
	// footer where the eye already is.
	rows := make([]string, 0, len(entries))
	for i, e := range entries {
		rows = append(rows, m.renderConnectionRow(e, i == m.connCursor))
	}
	if visible := m.connectionRows(); len(rows) > visible {
		rows = rows[len(rows)-visible:]
	}
	return strings.Join(rows, "\n")
}

// connectionRows is how many rows fit above the footer.
func (m *Model) connectionRows() int {
	if m.height <= 0 {
		return 15
	}
	// title, tabs, blanks, status, footer.
	if n := m.height - 8; n > 0 {
		return n
	}
	return 1
}

// reasonIndent is everything to the left of the reason on a connection row:
// the cursor, the verdict, the target and sandbox columns, and their spaces.
const reasonIndent = 52

func (m *Model) renderConnectionRow(e proxy.Entry, selected bool) string {
	marker := "  "
	if selected {
		marker = cursorStyle.Render("▸ ")
	}

	verdict := ui.OK.Render("✓")
	if !e.Allowed {
		verdict = ui.Bad.Render("✗")
	}

	target := e.Target.String()
	if selected {
		target = selectedStyle.Render(target)
	}
	line := marker + verdict + " " + pad(target, 34) + " " + ui.Faint.Render(pad(dash(e.Sandbox), 12))
	// everything to the left of the reason is fixed width, so what is left of
	// the terminal is the reason's budget. It loses its tail rather than being
	// clipped without a mark, so a truncated reason still says it was cut.
	reason := e.Reason
	if m.width > reasonIndent {
		reason = ui.Elide(reason, m.width-reasonIndent)
	}
	return line + " " + ui.Faint.Render(reason)
}

// sandboxKeys is the reminder line for the sandbox panel, naming what the
// highlighted sandbox can actually do.
//
// A fixed bar would offer `s start/stop` against a sandbox that can only be
// one of those, and attach, shell and remove on a fresh install with nothing
// to apply them to, burying `c create` among keys that do nothing.
func (m *Model) sandboxKeys() string {
	sb, ok := m.selected()
	if !ok {
		return "c create · tab network · ? help · q quit"
	}
	lifecycle := "s start"
	if sb.State.Status == api.StatusRunning {
		lifecycle = "s stop"
	}
	return "↑/↓ move · tab network · i details · c create · enter attach · x shell · " +
		lifecycle + " · r remove · ? help · q quit"
}

// renderFooter shows either the full key list or a one-line reminder.
func (m *Model) renderFooter() string {
	if !m.showHelp {
		if m.panel == networkPanel {
			scope := "f only " + m.selectedName()
			if m.connFilter {
				scope = "f all sandboxes"
			}
			if m.selectedName() == "" {
				scope = ""
			} else {
				scope += " · "
			}
			return ui.Faint.Render("↑/↓ move · a allow · d deny · " + scope + "tab sandboxes · ? help · q quit")
		}
		return ui.Faint.Render(m.sandboxKeys())
	}
	lines := make([]string, 0, len(Keys))
	for _, k := range Keys {
		lines = append(lines, "  "+ui.Value.Render(pad(strings.Join(k.Keys, " "), 12))+ui.Faint.Render(k.Help))
	}
	return strings.Join(lines, "\n")
}

// pad right-pads s to width, measured in terminal cells so styled or wide
// characters do not skew the columns.
func pad(s string, width int) string {
	if gap := width - lipgloss.Width(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
