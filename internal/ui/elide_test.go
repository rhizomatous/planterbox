package ui

import "testing"

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

// a multi-byte path must be cut on runes, not bytes, or the tail arrives
// as a broken character.
func TestElideCountsRunes(t *testing.T) {
	if got := ElidePath("/home/viv/jardinière", 11); got != "…jardinière" {
		t.Errorf("got %q, want %q", got, "…jardinière")
	}
	if got := Elide("jardinière is a word", 11); got != "jardinière…" {
		t.Errorf("got %q, want %q", got, "jardinière…")
	}
}
