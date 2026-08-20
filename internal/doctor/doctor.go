// Package doctor answers "is this installation going to work", by checking the
// things plbx needs that live outside any one sandbox.
//
// It sits below the presentation layer because the checks reach the container
// runtime and the store directly, which `cmd/plbx` is not allowed to do. What
// the CLI gets back is a list of findings to render.
package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/rhizomatous/planterbox/internal/api"
	"github.com/rhizomatous/planterbox/internal/daemon"
	runnerpkg "github.com/rhizomatous/planterbox/internal/runner"
	"github.com/rhizomatous/planterbox/internal/store"
)

// Check is one thing that was looked at.
//
// Fix is the point of the exercise: a report that says something is wrong and
// stops there has moved the problem rather than helped with it.
type Check struct {
	Name   string
	OK     bool
	Detail string
	Fix    string
}

// Run performs every check and returns them in the order they matter: a
// missing runtime makes everything below it moot, so it goes first.
func Run(ctx context.Context, cliVersion string) []Check {
	checks := []Check{checkRuntime(ctx)}

	env := daemon.HostEnv(runtime.GOOS)
	info, running := daemon.Info(ctx, env)
	checks = append(checks, checkDaemon(env, info, running))
	if running {
		checks = append(checks, checkVersions(cliVersion, info.Version))
	}
	return append(checks, checkStateDir())
}

// checkRuntime looks for a container runtime that is both installed and
// answering. plbx ships no runtime of its own, so this is the one dependency
// it cannot do anything about itself.
func checkRuntime(ctx context.Context) Check {
	rt, err := runnerpkg.Detect(ctx)
	if err != nil {
		if installed, iErr := runnerpkg.DetectInstalled(ctx); iErr == nil {
			return Check{
				Name:   "container runtime",
				Detail: installed.Name + " is installed but not answering",
				Fix:    "start " + installed.Name + " and run this again",
			}
		}
		return Check{
			Name:   "container runtime",
			Detail: err.Error(),
			Fix:    "install docker, OrbStack, or podman",
		}
	}
	return Check{Name: "container runtime", OK: true, Detail: rt.Name + "  " + rt.Path}
}

// checkDaemon reports the daemon, and treats one that is not running as fine:
// plbx starts it on demand, so its absence is a resting state rather than a
// fault.
func checkDaemon(env daemon.Env, info api.DaemonInfo, running bool) Check {
	socket, _ := daemon.Socket(env)
	if !running {
		return Check{
			Name:   "daemon",
			OK:     true,
			Detail: "not running; the next command that needs it starts one",
		}
	}
	version := info.Version
	if version == "" {
		version = "an unknown version"
	}
	return Check{Name: "daemon", OK: true, Detail: version + "  " + socket}
}

// checkVersions is the one that would have caught a daemon serving behaviour
// its own build had already replaced.
func checkVersions(cli, daemonVersion string) Check {
	switch {
	case daemonVersion == "":
		return Check{
			Name:   "versions match",
			Detail: "the daemon predates plbx " + cli + " and cannot say what it is",
			Fix:    "plbx daemon restart",
		}
	case daemonVersion != cli:
		return Check{
			Name:   "versions match",
			Detail: "plbx is " + cli + ", the daemon is " + daemonVersion,
			Fix:    "plbx daemon restart",
		}
	}
	return Check{Name: "versions match", OK: true, Detail: cli}
}

// checkStateDir proves the sandbox records can actually be written, rather
// than that a directory exists: a read-only or wrongly-owned one looks
// entirely healthy until the first create.
func checkStateDir() Check {
	env, err := store.HostEnv(runtime.GOOS)
	if err != nil {
		return Check{Name: "state directory", Detail: err.Error()}
	}
	dir, err := store.Root(env)
	if err != nil {
		return Check{Name: "state directory", Detail: err.Error()}
	}

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return Check{
			Name: "state directory", OK: true,
			Detail: dir + "  (not yet created; the first sandbox makes it)",
		}
	}

	probe := filepath.Join(dir, ".plbx-doctor")
	if err := os.WriteFile(probe, nil, 0o600); err != nil {
		return Check{
			Name:   "state directory",
			Detail: fmt.Sprintf("%s is not writable: %v", dir, err),
			Fix:    "check the ownership and mode of " + dir,
		}
	}
	_ = os.Remove(probe)
	return Check{Name: "state directory", OK: true, Detail: dir}
}
