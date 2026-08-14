package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rhizomatous/planterbox/internal/api"
)

// workspace makes a real directory, since building a spec stats its workspaces.
func workspace(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}
	return dir
}

func TestCreateBuildsASpecThroughTheService(t *testing.T) {
	dir := workspace(t, "myrepo")
	fake := api.NewFake()

	if _, err := runCLI(t, fake, "create", dir); err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(fake.Sandboxes) != 1 {
		t.Fatalf("created %d sandboxes, want 1", len(fake.Sandboxes))
	}

	spec := fake.Sandboxes[0].Spec
	if spec.Name != "myrepo" {
		t.Errorf("name = %q, want it derived from the directory", spec.Name)
	}
	if spec.Agent != api.DefaultAgent {
		t.Errorf("agent = %q, want the default %q", spec.Agent, api.DefaultAgent)
	}
	if spec.Image == "" {
		t.Error("image should default to the agent's own")
	}
	if len(spec.Workspaces) != 1 || spec.Workspaces[0].Host != dir {
		t.Errorf("workspaces = %+v, want the given directory", spec.Workspaces)
	}
	if spec.Workspaces[0].ReadOnly {
		t.Error("the primary workspace must be read-write; a read-only default would break every agent")
	}
}

func TestCreateAcceptsAnAgentPositional(t *testing.T) {
	dir := workspace(t, "myrepo")
	fake := api.NewFake()
	if _, err := runCLI(t, fake, "create", "codex", dir); err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := fake.Sandboxes[0].Spec.Agent; got != "codex" {
		t.Errorf("agent = %q, want codex", got)
	}
}

func TestCreateTreatsALeadingPathAsAWorkspace(t *testing.T) {
	// `plbx create .` must not read "." as an agent name.
	dir := workspace(t, "myrepo")
	fake := api.NewFake()
	if _, err := runCLI(t, fake, "create", dir); err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := fake.Sandboxes[0].Spec.Agent; got != api.DefaultAgent {
		t.Errorf("agent = %q, want the default", got)
	}
}

func TestCreateAppliesFlags(t *testing.T) {
	dir := workspace(t, "myrepo")
	other := workspace(t, "shared")
	fake := api.NewFake()

	_, err := runCLI(t, fake, "create", "shell", dir, other+":ro",
		"--name", "custom", "--image", "acme/base:2",
		"--cpus", "4", "-m", "8GiB",
		"-p", "3000", "-p", "5353:53",
		"-e", "FOO=bar", "-e", "EMPTY=")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	spec := fake.Sandboxes[0].Spec
	if spec.Name != "custom" || spec.Image != "acme/base:2" {
		t.Errorf("name/image = %q/%q, want the flag values", spec.Name, spec.Image)
	}
	if spec.Resources.CPUs != 4 || spec.Resources.Memory != 8<<30 {
		t.Errorf("resources = %+v, want 4 cpus and 8GiB", spec.Resources)
	}
	if ports := fake.Sandboxes[0].Ports; len(ports) != 2 || ports[1].Sandbox != 53 {
		t.Errorf("ports = %+v, want both, with the mapping kept", ports)
	}
	if spec.Env["FOO"] != "bar" {
		t.Errorf("env = %v, want FOO=bar", spec.Env)
	}
	if _, ok := spec.Env["EMPTY"]; !ok {
		t.Error("an explicitly empty value should still set the variable")
	}
	if len(spec.Workspaces) != 2 || !spec.Workspaces[1].ReadOnly {
		t.Errorf("workspaces = %+v, want the second mounted read-only", spec.Workspaces)
	}
}

func TestCreateKeepsWorkspacesInArgumentOrder(t *testing.T) {
	// the first workspace is the primary: it becomes the sandbox's working
	// directory and the path `plbx run` reattaches by. Reordering or reversing
	// these would break both, silently.
	first := workspace(t, "primary")
	second := workspace(t, "second")
	third := workspace(t, "third")

	fake := api.NewFake()
	if _, err := runCLI(t, fake, "create", first, second+":ro", third); err != nil {
		t.Fatalf("create: %v", err)
	}

	got := fake.Sandboxes[0].Spec.Workspaces
	want := []api.Workspace{
		{Host: first},
		{Host: second, ReadOnly: true},
		{Host: third},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d workspaces, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("workspace %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if fake.Sandboxes[0].Spec.Primary().Host != first {
		t.Errorf("primary = %q, want the first argument %q",
			fake.Sandboxes[0].Spec.Primary().Host, first)
	}
}

func TestRunReattachesByThePrimaryWorkspaceNotALaterOne(t *testing.T) {
	// only the first path identifies the sandbox. Matching on a secondary
	// workspace would reattach the wrong one whenever two sandboxes share a
	// read-only mount.
	first := workspace(t, "primary")
	second := workspace(t, "shared")

	fake := api.NewFake()
	if _, err := runCLI(t, fake, "create", first, second+":ro"); err != nil {
		t.Fatalf("create: %v", err)
	}

	// running from the primary reattaches.
	fake.Calls = nil
	if _, err := runCLI(t, fake, "run", first); err != nil {
		t.Fatalf("run from primary: %v", err)
	}
	if len(fake.Sandboxes) != 1 {
		t.Fatal("running from the primary workspace should have reattached")
	}

	// running from the secondary is a different sandbox, not the same one.
	if _, err := runCLI(t, fake, "run", second); err != nil {
		t.Fatalf("run from secondary: %v", err)
	}
	if len(fake.Sandboxes) != 2 {
		t.Error("running from a secondary workspace should not reattach the sandbox that merely mounts it")
	}
}

func TestCreateRejectsAMissingWorkspace(t *testing.T) {
	// better here than as an opaque mount failure from the runtime.
	fake := api.NewFake()
	if _, err := runCLI(t, fake, "create", "/definitely/not/here"); err == nil {
		t.Error("a workspace that does not exist should be rejected")
	}
	if len(fake.Sandboxes) != 0 {
		t.Error("nothing should have been created")
	}
}

func TestRunCreatesThenReattaches(t *testing.T) {
	dir := workspace(t, "myrepo")
	fake := api.NewFake()

	if _, err := runCLI(t, fake, "run", dir); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if len(fake.Sandboxes) != 1 {
		t.Fatalf("first run created %d sandboxes, want 1", len(fake.Sandboxes))
	}

	fake.Calls = nil
	if _, err := runCLI(t, fake, "run", dir); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(fake.Sandboxes) != 1 {
		t.Errorf("second run created another sandbox; it should have reattached")
	}
	for _, call := range fake.Calls {
		if call == "Create" {
			t.Error("second run called Create; reattach-by-path did not match")
		}
	}
}

func TestRunReattachesByName(t *testing.T) {
	dir := workspace(t, "myrepo")
	fake := api.NewFake()
	if _, err := runCLI(t, fake, "create", dir, "--name", "named"); err != nil {
		t.Fatalf("create: %v", err)
	}

	// from an unrelated directory, --name is what finds it.
	fake.Calls = nil
	if _, err := runCLI(t, fake, "run", "--name", "named"); err != nil {
		t.Fatalf("run --name: %v", err)
	}
	if len(fake.Sandboxes) != 1 {
		t.Error("run --name should have reattached, not created")
	}
}

func TestRunWithoutDashDashDoesNotPanic(t *testing.T) {
	// ArgsLenAtDash is -1 when there is no --, which must not be used to slice.
	dir := workspace(t, "myrepo")
	if _, err := runCLI(t, api.NewFake(), "run", dir); err != nil {
		t.Fatalf("run: %v", err)
	}
}

func TestRunWarnsWhenCreateFlagsCannotApply(t *testing.T) {
	dir := workspace(t, "myrepo")
	fake := api.NewFake()
	if _, err := runCLI(t, fake, "run", dir); err != nil {
		t.Fatalf("first run: %v", err)
	}

	out, err := runCLI(t, fake, "run", dir, "--cpus", "8")
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !strings.Contains(out, "--cpus") {
		t.Errorf("output %q should warn that --cpus cannot apply to an existing sandbox", out)
	}
}

func TestRunDoesNotWarnAboutNameWhichSelectsRatherThanConfigures(t *testing.T) {
	dir := workspace(t, "myrepo")
	fake := api.NewFake()
	if _, err := runCLI(t, fake, "create", dir, "--name", "named"); err != nil {
		t.Fatalf("create: %v", err)
	}
	out, err := runCLI(t, fake, "run", "--name", "named")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(out, "fixed when") {
		t.Errorf("output %q should not warn about --name", out)
	}
}

func TestSandboxName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"/home/viv/myrepo", "myrepo"},
		{"/home/viv/my repo", "my-repo"},
		{"/home/viv/my.repo", "my.repo"},
		{"/home/viv/.dotfiles", "dotfiles"},
		{"/home/viv/repo!!", "repo-"},
		{"/", "sandbox"},
	}
	for _, tc := range cases {
		if got := api.SandboxName(tc.in); got != tc.want {
			t.Errorf("api.SandboxName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSandboxNameAlwaysProducesAValidName(t *testing.T) {
	for _, in := range []string{"/", "/home/viv/...", "/home/viv/---", "/home/viv/" + strings.Repeat("x", 200)} {
		if got := api.SandboxName(in); !api.ValidName(got) {
			t.Errorf("api.SandboxName(%q) = %q, which is not a valid sandbox name", in, got)
		}
	}
}
