package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rhizomatous/planterbox/internal/api"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "sandboxes"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

func sandbox(name, host string) api.Sandbox {
	return api.Sandbox{
		Spec: api.Spec{
			Name:       name,
			Image:      "base:1",
			Workspaces: []api.Workspace{{Host: host}},
			CreatedAt:  time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC),
		},
		State: api.State{Status: api.StatusCreated, ContainerID: "plbx-" + name},
	}
}

func TestOpenCreatesRootAndListsEmpty(t *testing.T) {
	s := testStore(t)

	if _, err := os.Stat(s.Dir()); err != nil {
		t.Fatalf("store dir should exist after Open: %v", err)
	}
	got, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("List = %v, want empty on a fresh store", got)
	}
}

func TestPutGetRoundTrip(t *testing.T) {
	s := testStore(t)
	want := sandbox("demo", "/home/viv/project")

	if err := s.Put(want); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := s.Get("demo")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Spec.Name != want.Spec.Name || got.Spec.Image != want.Spec.Image {
		t.Errorf("spec = %+v, want %+v", got.Spec, want.Spec)
	}
	if !got.Spec.CreatedAt.Equal(want.Spec.CreatedAt) {
		t.Errorf("created_at = %v, want %v", got.Spec.CreatedAt, want.Spec.CreatedAt)
	}
	if got.Spec.Primary().Host != "/home/viv/project" {
		t.Errorf("primary workspace = %q, want the one we stored", got.Spec.Primary().Host)
	}
	if got.State.Status != api.StatusCreated {
		t.Errorf("status = %q, want created", got.State.Status)
	}
}

func TestPutReplacesInPlace(t *testing.T) {
	s := testStore(t)
	sb := sandbox("demo", "/a")
	if err := s.Put(sb); err != nil {
		t.Fatalf("Put: %v", err)
	}
	sb.State.Status = api.StatusRunning
	if err := s.Put(sb); err != nil {
		t.Fatalf("Put again: %v", err)
	}

	all, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("List returned %d records, want 1 — a rewrite must replace, not append", len(all))
	}
	if all[0].State.Status != api.StatusRunning {
		t.Errorf("status = %q, want the rewritten value", all[0].State.Status)
	}
}

func TestListIsSortedByName(t *testing.T) {
	s := testStore(t)
	for _, n := range []string{"zeta", "alpha", "mid"} {
		if err := s.Put(sandbox(n, "/"+n)); err != nil {
			t.Fatalf("Put %s: %v", n, err)
		}
	}

	all, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"alpha", "mid", "zeta"}
	for i, n := range want {
		if all[i].Spec.Name != n {
			t.Fatalf("List order = %v, want %v", all, want)
		}
	}
}

func TestListIgnoresForeignFiles(t *testing.T) {
	s := testStore(t)
	if err := s.Put(sandbox("demo", "/a")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	for _, name := range []string{"notes.txt", ".hidden"} {
		if err := os.WriteFile(filepath.Join(s.Dir(), name), []byte("junk"), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	all, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("List = %v, want only the one record", all)
	}
}

func TestGetMissingIsErrNotFound(t *testing.T) {
	if _, err := testStore(t).Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestDeleteRemovesRecordThenReportsNotFound(t *testing.T) {
	s := testStore(t)
	if err := s.Put(sandbox("demo", "/a")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Delete("demo"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get("demo"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete = %v, want ErrNotFound", err)
	}
	if err := s.Delete("demo"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Delete = %v, want ErrNotFound", err)
	}
}

func TestFindByNameAndByPath(t *testing.T) {
	s := testStore(t)
	if err := s.Put(sandbox("demo", "/home/viv/project")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	byName, err := s.Find(api.ByName("demo"))
	if err != nil {
		t.Fatalf("Find by name: %v", err)
	}
	if byName.Spec.Name != "demo" {
		t.Errorf("Find by name = %q, want demo", byName.Spec.Name)
	}

	// reattach-by-path is what makes `plbx run` in a repo find last time's sandbox.
	byPath, err := s.Find(api.ByPath("/home/viv/project"))
	if err != nil {
		t.Fatalf("Find by path: %v", err)
	}
	if byPath.Spec.Name != "demo" {
		t.Errorf("Find by path = %q, want demo", byPath.Spec.Name)
	}

	if _, err := s.Find(api.ByPath("/somewhere/else")); !errors.Is(err, ErrNotFound) {
		t.Errorf("Find by unknown path = %v, want ErrNotFound", err)
	}
	if _, err := s.Find(api.Ref{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Find with an empty ref = %v, want ErrNotFound", err)
	}
}

func TestPutRejectsUnsafeNames(t *testing.T) {
	s := testStore(t)
	for _, name := range []string{"", "../escape", "has/slash", "has space", ".leading-dot"} {
		if err := s.Put(sandbox(name, "/a")); err == nil {
			t.Errorf("Put(%q) succeeded, want a rejection", name)
		}
	}
}

func TestReadsAndDeletesCannotEscapeTheStore(t *testing.T) {
	// Get and Delete take a name from the caller without validating it, so the
	// containment has to come from path() alone.
	s := testStore(t)
	outside := filepath.Join(filepath.Dir(s.Dir()), "outside.json")
	if err := os.WriteFile(outside, []byte(`{"spec":{"name":"outside"}}`), 0o600); err != nil {
		t.Fatalf("seeding a file outside the store: %v", err)
	}

	for _, name := range []string{"../outside", "../../outside", "/etc/passwd", ".."} {
		if _, err := s.Get(name); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get(%q) = %v, want ErrNotFound rather than a file outside the store", name, err)
		}
		if err := s.Delete(name); !errors.Is(err, ErrNotFound) {
			t.Errorf("Delete(%q) = %v, want ErrNotFound", name, err)
		}
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("the file outside the store was touched: %v", err)
	}
}

func TestPutLeavesNoTempFiles(t *testing.T) {
	// writes go through a temp file and a rename; the temp must not linger.
	s := testStore(t)
	if err := s.Put(sandbox("demo", "/a")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	entries, err := os.ReadDir(s.Dir())
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "demo.json" {
		t.Errorf("store holds %v, want just demo.json", entries)
	}
}

func TestRecordIsNotWorldReadable(t *testing.T) {
	// specs name host paths; they are nobody else's business.
	s := testStore(t)
	if err := s.Put(sandbox("demo", "/home/viv/project")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	fi, err := os.Stat(filepath.Join(s.Dir(), "demo.json"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("record mode = %o, want no group or other access", perm)
	}
}

// Everything plbx keeps on disk lives under the one directory it was given.
// Deriving the rest by walking up from the records directory put the policy
// and the ssh host key in that directory's parent, so two stores under one
// parent shared both, and `--state-dir ~/mine` wrote into the home directory.
func TestEverythingLivesUnderTheStateDirectory(t *testing.T) {
	state := t.TempDir()
	s, err := Open(state)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, tc := range []struct {
		name string
		got  string
	}{
		{name: "records", got: s.Dir()},
		{name: "policy", got: s.PolicyPath()},
		{name: "ssh host key", got: s.SSHHostKeyPath()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rel, err := filepath.Rel(state, tc.got)
			if err != nil || strings.HasPrefix(rel, "..") {
				t.Errorf("%s at %q is outside the state directory %q", tc.name, tc.got, state)
			}
		})
	}
}

// Two stores given different directories share nothing.
func TestTwoStateDirectoriesShareNothing(t *testing.T) {
	base := t.TempDir()
	a, err := Open(filepath.Join(base, "tenant-a"))
	if err != nil {
		t.Fatalf("Open a: %v", err)
	}
	b, err := Open(filepath.Join(base, "tenant-b"))
	if err != nil {
		t.Fatalf("Open b: %v", err)
	}
	if a.PolicyPath() == b.PolicyPath() {
		t.Errorf("both stores share a policy at %q", a.PolicyPath())
	}
	if a.SSHHostKeyPath() == b.SSHHostKeyPath() {
		t.Errorf("both stores share an ssh host key at %q", a.SSHHostKeyPath())
	}
}
