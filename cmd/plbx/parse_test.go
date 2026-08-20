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
		{name: "absolute path defaults to read-write", arg: "/home/viv/demo", wantHost: "/home/viv/demo", wantRO: false},
		{name: "read-only suffix", arg: "/home/viv/shared:ro", wantHost: "/home/viv/shared", wantRO: true},
		{name: "explicit read-write suffix", arg: "/home/viv/demo:rw", wantHost: "/home/viv/demo", wantRO: false},
		{name: "relative resolves against base", arg: "../shared", wantHost: "/home/viv/shared", wantRO: false},
		{name: "relative with mode", arg: "../shared:ro", wantHost: "/home/viv/shared", wantRO: true},
		{name: "dot is the base itself", arg: ".", wantHost: "/home/viv/demo", wantRO: false},
		{name: "trailing slash is cleaned", arg: "/home/viv/demo/", wantHost: "/home/viv/demo", wantRO: false},
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
		{arg: "3000", want: api.Port{Host: 3000, Sandbox: 3000}},
		{arg: "8080:80", want: api.Port{Host: 8080, Sandbox: 80}},
		{arg: "80/tcp", want: api.Port{Host: 80, Sandbox: 80}},
		{arg: "65535", want: api.Port{Host: 65535, Sandbox: 65535}},
	}
	for _, tc := range cases {
		t.Run(tc.arg, func(t *testing.T) {
			got, err := parsePort(tc.arg)
			if err != nil {
				t.Fatalf("parsePort(%q): %v", tc.arg, err)
			}
			if got != tc.want {
				t.Errorf("parsePort(%q) = %+v, want %+v", tc.arg, got, tc.want)
			}
		})
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
		{name: "sandbox reference", arg: "demo:/home/agent/a", wantSandbox: "demo", wantPath: "/home/agent/a"},
		{name: "sandbox with relative inner path", arg: "demo:notes.md", wantSandbox: "demo", wantPath: "notes.md"},
		{name: "bare host path", arg: "/tmp/a", wantSandbox: "", wantPath: "/tmp/a"},
		{name: "relative host path", arg: "./a", wantSandbox: "", wantPath: "./a"},
		{name: "host path with a colon", arg: "/tmp/od:d/a", wantSandbox: "", wantPath: "/tmp/od:d/a"},
		{name: "dot-slash escapes an ambiguous name", arg: "./demo:x", wantSandbox: "", wantPath: "./demo:x"},
		{name: "trailing colon is a host path", arg: "demo:", wantSandbox: "", wantPath: "demo:"},
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
