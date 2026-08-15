package main

import "testing"

// TestSentenceCaseLeavesTheRestOfTheWordAlone guards the reason this exists at
// all: fang's own transform title-cases the first word, and Unicode title
// casing treats a hyphen as a word boundary — so a hyphenated sandbox name
// came back as a name nobody has.
func TestSentenceCaseLeavesTheRestOfTheWordAlone(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"foo-bar: sandbox not found", "Foo-bar: sandbox not found"},
		{"a-b-c-d: nope", "A-b-c-d: nope"},
		{"plain: sandbox not found", "Plain: sandbox not found"},
		{"Already capital", "Already capital"},
		{"ünicode leads", "Ünicode leads"},
		{"", ""},
		{"x", "X"},
	} {
		if got := sentenceCase(tc.in); got != tc.want {
			t.Errorf("sentenceCase(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
