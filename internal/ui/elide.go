package ui

import "strings"

// ellipsis is one rune, so it costs one column of the budget it saves.
const ellipsis = "…"

// Elide shortens s to width columns by dropping the end, which is where prose
// carries the least: a sentence still reads from its opening.
func Elide(s string, width int) string {
	r := []rune(s)
	if width <= 0 || len(r) <= width {
		return s
	}
	if width == 1 {
		return ellipsis
	}
	return strings.TrimRight(string(r[:width-1]), " ") + ellipsis
}

// ElidePath shortens s to width columns by dropping the front, because a path is
// identified by its tail. Every workspace on a machine shares the prefix; only
// the last component or two say which one this is.
func ElidePath(s string, width int) string {
	r := []rune(s)
	if width <= 0 || len(r) <= width {
		return s
	}
	if width == 1 {
		return ellipsis
	}
	return ellipsis + string(r[len(r)-(width-1):])
}
