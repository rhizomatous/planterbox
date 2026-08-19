package ui

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// longPath is wider than any width these tests ask for, so every case that
// elides has something to drop.
const longPath = "/private/tmp/very/long/scratchpad/demo"

func TestElideKeepsTheHead(t *testing.T) {
	for _, tc := range []struct {
		name  string
		in    string
		width int
		want  string
	}{
		{name: "fits", in: "short", width: 20, want: "short"},
		{name: "cut", in: "not allowed by any rule", width: 12, want: "not allowed…"},
		{name: "exact", in: "exactly ten", width: 11, want: "exactly ten"},
		{name: "one rune", in: "anything", width: 1, want: "…"},
		{name: "unlimited", in: "anything at all", width: 0, want: "anything at all"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Elide(tc.in, tc.width); got != tc.want {
				t.Errorf("Elide(%q, %d) = %q, want %q", tc.in, tc.width, got, tc.want)
			}
		})
	}
}

func TestElidePathKeepsTheTail(t *testing.T) {
	for _, tc := range []struct {
		name  string
		in    string
		width int
		want  string
	}{
		{name: "fits", in: "/a/b", width: 20, want: "/a/b"},
		{name: "cut", in: longPath, width: 16, want: "…scratchpad/demo"},
		{name: "one rune", in: longPath, width: 1, want: "…"},
		{name: "unlimited", in: longPath, width: 0, want: longPath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ElidePath(tc.in, tc.width); got != tc.want {
				t.Errorf("ElidePath(%q, %d) = %q, want %q", tc.in, tc.width, got, tc.want)
			}
		})
	}
}

// Width is cells, and the layout around these measures the same way: a column
// is sized with lipgloss.Width. Runes disagree with cells in both directions,
// so a budget kept in runes is not a budget.
func TestElidingNeverExceedsTheBudget(t *testing.T) {
	for _, tc := range []struct {
		name  string
		in    string
		width int
	}{
		// 16 runes, 22 cells
		{name: "wide characters", in: "/home/viv/\u30d7\u30ed\u30b8\u30a7\u30af\u30c8", width: 16},
		// a combining mark is a rune that costs no cell
		{name: "zero-width combining marks", in: "/home/viv/jardinie\u0301re", width: 16},
		{name: "a cut inside a joined emoji", in: "team \U0001F468\u200d\U0001F469\u200d\U0001F467 ships", width: 8},
		{name: "a cut inside a flag", in: "from \U0001F1EF\U0001F1F5 today", width: 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ansi.StringWidth(Elide(tc.in, tc.width)); got > tc.width {
				t.Errorf("Elide(%q, %d) is %d cells wide", tc.in, tc.width, got)
			}
			if got := ansi.StringWidth(ElidePath(tc.in, tc.width)); got > tc.width {
				t.Errorf("ElidePath(%q, %d) is %d cells wide", tc.in, tc.width, got)
			}
		})
	}
}

// a multi-byte path must be cut on character boundaries, not bytes, or the
// tail arrives as a broken character.
func TestElideDoesNotCutMidCharacter(t *testing.T) {
	if got := ElidePath("/home/viv/jardinière", 11); got != "…jardinière" {
		t.Errorf("got %q, want %q", got, "…jardinière")
	}
	if got := Elide("jardinière is a word", 11); got != "jardinière…" {
		t.Errorf("got %q, want %q", got, "jardinière…")
	}
}
