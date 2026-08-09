package ui

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/rhizomatous/jardiniere/internal/api"
)

// SandboxColumns are the headers of the `jard ls` table, in order.
var SandboxColumns = []string{"NAME", "STATUS", "AGENT", "IMAGE", "WORKSPACE", "CREATED"}

// RenderSandboxes returns the `jard ls` table. An empty list renders a single
// hint line rather than a bare header, which reads better on a fresh install.
func RenderSandboxes(sandboxes []api.Sandbox, now time.Time) string {
	if len(sandboxes) == 0 {
		return Faint.Render("no sandboxes yet")
	}

	rows := make([][]string, 0, len(sandboxes))
	for _, sb := range sandboxes {
		rows = append(rows, []string{
			sb.Spec.Name,
			string(sb.State.Status),
			dash(sb.Spec.Agent),
			dash(sb.Spec.Image),
			dash(sb.Spec.Primary().Host),
			Age(sb.Spec.CreatedAt, now),
		})
	}

	widths := columnWidths(SandboxColumns, rows)
	lines := make([]string, 0, len(rows)+1)
	lines = append(lines, renderRow(SandboxColumns, widths, headerStyle))
	for _, row := range rows {
		lines = append(lines, renderRow(row, widths, cellStyle))
	}
	return strings.Join(lines, "\n")
}

// headerStyle renders every header cell the same way.
func headerStyle(_ int, cell string) string { return Header.Render(cell) }

// cellStyle picks the style for one cell by column, so status reads as a
// posture rather than a word.
func cellStyle(col int, cell string) string {
	switch col {
	case 0:
		return Value.Render(cell)
	case 1:
		return StatusStyle(api.Status(cell)).Render(cell)
	default:
		return cell
	}
}

// RenderCreated is the line printed when a sandbox is built.
func RenderCreated(sb api.Sandbox) string {
	return OK.Render("created ") + Value.Render(sb.Spec.Name) +
		Faint.Render("  "+dash(sb.Spec.Image))
}

// RenderAttaching is the line printed before the terminal is handed to the
// agent. It says whether the sandbox is new, because that is the difference
// between a fresh environment and one carrying everything installed last time.
func RenderAttaching(sb api.Sandbox, created bool) string {
	what := "reattaching to"
	if created {
		what = "starting"
	}
	line := Faint.Render("🪴 "+what+" ") + Value.Render(sb.Spec.Name)
	if ws := sb.Spec.Primary().Host; ws != "" {
		line += Faint.Render("  " + ws)
	}
	return line
}

// RenderSandbox is the detail view behind `jard inspect`.
func RenderSandbox(sb api.Sandbox, now time.Time) string {
	return Title.Render("🪴 "+sb.Spec.Name) + "\n" + RenderSandboxFields(sb, now)
}

// RenderSandboxFields is a sandbox's definition as label/value lines, with no
// heading, for a caller that has already named the sandbox — the dashboard's
// detail pane sits under a row that says which one is selected.
func RenderSandboxFields(sb api.Sandbox, now time.Time) string {
	label := lipgloss.NewStyle().Faint(true).Width(12)
	row := func(k, v string) string { return "  " + label.Render(k) + v }

	lines := []string{
		"  " + label.Render("status") + StatusStyle(sb.State.Status).Render(string(sb.State.Status)),
		row("agent", dash(sb.Spec.Agent)),
		row("image", dash(sb.Spec.Image)),
		row("created", Age(sb.Spec.CreatedAt, now)),
	}
	if len(sb.Spec.Workspaces) > 0 {
		for i, ws := range sb.Spec.Workspaces {
			key := "workspaces"
			if i > 0 {
				key = ""
			}
			mode := ""
			if ws.ReadOnly {
				mode = Faint.Render("  read-only")
			}
			lines = append(lines, row(key, ws.Host+mode))
		}
	}
	if r := sb.Spec.Resources; r.CPUs > 0 || r.Memory > 0 {
		lines = append(lines, row("limits", fmt.Sprintf("%s cpu, %s memory",
			cpuLabel(r.CPUs), api.FormatBytes(r.Memory))))
	}
	for i, p := range sb.Ports {
		key := "ports"
		if i > 0 {
			key = ""
		}
		lines = append(lines, row(key, fmt.Sprintf("%d → %d%s", p.Host, p.Sandbox, protoLabel(p.Proto))))
	}
	for i, k := range slices.Sorted(maps.Keys(sb.Spec.Env)) {
		key := "env"
		if i > 0 {
			key = ""
		}
		lines = append(lines, row(key, k+"="+sb.Spec.Env[k]))
	}
	return strings.Join(lines, "\n")
}

// RenderPorts lists what a sandbox publishes on the host.
//
// A stopped sandbox's ports are recorded but not bound, and saying so is the
// difference between "nothing is listening" and "jard forgot".
func RenderPorts(sb api.Sandbox) string {
	if len(sb.Ports) == 0 {
		return Faint.Render(sb.Spec.Name + " publishes no ports")
	}

	lines := make([]string, 0, len(sb.Ports)+1)
	for _, p := range sb.Ports {
		lines = append(lines, "  "+Value.Render(fmt.Sprintf("%d", p.Host))+
			Faint.Render(" → ")+fmt.Sprintf("%d%s", p.Sandbox, protoLabel(p.Proto)))
	}
	if sb.State.Status != api.StatusRunning {
		lines = append(lines, Faint.Render(fmt.Sprintf(
			"  published when %s starts", sb.Spec.Name)))
	}
	return strings.Join(lines, "\n")
}

func cpuLabel(cpus float64) string {
	if cpus <= 0 {
		return "unlimited"
	}
	return strconv.FormatFloat(cpus, 'g', -1, 64)
}

func protoLabel(proto string) string {
	if proto == "" || proto == "tcp" {
		return ""
	}
	return "/" + proto
}

// RenderAgents lists the supported agents, marking the default.
func RenderAgents(agents []api.Agent, def string) string {
	lines := make([]string, 0, len(agents))
	for _, a := range agents {
		name := Value.Render(a.Name)
		if a.Name == def {
			name += OK.Render(" (default)")
		}
		lines = append(lines, "  "+name+"\n    "+Faint.Render(a.Image))
	}
	return strings.Join(lines, "\n")
}

// StatusStyle colors a status by how much attention it wants.
func StatusStyle(s api.Status) lipgloss.Style {
	switch s {
	case api.StatusRunning:
		return OK
	case api.StatusStopped, api.StatusCreated:
		return Faint
	case api.StatusMissing:
		return Bad
	default:
		return Warn
	}
}

// Age renders how long ago t was, in the coarsest unit that still says
// something. A zero time renders as "-".
func Age(t, now time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute")
	case d < 24*time.Hour:
		return plural(int(d.Hours()), "hour")
	default:
		return plural(int(d.Hours()/24), "day")
	}
}

func plural(n int, unit string) string {
	s := strconv.Itoa(n) + " " + unit
	if n != 1 {
		s += "s"
	}
	return s + " ago"
}

// columnWidths measures each column against its header and every cell.
func columnWidths(headers []string, rows [][]string) []int {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = lipgloss.Width(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if w := lipgloss.Width(cell); w > widths[i] {
				widths[i] = w
			}
		}
	}
	return widths
}

// renderRow pads each cell to its column width and styles it. The last column
// is left unpadded so there's no trailing whitespace.
func renderRow(cells []string, widths []int, style func(col int, cell string) string) string {
	var b strings.Builder
	for i, cell := range cells {
		b.WriteString(style(i, cell))
		if i == len(cells)-1 {
			break
		}
		b.WriteString(strings.Repeat(" ", widths[i]-lipgloss.Width(cell)+2))
	}
	return b.String()
}

// dash renders an empty value as a placeholder, so columns never look skipped.
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
