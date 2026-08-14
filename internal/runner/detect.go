package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Runtime is a resolved container CLI.
type Runtime struct {
	Name string // "docker" or "podman"
	Path string // absolute path to the binary
}

// candidates are the runtimes we hunt for, in probe order. OrbStack and colima
// both present as docker.
var candidates = []string{"docker", "podman"}

// Detect finds the first runtime that is both installed and has a reachable
// daemon. If PLBX_RUNTIME is set it is tried first and exclusively.
//
// When a CLI is installed but its daemon is unreachable (OrbStack not started,
// say), the error names that runtime so the caller can nudge the user.
func Detect(ctx context.Context) (Runtime, error) {
	return detect(ctx, true)
}

// DetectInstalled returns the first runtime on PATH without requiring its
// daemon to be reachable. This is what --dry-run uses.
func DetectInstalled(ctx context.Context) (Runtime, error) {
	return detect(ctx, false)
}

func detect(ctx context.Context, requireReachable bool) (Runtime, error) {
	order := candidates
	if forced := os.Getenv("PLBX_RUNTIME"); forced != "" {
		order = []string{forced}
	}

	var installedButDown []string
	for _, name := range order {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		if !requireReachable || reachable(ctx, path) {
			return Runtime{Name: name, Path: path}, nil
		}
		installedButDown = append(installedButDown, name)
	}

	if len(installedButDown) > 0 {
		return Runtime{}, fmt.Errorf(
			"found %s but its daemon is not reachable — is the VM/engine running? "+
				"(start OrbStack/Docker Desktop, or `podman machine start`)",
			strings.Join(installedButDown, ", "),
		)
	}
	return Runtime{}, fmt.Errorf("no container runtime found on PATH (looked for %s)",
		strings.Join(order, ", "))
}

// reachable reports whether the runtime's daemon answers an info call within a
// short timeout.
func reachable(ctx context.Context, path string) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "info")
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}
