package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// ellipsis marks a string that was cut. One cell wide, so it costs one cell of
// the budget it saves.
const ellipsis = "…"

// Elide shortens s to width terminal cells by dropping the end, which is where
// prose carries the least: a sentence still reads from its opening. A width of
// zero or less means no limit.
//
// Width is cells rather than runes because that is what everything around it
// measures in: a column is sized with lipgloss.Width, which counts the same
// way. Runes disagree with cells in both directions, and the cut is over
// grapheme clusters so it cannot land inside one.
func Elide(s string, width int) string {
	if width <= 0 || ansi.StringWidth(s) <= width {
		return s
	}
	if width <= ansi.StringWidth(ellipsis) {
		return ellipsis // no room for anything but the mark
	}
	// cut a cell short and drop any space the cut left dangling, so the
	// ellipsis follows a word rather than a gap
	body := ansi.Truncate(s, width-ansi.StringWidth(ellipsis), "")
	return strings.TrimRight(body, " ") + ellipsis
}

// ElidePath shortens s to width terminal cells by dropping the front, because a
// path is identified by its tail. Every workspace on a machine shares the
// prefix; only the last component or two say which one this is.
//
// Cells and grapheme clusters, for the reasons on [Elide].
func ElidePath(s string, width int) string {
	if width <= 0 || ansi.StringWidth(s) <= width {
		return s
	}
	if width <= ansi.StringWidth(ellipsis) {
		return ellipsis // no room for anything but the mark
	}
	// TruncateLeft drops cells off the front, so ask it for however many leave
	// the rest plus the ellipsis inside the budget. It rounds up rather than
	// splitting a grapheme, so a boundary landing inside a wide cluster comes
	// back a cell over; drop another and ask again.
	drop := ansi.StringWidth(s) - width + ansi.StringWidth(ellipsis)
	for out := ansi.TruncateLeft(s, drop, ellipsis); ; out = ansi.TruncateLeft(s, drop, ellipsis) {
		if ansi.StringWidth(out) <= width {
			return out
		}
		drop++
	}
}
