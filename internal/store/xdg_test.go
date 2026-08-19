package store

import (
	"path/filepath"
	"testing"
)

// env builds an Env whose Getenv reads from a map, so resolution is tested
// without touching the real environment.
func env(goos, home string, vars map[string]string) Env {
	return Env{
		GOOS:   goos,
		Home:   home,
		Getenv: func(k string) string { return vars[k] },
	}
}

func TestRootPerPlatformDefaults(t *testing.T) {
	cases := []struct {
		goos string
		want string
	}{
		{goos: "linux", want: filepath.Join("/home/viv", ".local", "share", "planterbox")},
		{goos: "darwin", want: filepath.Join("/home/viv", "Library", "Application Support", "planterbox")},
	}
	for _, tc := range cases {
		t.Run(tc.goos, func(t *testing.T) {
			got, err := Root(env(tc.goos, "/home/viv", nil))
			if err != nil {
				t.Fatalf("Root: %v", err)
			}
			if got != tc.want {
				t.Errorf("Root = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRootHonorsXDGDataHomeOnEveryPlatform(t *testing.T) {
	// anyone who sets XDG_DATA_HOME on macOS means it.
	for _, goos := range []string{"linux", "darwin"} {
		got, err := Root(env(goos, "/home/viv", map[string]string{"XDG_DATA_HOME": "/data"}))
		if err != nil {
			t.Fatalf("%s: Root: %v", goos, err)
		}
		want := filepath.Join("/data", "planterbox")
		if got != want {
			t.Errorf("%s: Root = %q, want %q", goos, got, want)
		}
	}
}

func TestRootIgnoresRelativeXDGDataHome(t *testing.T) {
	// the spec says a relative XDG_DATA_HOME is invalid and must be ignored.
	got, err := Root(env("linux", "/home/viv", map[string]string{"XDG_DATA_HOME": "relative/path"}))
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	want := filepath.Join("/home/viv", ".local", "share", "planterbox")
	if got != want {
		t.Errorf("Root = %q, want the default %q", got, want)
	}
}

func TestRootStateDirOverrideWins(t *testing.T) {
	got, err := Root(env("linux", "/home/viv", map[string]string{
		"PLBX_STATE_DIR": "/explicit",
		"XDG_DATA_HOME":  "/data",
	}))
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if got != "/explicit" {
		t.Errorf("Root = %q, want the explicit override", got)
	}
}

func TestRootWithoutHomeOrOverrideErrors(t *testing.T) {
	if _, err := Root(env("linux", "", nil)); err == nil {
		t.Error("Root with no home and no override should error rather than pick a surprise directory")
	}
}

// A relative state directory resolves against the working directory, so what
// plbx keeps would move with wherever it was run from.
func TestRelativeStateDirIsRefused(t *testing.T) {
	if got, err := Root(env("linux", "/home/viv", map[string]string{"PLBX_STATE_DIR": "mine"})); err == nil {
		t.Errorf("Root = %q, want a relative PLBX_STATE_DIR refused", got)
	}
}
