package store

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/rhizomatous/planterbox/internal/proxy"
)

// TestReadOnlyStoreReadsButNeverWrites is what makes --dry-run honest: it has
// to see the sandboxes that exist, so it can render what it would do to them,
// and leave the disk exactly as it found it.
func TestReadOnlyStoreReadsButNeverWrites(t *testing.T) {
	dir := t.TempDir()
	live, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := live.Put(sandbox("existing", "/home/viv/existing")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	ro, err := OpenReadOnly(dir)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}

	// reads are real, or a dry run cannot describe what it would touch
	if _, err := ro.Get("existing"); err != nil {
		t.Errorf("a read-only store should still read: %v", err)
	}
	if list, err := ro.List(); err != nil || len(list) != 1 {
		t.Errorf("List() = %v, %v, want the one existing record", list, err)
	}

	// writes go nowhere
	if err := ro.Put(sandbox("ghost", "/home/viv/ghost")); err != nil {
		t.Errorf("Put should be a silent no-op: %v", err)
	}
	if err := ro.Delete("existing"); err != nil {
		t.Errorf("Delete should be a silent no-op: %v", err)
	}
	if err := ro.SetPolicy(proxy.Policy{Preset: proxy.PresetOpen}); err != nil {
		t.Errorf("SetPolicy should be a silent no-op: %v", err)
	}

	after, err := live.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(after) != 1 || after[0].Spec.Name != "existing" {
		t.Errorf("the disk changed under a read-only store: %+v", after)
	}
	if _, err := live.Policy(); !errors.Is(err, ErrNoPolicy) {
		t.Errorf("a policy was written by a read-only store: %v", err)
	}
}

// a name that was never there is still not there, so a dry run and a real run
// disagree about the disk and nothing else.
func TestReadOnlyDeleteStillReportsAMissingRecord(t *testing.T) {
	ro, err := OpenReadOnly(t.TempDir())
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	if err := ro.Delete("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete(%q) = %v, want ErrNotFound", "nope", err)
	}
}

// --dry-run must work on a machine that has never run plbx, without leaving
// the state directory behind as its only trace.
func TestReadOnlyOpenCreatesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "never-existed")
	ro, err := OpenReadOnly(dir)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	if list, err := ro.List(); err != nil || len(list) != 0 {
		t.Errorf("List() = %v, %v, want empty", list, err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("OpenReadOnly created %s", dir)
	}
}
