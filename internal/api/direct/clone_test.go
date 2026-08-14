package direct

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/rhizomatous/planterbox/internal/api"
)

// gitRepo makes a real repository to operate on. Clone mode writes to the
// user's own repository, so the tests that matter here use a real one.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"config", "user.email", "a@b"}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v: %s", err, out)
		}
	}
	return dir
}

func remotes(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "remote").Output()
	if err != nil {
		t.Fatalf("git remote: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func cloneSandbox(name, repo string) api.Sandbox {
	return api.Sandbox{Spec: api.Spec{
		Name:       name,
		Clone:      true,
		Workspaces: []api.Workspace{{Host: repo}},
	}}
}

func TestHostRemoteAddedAndDropped(t *testing.T) {
	repo := gitRepo(t)
	svc, _ := testService(t)
	sb := cloneSandbox("demo", repo)
	ctx := context.Background()

	if err := svc.addHostRemote(ctx, sb); err != nil {
		t.Fatalf("addHostRemote: %v", err)
	}
	if got := remotes(t, repo); got != "plbx-demo" {
		t.Fatalf("remotes = %q, want plbx-demo", got)
	}

	svc.dropHostRemote(ctx, sb)
	if got := remotes(t, repo); got != "" {
		t.Errorf("remotes = %q, want none left after the sandbox went", got)
	}
}

// Adding twice must not fail: a sandbox recreated under the same name should
// end up pointing at the new one.
func TestHostRemoteIsRewrittenNotDuplicated(t *testing.T) {
	repo := gitRepo(t)
	svc, _ := testService(t)
	sb := cloneSandbox("demo", repo)
	ctx := context.Background()

	for range 2 {
		if err := svc.addHostRemote(ctx, sb); err != nil {
			t.Fatalf("addHostRemote: %v", err)
		}
	}
	if got := remotes(t, repo); got != "plbx-demo" {
		t.Errorf("remotes = %q, want exactly one", got)
	}
}

// A sandbox that is not in clone mode has no clone to fetch from, so it should
// not appear in anyone's repository.
func TestNoRemoteWithoutCloneMode(t *testing.T) {
	repo := gitRepo(t)
	svc, _ := testService(t)
	sb := cloneSandbox("demo", repo)
	sb.Spec.Clone = false

	if err := svc.addHostRemote(context.Background(), sb); err != nil {
		t.Fatalf("addHostRemote: %v", err)
	}
	if got := remotes(t, repo); got != "" {
		t.Errorf("remotes = %q, want none", got)
	}
}
