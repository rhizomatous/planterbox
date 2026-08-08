package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMakeBuildProducesTheDaemonToo guards a gap that is invisible until it
// bites: jard autostarts jardd from beside itself, so a build that produces
// only jard yields a binary that cannot run a sandbox at all, and says so with
// an error about $PATH that points nowhere useful.
func TestMakeBuildProducesTheDaemonToo(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("locating the repository root: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatalf("reading the Makefile: %v", err)
	}
	if !strings.Contains(string(data), "./cmd/jardd") {
		t.Error("`make build` does not build cmd/jardd; jard cannot autostart a daemon that was never built")
	}
}
