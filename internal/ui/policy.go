package ui

import (
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/rhizomatous/planterbox/internal/proxy"
)

// RenderPolicy is the detail view behind `plbx policy ls`.
//
// The preset's own allowances are summarised rather than listed. They are the
// baseline, not something anyone chose here, and forty of them would bury the
// handful of rules that were actually added.
func RenderPolicy(p proxy.Policy) string {
	label := lipgloss.NewStyle().Faint(true).Width(10)

	lines := []string{
		Title.Render("🪴 network policy"),
		"  " + label.Render("preset") + Value.Render(string(p.Preset)),
		"  " + label.Render("") + Faint.Render(p.Preset.Description()),
	}
	if n := len(p.Preset.Allowances()); n > 0 {
		lines = append(lines, "  "+label.Render("")+Faint.Render(
			"it allows "+strconv.Itoa(n)+" common hosts on its own"))
	}

	if len(p.Rules) == 0 {
		return strings.Join(append(lines, "", "  "+Faint.Render("no rules of your own; the preset decides everything")), "\n")
	}

	// allows and denies read as two lists rather than one interleaved one: a
	// deny beats an allow however they were ordered, and showing them mixed
	// would imply a precedence that does not exist.
	allowed, denied := splitRules(p.Rules)
	lines = append(lines, "")
	if len(allowed) > 0 {
		lines = append(lines, ruleBlock("allowed", allowed, OK)...)
	}
	if len(denied) > 0 {
		if len(allowed) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, ruleBlock("denied", denied, Bad)...)
		lines = append(lines, "", "  "+Faint.Render("a denial wins over any allow that covers the same host"))
	}
	return strings.Join(lines, "\n")
}

// ruleBlock renders one heading and its patterns.
func ruleBlock(heading string, patterns []string, style lipgloss.Style) []string {
	lines := []string{"  " + style.Render(heading) + Faint.Render("  ("+strconv.Itoa(len(patterns))+")")}
	for _, p := range patterns {
		lines = append(lines, "    "+p)
	}
	return lines
}

func splitRules(rules []proxy.Rule) (allowed, denied []string) {
	for _, r := range rules {
		if r.Allow {
			allowed = append(allowed, r.Pattern)
		} else {
			denied = append(denied, r.Pattern)
		}
	}
	return allowed, denied
}

// RenderVerdict is the answer to `plbx policy check`.
func RenderVerdict(t proxy.Target, v proxy.Verdict) string {
	if v.Allowed {
		return OK.Render("allowed ") + Value.Render(t.String()) + "\n  " + Faint.Render(v.Reason)
	}
	return Bad.Render("denied  ") + Value.Render(t.String()) + "\n  " + Faint.Render(v.Reason)
}

// ConnectionColumns are the headers of the `plbx policy log` table.
var ConnectionColumns = []string{"", "TARGET", "SANDBOX", "HITS", "WHEN", "REASON"}

// RenderConnections is the table behind `plbx policy log`.
func RenderConnections(groups []proxy.Group, now time.Time) string {
	if len(groups) == 0 {
		return Faint.Render("nothing has tried to reach out yet")
	}

	rows := make([][]string, 0, len(groups))
	for _, g := range groups {
		hits := ""
		// a lone attempt needs no count, and a column of "1" is noise in
		// front of the ones that are not.
		if g.Hits > 1 {
			hits = strconv.Itoa(g.Hits)
		}
		rows = append(rows, []string{
			mark(g.Allowed),
			g.Target.String(),
			dash(g.Sandbox),
			hits,
			Age(g.Last, now),
			g.Reason,
		})
	}

	widths := columnWidths(ConnectionColumns, rows)
	lines := make([]string, 0, len(rows)+1)
	lines = append(lines, renderRow(ConnectionColumns, widths, headerStyle))
	for i, row := range rows {
		lines = append(lines, renderRow(row, widths, connectionCellStyle(groups[i].Allowed)))
	}
	return strings.Join(lines, "\n")
}

// mark is the one-glyph verdict at the start of a log row, so a screen of
// entries can be scanned without reading any of them.
func mark(allowed bool) string {
	if allowed {
		return "✓"
	}
	return "✗"
}

// connectionCellStyle colors the mark by verdict and dims the reason, so the
// eye lands on what was refused.
func connectionCellStyle(allowed bool) func(int, string) string {
	return func(col int, cell string) string {
		switch col {
		case 0:
			if allowed {
				return OK.Render(cell)
			}
			return Bad.Render(cell)
		case 1:
			return Value.Render(cell)
		case 4:
			return Faint.Render(cell)
		default:
			return cell
		}
	}
}
