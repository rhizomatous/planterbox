package direct

import (
	"context"
	"io"
	"runtime"

	"github.com/rhizomatous/planterbox/internal/proxy"
	"github.com/rhizomatous/planterbox/internal/runner"
	"github.com/rhizomatous/planterbox/internal/store"
)

// Options describes how to assemble a service against the local host.
type Options struct {
	// StateDir overrides where sandbox records are kept.
	StateDir string
	// DryRun renders every runtime invocation to DryRunOut and executes none of
	// them. It also relaxes runtime detection, since nothing needs to run.
	DryRun bool
	// DryRunOut receives rendered invocations. Defaults to io.Discard.
	DryRunOut io.Writer
	// ConnectionLog is the proxy's record, when a proxy is running alongside.
	ConnectionLog *proxy.Log
	// EgressProxy is where a sandbox's traffic ends up, as the relay running
	// inside the runtime can reach it. Empty leaves sandboxes on the runtime's
	// default network with no egress control, which is what --dry-run and the
	// in-process path want: neither has a daemon holding a proxy.
	EgressProxy string
	// RelayImage overrides the published relay.
	RelayImage string
}

// Open assembles a service against the local store and container runtime. It
// exists so cmd/plbx and internal/tui can get a working api.Service without
// importing internal/store or internal/runner themselves.
func Open(ctx context.Context, opts Options) (*Service, error) {
	dir := opts.StateDir
	if dir == "" {
		env, err := store.HostEnv(runtime.GOOS)
		if err != nil {
			return nil, err
		}
		if dir, err = store.Root(env); err != nil {
			return nil, err
		}
	}
	open := store.Open
	if opts.DryRun {
		// a dry run reports what plbx would do; leaving a record behind is
		// something it did.
		open = store.OpenReadOnly
	}
	st, err := open(dir)
	if err != nil {
		return nil, err
	}

	detect := runner.Detect
	var runnerOpts []runner.Option
	if opts.EgressProxy != "" {
		runnerOpts = append(runnerOpts, runner.WithEgress(opts.EgressProxy, opts.RelayImage))
	}
	if opts.DryRun {
		detect = runner.DetectInstalled
		out := opts.DryRunOut
		if out == nil {
			out = io.Discard
		}
		runnerOpts = append(runnerOpts, runner.WithDryRun(out))
	}

	var serviceOpts []Option
	if opts.ConnectionLog != nil {
		serviceOpts = append(serviceOpts, WithConnectionLog(opts.ConnectionLog))
	}

	// a runtime we can't reach isn't fatal on its own. hand back a runner that
	// fails every call with this error, so it surfaces on the first command
	// that actually needs one.
	rt, err := detect(ctx)
	switch {
	case err == nil:
	case opts.DryRun:
		// nothing is executed under --dry-run, so render against a nominal
		// runtime rather than refusing. Inspecting what plbx would do is the
		// one thing that must work on a machine with no runtime at all.
		rt = runner.Runtime{Name: "docker", Path: "docker"}
	default:
		return New(st, runner.Unavailable(err), serviceOpts...), nil
	}
	return New(st, runner.NewOCI(rt, runnerOpts...), serviceOpts...), nil
}
