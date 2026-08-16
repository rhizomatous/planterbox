package ui

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/rhizomatous/planterbox/internal/api"
)

// SandboxColumns are the headers of the `plbx ls` table, in order.
//
// The image is not among them. It is long, it is the same for every sandbox of
// a given agent, and it is one `plbx inspect` away — while its width was
// enough to wrap the header itself on an ordinary terminal. Ports take its
// place: they are the one part of a sandbox that changes after it is built.
//
// The workspace goes last because it is the only unbounded field, so it is the
// only one whose overflow costs nothing behind it.
var SandboxColumns = []string{"NAME", "STATUS", "AGENT", "PORTS", "CREATED", "WORKSPACE"}

// maxWorkspaceWidth keeps a listing inside a normal terminal. Paths under a
// home directory come in well under it; the ones that do not are elided from
// the front, which is the half they share with every other path.
const maxWorkspaceWidth = 44

// labelIndent is the two-space margin plus the label column that every value
// in a field listing sits to the right of.
const labelIndent = 14

// RenderSandboxes returns the `plbx ls` table. An empty list renders a single
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
			dash(portsInline(sb.Ports)),
			Age(sb.Spec.CreatedAt, now),
			dash(ElidePath(sb.Spec.Primary().Host, maxWorkspaceWidth)),
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

// RenderCreating is the definition a create is about to build, printed before
// it starts. Ports are absent on purpose: they are the one thing here that can
// still be changed afterwards, so they are not part of what this is warning
// about.
func RenderCreating(spec api.Spec) string {
	return Title.Render("🪴 creating "+spec.Name) + "\n" + RenderSpecFields(spec, 0)
}

// RenderCreated is the line printed when a sandbox is built.
func RenderCreated(sb api.Sandbox) string {
	return OK.Render("created ") + Value.Render(sb.Spec.Name) +
		Faint.Render("  "+dash(sb.Spec.Image)) + "\n" +
		Faint.Render("  attach with  ") + Value.Render("plbx run --name "+sb.Spec.Name)
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

// RenderSandbox is the detail view behind `plbx inspect`.
func RenderSandbox(sb api.Sandbox, now time.Time) string {
	return Title.Render("🪴 "+sb.Spec.Name) + "\n" + RenderSandboxFields(sb, now, 0)
}

// RenderSandboxFields is a sandbox's definition as label/value lines, with no
// heading, for a caller that has already named the sandbox — the dashboard's
// detail pane sits under a row that says which one is selected.
// width is the room the whole line has, or 0 for no limit. Only the workspace
// paths can outgrow it, and they are elided from the front so the component
// that names the workspace survives.
func RenderSandboxFields(sb api.Sandbox, now time.Time, width int) string {
	status := string(sb.State.Status)
	// how long it has been up is the part of "running" that says whether it
	// just came back or has been there all week.
	if sb.State.Status == api.StatusRunning && !sb.State.StartedAt.IsZero() {
		status += Faint.Render("  up " + Uptime(sb.State.StartedAt, now))
	}

	lines := []string{
		fieldRow("status", StatusStyle(sb.State.Status).Render(status)),
		fieldRow("created", Age(sb.Spec.CreatedAt, now)),
	}
	lines = append(lines, specLines(sb.Spec, width)...)
	lines = append(lines, portLines(sb.Ports)...)
	return strings.Join(lines, "\n")
}

// RenderSpecFields is the part of a sandbox that a create fixes for good, for
// echoing back before the container is built. Everything here costs a rebuild
// to change, and a rebuild costs whatever the old sandbox held outside its
// home volume — so this is the last cheap moment to notice a wrong value.
func RenderSpecFields(spec api.Spec, width int) string {
	return strings.Join(specLines(spec, width), "\n")
}

// specLines renders the frozen half of a definition, shared so that what a
// create echoes and what an inspect reports cannot drift apart.
func specLines(spec api.Spec, width int) []string {
	valueWidth := 0
	if width > labelIndent {
		valueWidth = width - labelIndent
	}

	lines := []string{
		fieldRow("agent", dash(spec.Agent)),
		fieldRow("image", dash(spec.Image)),
	}
	for i, ws := range spec.Workspaces {
		key := "workspaces"
		if i > 0 {
			key = ""
		}
		mode := Faint.Render("  read-write")
		if ws.ReadOnly {
			mode = Faint.Render("  read-only")
		}
		lines = append(lines, fieldRow(key, ElidePath(ws.Host, valueWidth)+mode))
	}
	if spec.Clone {
		lines = append(lines, fieldRow("mode", "clone"+Faint.Render(
			"  your repository is read-only; the agent works in "+spec.CloneDir())))
	}
	// reported even when unset, because "unlimited" is a decision this
	// sandbox is now stuck with rather than an absence of one.
	lines = append(lines, fieldRow("limits", fmt.Sprintf("%s cpu, %s memory",
		cpuLabel(spec.Resources.CPUs), memoryLabel(spec.Resources.Memory))))
	for i, k := range slices.Sorted(maps.Keys(spec.Env)) {
		key := "env"
		if i > 0 {
			key = ""
		}
		// names only. A sandbox holds live credentials — `docs/concessions.md`
		// says so plainly — and printing their values puts them in every
		// screenshot and paste of an inspect.
		lines = append(lines, fieldRow(key, k+Faint.Render("  set")))
	}
	return lines
}

// portLines reports what a sandbox publishes, saying so even when it publishes
// nothing: ports are the one part of a sandbox that can still change, so their
// absence is worth stating rather than leaving to be inferred.
func portLines(ports []api.Port) []string {
	if len(ports) == 0 {
		return []string{fieldRow("ports", Faint.Render("none"))}
	}
	lines := make([]string, 0, len(ports))
	for i, p := range ports {
		key := "ports"
		if i > 0 {
			key = ""
		}
		lines = append(lines, fieldRow(key, fmt.Sprintf("%d → %d%s", p.Host, p.Sandbox, protoLabel(p.Proto))))
	}
	return lines
}

// fieldRow is one label/value line of a definition listing.
func fieldRow(k, v string) string {
	return "  " + lipgloss.NewStyle().Faint(true).Width(12).Render(k) + v
}

// memoryLabel names a byte limit, or the absence of one.
func memoryLabel(bytes int64) string {
	if bytes <= 0 {
		return "unlimited"
	}
	return api.FormatBytes(bytes)
}

// RenderPorts lists what a sandbox publishes on the host.
//
// A stopped sandbox's ports are recorded but not bound, and saying so is the
// difference between "nothing is listening" and "plbx forgot".
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

// portsInline is the listing's one-cell form of what a sandbox publishes.
// A port mapped to itself is written once, because "3000" and "3000 → 3000"
// say the same thing and only one of them fits in a table.
func portsInline(ports []api.Port) string {
	if len(ports) == 0 {
		return ""
	}
	parts := make([]string, 0, len(ports))
	for _, p := range ports {
		if p.Host == p.Sandbox {
			parts = append(parts, fmt.Sprintf("%d%s", p.Host, protoLabel(p.Proto)))
			continue
		}
		parts = append(parts, fmt.Sprintf("%d→%d%s", p.Host, p.Sandbox, protoLabel(p.Proto)))
	}
	return strings.Join(parts, ",")
}

func protoLabel(proto string) string {
	if proto == "" || proto == "tcp" {
		return ""
	}
	return "/" + proto
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

// Uptime is how long something has been going, as a span rather than a point:
// "up 30m" says what "up 30 minutes ago" only implies, and takes less room
// doing it.
func Uptime(since, now time.Time) string {
	if since.IsZero() {
		return "-"
	}
	d := now.Sub(since)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%dh", int(d.Hours()/24), int(d.Hours())%24)
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
