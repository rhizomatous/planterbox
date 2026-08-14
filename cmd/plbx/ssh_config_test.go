package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSSHConfigBlockShape(t *testing.T) {
	block := sshConfigBlock("/usr/local/bin/plbx", "/home/viv/.ssh/plbx_known_hosts")

	for _, want := range []string{
		"Host *.plbx",
		// %n, not %h: %h is what a HostName line would have replaced.
		"ProxyCommand /usr/local/bin/plbx ssh-proxy %n",
		"UserKnownHostsFile /home/viv/.ssh/plbx_known_hosts",
		"StrictHostKeyChecking accept-new",
		"ForwardAgent no",
		// the gateway checks no key, so ssh should present none.
		"IdentityAgent none",
		"IdentityFile none",
		"IdentitiesOnly yes",
		"ControlMaster no",
		"ControlPath none",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("block is missing %q:\n%s", want, block)
		}
	}
	// /dev/null is the other way to offer no identity, and ssh warns about
	// failing to parse it on every single connection.
	if strings.Contains(block, "/dev/null") {
		t.Error("the block points an IdentityFile at /dev/null, which ssh complains about")
	}
	// turning the host key check off would weaken something real to save one
	// prompt, and the gateway's key is stable precisely so it need not.
	if strings.Contains(block, "StrictHostKeyChecking no") {
		t.Error("the block disables host key checking")
	}
}

// A path with a space in it would otherwise be read as two arguments.
func TestSSHConfigBlockQuotesAPathWithSpaces(t *testing.T) {
	block := sshConfigBlock("/Applications/My Tools/plbx", "/home/viv/.ssh/known")
	if !strings.Contains(block, `ProxyCommand "/Applications/My Tools/plbx" ssh-proxy %n`) {
		t.Errorf("block does not quote the binary path:\n%s", block)
	}
}

func TestWriteManagedBlockLeavesEverythingElseAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".ssh", "config")
	before := "Host git\n    HostName github.com\n    User git\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	block := sshConfigBlock("/usr/local/bin/plbx", "/known")
	changed, err := writeManagedBlock(path, block)
	if err != nil {
		t.Fatalf("writeManagedBlock: %v", err)
	}
	if !changed {
		t.Error("changed = false on a config with no block in it")
	}

	got := read(t, path)
	if !strings.HasPrefix(got, before) {
		t.Errorf("the user's own config was disturbed:\n%s", got)
	}
	if !strings.Contains(got, "Host *.plbx") {
		t.Errorf("the block was not written:\n%s", got)
	}
}

// Running setup twice must not stack up blocks, and must report the second run
// as a no-op.
func TestWriteManagedBlockIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	block := sshConfigBlock("/usr/local/bin/plbx", "/known")

	if _, err := writeManagedBlock(path, block); err != nil {
		t.Fatalf("first write: %v", err)
	}
	first := read(t, path)

	changed, err := writeManagedBlock(path, block)
	if err != nil {
		t.Fatalf("second write: %v", err)
	}
	if changed {
		t.Error("changed = true when nothing needed changing")
	}
	if got := read(t, path); got != first {
		t.Errorf("the second write altered the file:\n%s", got)
	}
	if n := strings.Count(read(t, path), "Host *.plbx"); n != 1 {
		t.Errorf("found %d blocks, want 1", n)
	}
}

// An upgrade rewrites the block in place, keeping what surrounds it.
func TestWriteManagedBlockReplacesInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if _, err := writeManagedBlock(path, sshConfigBlock("/old/plbx", "/known")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := os.WriteFile(path, []byte(read(t, path)+"\nHost later\n    User viv\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := writeManagedBlock(path, sshConfigBlock("/new/plbx", "/known")); err != nil {
		t.Fatalf("second write: %v", err)
	}
	got := read(t, path)
	if strings.Contains(got, "/old/plbx") {
		t.Errorf("the old block survived:\n%s", got)
	}
	if !strings.Contains(got, "/new/plbx") {
		t.Errorf("the new block is missing:\n%s", got)
	}
	if !strings.Contains(got, "Host later") {
		t.Errorf("what came after the block was eaten:\n%s", got)
	}
}

// A file edited into a shape we did not write is left alone: rewriting from a
// start marker with no end would swallow everything after it.
func TestWriteManagedBlockWillNotGuessAtAHalfMarkedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	half := blockStart + "\nHost *.plbx\n    User agent\n\nHost mine\n    User viv\n"
	if err := os.WriteFile(path, []byte(half), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := writeManagedBlock(path, sshConfigBlock("/usr/local/bin/plbx", "/known")); err != nil {
		t.Fatalf("writeManagedBlock: %v", err)
	}
	got := read(t, path)
	if !strings.Contains(got, "Host mine") {
		t.Errorf("the user's own host was eaten:\n%s", got)
	}
}

func TestWriteManagedBlockCreatesTheConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".ssh", "config")
	if _, err := writeManagedBlock(path, sshConfigBlock("/usr/local/bin/plbx", "/known")); err != nil {
		t.Fatalf("writeManagedBlock: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("config mode = %04o, want 0600", perm)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	return string(data)
}
