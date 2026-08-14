package main

import (
	"testing"

	"github.com/rhizomatous/planterbox/internal/api"
)

func TestParseWorkspace(t *testing.T) {
	cases := []struct {
		name     string
		arg      string
		wantHost string
		wantRO   bool
	}{
		{"absolute path defaults to read-write", "/home/viv/demo", "/home/viv/demo", false},
		{"read-only suffix", "/home/viv/shared:ro", "/home/viv/shared", true},
		{"explicit read-write suffix", "/home/viv/demo:rw", "/home/viv/demo", false},
		{"relative resolves against base", "../shared", "/home/viv/shared", false},
		{"relative with mode", "../shared:ro", "/home/viv/shared", true},
		{"dot is the base itself", ".", "/home/viv/demo", false},
		{"trailing slash is cleaned", "/home/viv/demo/", "/home/viv/demo", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseWorkspace(tc.arg, "/home/viv/demo")
			if err != nil {
				t.Fatalf("parseWorkspace(%q): %v", tc.arg, err)
			}
			if got.Host != tc.wantHost {
				t.Errorf("host = %q, want %q", got.Host, tc.wantHost)
			}
			if got.ReadOnly != tc.wantRO {
				t.Errorf("readOnly = %v, want %v", got.ReadOnly, tc.wantRO)
			}
		})
	}
}

func TestParseWorkspaceRejectsGarbage(t *testing.T) {
	for _, arg := range []string{"", ":ro", "/path:rx", "/path:", "/a:b:ro"} {
		if _, err := parseWorkspace(arg, "/base"); err == nil {
			t.Errorf("parseWorkspace(%q) succeeded, want an error", arg)
		}
	}
}

func TestParseWorkspaceRejectsAColonInThePath(t *testing.T) {
	// a mount spec is colon-delimited, so this cannot be represented. Better to
	// refuse than to bind somewhere the user did not ask for.
	if _, err := parseWorkspace("/home/viv/a:b", "/base"); err == nil {
		t.Error("a path containing a colon should be rejected")
	}
}

func TestParsePort(t *testing.T) {
	cases := []struct {
		arg  string
		want api.Port
	}{
		{"3000", api.Port{Host: 3000, Sandbox: 3000}},
		{"8080:80", api.Port{Host: 8080, Sandbox: 80}},
		{"80/tcp", api.Port{Host: 80, Sandbox: 80}},
		{"65535", api.Port{Host: 65535, Sandbox: 65535}},
	}
	for _, tc := range cases {
		got, err := parsePort(tc.arg)
		if err != nil {
			t.Errorf("parsePort(%q): %v", tc.arg, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parsePort(%q) = %+v, want %+v", tc.arg, got, tc.want)
		}
	}
}

func TestParsePortNormalizesTCP(t *testing.T) {
	// tcp is the default; storing it explicitly only makes records noisier.
	got, err := parsePort("80/tcp")
	if err != nil {
		t.Fatalf("parsePort: %v", err)
	}
	if got.Proto != "" {
		t.Errorf("Proto = %q, want it left empty for the default", got.Proto)
	}
}

func TestParsePortRejectsGarbage(t *testing.T) {
	for _, arg := range []string{"", "http", "0", "-1", "70000", "80:", ":80", "80:80/sctp", "80:http"} {
		if _, err := parsePort(arg); err == nil {
			t.Errorf("parsePort(%q) succeeded, want an error", arg)
		}
	}
}

func TestParseCopyPath(t *testing.T) {
	cases := []struct {
		name        string
		arg         string
		wantSandbox string
		wantPath    string
	}{
		{"sandbox reference", "demo:/home/agent/a", "demo", "/home/agent/a"},
		{"sandbox with relative inner path", "demo:notes.md", "demo", "notes.md"},
		{"bare host path", "/tmp/a", "", "/tmp/a"},
		{"relative host path", "./a", "", "./a"},
		{"host path with a colon", "/tmp/od:d/a", "", "/tmp/od:d/a"},
		{"dot-slash escapes an ambiguous name", "./demo:x", "", "./demo:x"},
		{"trailing colon is a host path", "demo:", "", "demo:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseCopyPath(tc.arg)
			if err != nil {
				t.Fatalf("parseCopyPath(%q): %v", tc.arg, err)
			}
			if got.Sandbox != tc.wantSandbox || got.Path != tc.wantPath {
				t.Errorf("parseCopyPath(%q) = %+v, want sandbox %q path %q",
					tc.arg, got, tc.wantSandbox, tc.wantPath)
			}
		})
	}
}

func TestParseCopyPathRejectsEmpty(t *testing.T) {
	if _, err := parseCopyPath(""); err == nil {
		t.Error("an empty path should be rejected")
	}
}
