package direct

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/rhizomatous/planterbox/internal/api"
)

// Clone mode gives a sandbox a copy of your repository rather than your
// repository.
//
// The original mounts read-only and the agent works in a clone under the home
// volume, so nothing it writes reaches your tree: not a stray edit, not a
// .git/hooks script that would run on your machine later.
//
// The clone is made on first start rather than at create, because it needs a
// running container to make it in. It is idempotent: a sandbox that already
// has one is left alone, so restarting never discards work.

// hostRemote is what the clone calls the read-only original.
//
// Not "origin". A clone names its source origin by default, which would put a
// path that only exists inside this sandbox where the real upstream belongs,
// and `git push origin` would then hit a read-only mount rather than github.
// The real remotes are copied over as themselves; this one gets its own name.
const hostRemote = "host"

// cloneScript builds the clone and points it at the right remotes.
//
// The clone's own path may exist before the clone does, and belong to root;
// see the script. --no-hardlinks is load-bearing: a local clone links its
// objects to the source by default, which would tie the sandbox's object store
// to a read-only mount of your repository and to the files behind it.
//
// Remotes are copied from the original, minus any that name a local path.
// Those resolve to somewhere on your machine that does not exist in here, and
// a remote that silently points at nothing is worse than one that is absent.
const cloneScript = `set -e
if [ -e "$PLBX_CLONE/.git" ]; then exit 0; fi
# the runtime makes the container's working directory before anything runs,
# and makes it as root, so on a first start the clone's own path is already
# there, owned by someone the agent is not. rmdir refuses a directory with
# anything in it, so this can only ever clear the empty one left behind.
rmdir "$PLBX_CLONE" 2>/dev/null || true
git clone --no-hardlinks --origin ` + hostRemote + ` "$PLBX_SRC" "$PLBX_CLONE"
git -C "$PLBX_SRC" config --get-regexp '^remote\..*\.url' 2>/dev/null | while read -r key url; do
  name=${key#remote.}
  name=${name%.url}
  [ "$name" = "` + hostRemote + `" ] && continue
  case "$url" in
    /*|./*|../*|file://*) continue ;;
  esac
  git -C "$PLBX_CLONE" remote add "$name" "$url" 2>/dev/null ||
    git -C "$PLBX_CLONE" remote set-url "$name" "$url"
done
exit 0
`

// ensureClone makes a clone-mode sandbox's clone, if it has not got one.
func (s *Service) ensureClone(ctx context.Context, sb api.Sandbox) error {
	if !sb.Spec.Clone {
		return nil
	}
	src := sb.Spec.Primary().Host
	dst := sb.Spec.CloneDir()
	if src == "" || dst == "" {
		return nil
	}

	var out bytes.Buffer
	res, err := s.runner.Exec(ctx, sb.Spec.Name, api.ExecRequest{
		Cmd: []string{"/bin/bash", "-c", cloneScript},
		Env: map[string]string{"PLBX_SRC": src, "PLBX_CLONE": dst},
		// explicitly the home directory, not the container's own default. That
		// default is the clone's path, which does not exist yet on a first
		// start: the runtime makes an empty directory for it, and git then
		// finds itself cloning into its own working directory and fails the
		// checkout.
		Workdir: api.AgentHome,
		User:    "agent",
	}, api.Streams{Stdin: emptyReader{}, Stdout: &out, Stderr: &out})
	if err != nil {
		return fmt.Errorf("%w: %w", api.ErrCloneFailed, err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("%w: %s", api.ErrCloneFailed, lastLine(out.String()))
	}
	return nil
}

// emptyReader stands in for stdin, which the clone never reads. A nil reader
// would have the runtime wait on a stream nothing is going to write.
type emptyReader struct{}

func (emptyReader) Read([]byte) (int, error) { return 0, io.EOF }

// lastLine trims git's answer to the part worth reporting.
//
// The last line rather than the first: git opens with progress ("Cloning
// into ...") and puts the reason it gave up at the end, so reporting the first
// line hands back a status message where the error should be.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return "no output"
}

// The host reaches the clone over the ssh gateway.
//
// git speaks its protocol over ssh by running git-upload-pack on the far side,
// and the gateway already runs commands in a sandbox. So the remote is a fixed
// ssh URL that survives restarts, with no daemon to supervise, no port to
// allocate, and nothing unauthenticated listening.
//
// The remote only resolves once `plbx setup ssh` has been run, which is why
// the CLI says so when it adds one.

// HostRemote is what a sandbox's clone is called in your repository.
func HostRemote(sandbox string) string { return "plbx-" + sandbox }

// cloneURL is the ssh URL your repository fetches the clone from.
func cloneURL(sb api.Sandbox) string {
	return api.SSHHost(sb.Spec.Name) + ":" + sb.Spec.CloneDir()
}

// addHostRemote points your repository at the sandbox's clone.
//
// Failure is reported but not fatal. The sandbox is made and works; what is
// missing is a shortcut, and this writes to a repository that belongs to the
// user.
func (s *Service) addHostRemote(ctx context.Context, sb api.Sandbox) error {
	repo := sb.Spec.Primary().Host
	if !sb.Spec.Clone || repo == "" {
		return nil
	}
	name, url := HostRemote(sb.Spec.Name), cloneURL(sb)
	if err := git(ctx, repo, "remote", "add", name, url); err != nil {
		// already there, from a sandbox of the same name before this one.
		return git(ctx, repo, "remote", "set-url", name, url)
	}
	return nil
}

// dropHostRemote takes it back out, tolerating one already gone.
func (s *Service) dropHostRemote(ctx context.Context, sb api.Sandbox) {
	repo := sb.Spec.Primary().Host
	if !sb.Spec.Clone || repo == "" {
		return
	}
	_ = git(ctx, repo, "remote", "remove", HostRemote(sb.Spec.Name))
}

// git runs one git command against a repository on the host.
func git(ctx context.Context, repo string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %s", strings.Join(args, " "), lastLine(string(out)))
	}
	return nil
}
