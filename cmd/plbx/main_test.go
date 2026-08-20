package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/rhizomatous/planterbox/internal/api"
)

// TestSentenceCaseLeavesTheRestOfTheWordAlone guards the reason this exists at
// all: fang's own transform title-cases the first word, and Unicode title
// casing treats a hyphen as a word boundary, so a hyphenated sandbox name
// comes back as a name nobody has.
func TestSentenceCaseLeavesTheRestOfTheWordAlone(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{in: "foo-bar: sandbox not found", want: "Foo-bar: sandbox not found"},
		{in: "a-b-c-d: nope", want: "A-b-c-d: nope"},
		{in: "plain: sandbox not found", want: "Plain: sandbox not found"},
		{in: "Already capital", want: "Already capital"},
		{in: "ünicode leads", want: "Ünicode leads"},
		{in: "", want: ""},
		{in: "x", want: "X"},
	} {
		if got := sentenceCase(tc.in); got != tc.want {
			t.Errorf("sentenceCase(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// initialisms read wrong once fang has title-cased them into the leading
// position, whether or not they were written upper-case: "ssh" renders as
// "Ssh" and "CPU" as "Cpu". Neither is a word.
var initialisms = map[string]bool{
	"ansi": true, "api": true, "ca": true, "cli": true, "cpu": true,
	"cpus": true, "dns": true, "gid": true, "grpc": true, "http": true,
	"https": true, "id": true, "io": true, "ip": true, "json": true,
	"oci": true, "os": true, "pid": true, "rpc": true, "rpm": true,
	"ssh": true, "ssl": true, "tls": true, "tty": true, "tui": true,
	"ui": true, "uid": true, "uri": true, "url": true, "xdg": true,
}

// TestDescriptionsSurviveFangsTitleCasing checks every string fang renders
// through its FlagDescription style: command Short lines and flag usage.
// That style title-cases the first word and fang exposes no way to unset it
// for help output, so the strings have to be written around it: an initialism
// in the leading position comes back lower-cased after its first letter.
//
// Long descriptions are deliberately not checked. Those go through
// styles.Text, which has no transform, and should be written as they read.
func TestDescriptionsSurviveFangsTitleCasing(t *testing.T) {
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		// cobra writes these two itself, and their wording is not ours.
		if c.Name() == "completion" || c.Name() == "help" {
			return
		}
		checkLeadingWord(t, c.CommandPath(), c.Short)
		c.LocalFlags().VisitAll(func(f *pflag.Flag) {
			checkLeadingWord(t, c.CommandPath()+" --"+f.Name, f.Usage)
		})
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(newRootCmd())
}

// checkLeadingWord reports a description whose first word fang would mangle.
func checkLeadingWord(t *testing.T, where, desc string) {
	t.Helper()
	fields := strings.Fields(desc)
	if len(fields) == 0 {
		return
	}
	first := fields[0]
	bare := strings.ToLower(strings.Trim(first, ",.:;()"))
	switch {
	case first != strings.ToLower(first):
		t.Errorf("%s: %q opens on %q; fang title-cases the first word, which lower-cases the rest of it",
			where, desc, first)
	case initialisms[bare]:
		t.Errorf("%s: %q opens on the initialism %q, which fang renders as a word; lead with something else",
			where, desc, first)
	}
}

// TestRenderErrorPrintsFailuresAndNotExitStatuses guards the difference between
// plbx failing and the thing plbx ran failing. `plbx exec box sh -c 'exit 42'`
// is plbx working exactly as asked; wrapping the status in an ERROR block
// claims a failure that did not happen, and makes the passthrough useless in a
// script that reads stderr.
func TestRenderErrorPrintsFailuresAndNotExitStatuses(t *testing.T) {
	var out bytes.Buffer

	renderError(&out, fang.Styles{}, exitCodeError{what: "the command", code: 42})
	if out.Len() != 0 {
		t.Errorf("an exit status should print nothing, got %q", out.String())
	}

	out.Reset()
	renderError(&out, fang.Styles{}, errors.New("the runtime is not reachable"))
	if !strings.Contains(out.String(), "runtime is not reachable") {
		t.Errorf("a real failure should still be reported, got %q", out.String())
	}

	out.Reset()
	renderError(&out, fang.Styles{}, fmt.Errorf("%w: %q", api.ErrNotFound, "nope"))
	if !strings.Contains(out.String(), "plbx ls") {
		t.Errorf("a not-found should still point at the listing, got %q", out.String())
	}
}
