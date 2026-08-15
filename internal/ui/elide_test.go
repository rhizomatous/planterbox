package ui

import "testing"

func TestElideKeepsTheHeadAndElidePathKeepsTheTail(t *testing.T) {
	const path = "/private/tmp/very/long/scratchpad/demo"
	for _, tc := range []struct {
		name string
		got  string
		want string
	}{
		{"prose fits", Elide("short", 20), "short"},
		{"prose cut", Elide("not allowed by any rule", 12), "not allowed…"},
		{"prose exact", Elide("exactly ten", 11), "exactly ten"},
		{"prose one column", Elide("anything", 1), "…"},
		{"prose unlimited", Elide("anything at all", 0), "anything at all"},
		{"path fits", ElidePath("/a/b", 20), "/a/b"},
		{"path cut", ElidePath(path, 16), "…scratchpad/demo"},
		{"path one column", ElidePath(path, 1), "…"},
		{"path unlimited", ElidePath(path, 0), path},
	} {
		if tc.got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, tc.got, tc.want)
		}
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
