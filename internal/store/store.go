// Package store keeps sandbox specs and last-known state on disk, one JSON
// record per sandbox, under an XDG-respecting root.
//
// The spec is authoritative here; state is a cache the runtime can always
// correct. Writes are atomic, so a killed process leaves no half-written record.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rhizomatous/planterbox/internal/api"
)

// ErrNotFound means no record exists under that name.
var ErrNotFound = errors.New("no such sandbox record")

// recordExt is the extension every sandbox record carries.
const recordExt = ".json"

// Store is a directory of sandbox records.
type Store struct {
	dir string
}

// Open returns a store rooted at dir, creating it if absent.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating store %s: %w", dir, err)
	}
	return &Store{dir: dir}, nil
}

// Dir reports where the store keeps its records.
func (s *Store) Dir() string { return s.dir }

// Put writes a sandbox record, replacing any record under the same name.
func (s *Store) Put(sb api.Sandbox) error {
	if !api.ValidName(sb.Spec.Name) {
		return fmt.Errorf("invalid sandbox name %q", sb.Spec.Name)
	}
	data, err := json.MarshalIndent(sb, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding sandbox %q: %w", sb.Spec.Name, err)
	}
	return writeAtomic(s.path(sb.Spec.Name), append(data, '\n'))
}

// Get reads one sandbox record by name.
func (s *Store) Get(name string) (api.Sandbox, error) {
	data, err := os.ReadFile(s.path(name))
	if err != nil {
		if os.IsNotExist(err) {
			return api.Sandbox{}, fmt.Errorf("%q: %w", name, ErrNotFound)
		}
		return api.Sandbox{}, err
	}
	var sb api.Sandbox
	if err := json.Unmarshal(data, &sb); err != nil {
		return api.Sandbox{}, fmt.Errorf("decoding sandbox %q: %w", name, err)
	}
	return sb, nil
}

// List returns every record, sorted by name. A store that has never held a
// sandbox lists empty rather than erroring.
func (s *Store) List() ([]api.Sandbox, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), recordExt) {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), recordExt))
	}
	sort.Strings(names)

	sandboxes := make([]api.Sandbox, 0, len(names))
	for _, name := range names {
		sb, err := s.Get(name)
		if err != nil {
			return nil, err
		}
		sandboxes = append(sandboxes, sb)
	}
	return sandboxes, nil
}

// Delete removes a sandbox record.
func (s *Store) Delete(name string) error {
	err := os.Remove(s.path(name))
	if os.IsNotExist(err) {
		return fmt.Errorf("%q: %w", name, ErrNotFound)
	}
	return err
}

// Find locates a record by ref: by name when one is given, otherwise by primary
// workspace path.
func (s *Store) Find(ref api.Ref) (api.Sandbox, error) {
	if ref.Name != "" {
		return s.Get(ref.Name)
	}
	if ref.Path == "" {
		return api.Sandbox{}, fmt.Errorf("%v: %w", ref, ErrNotFound)
	}
	all, err := s.List()
	if err != nil {
		return api.Sandbox{}, err
	}
	for _, sb := range all {
		if sb.Spec.Primary().Host == ref.Path {
			return sb, nil
		}
	}
	return api.Sandbox{}, fmt.Errorf("%v: %w", ref, ErrNotFound)
}

// path is where a named record lives. Reads and deletes take a name from the
// caller unvalidated, so Base strips any directory part: a traversal attempt
// lands in the store's own directory and misses.
func (s *Store) path(name string) string {
	return filepath.Join(s.dir, filepath.Base(name)+recordExt)
}

// writeAtomic writes data to path via a temp file and a rename, so readers
// never see a partial record.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
