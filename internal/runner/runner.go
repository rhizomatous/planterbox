// Package runner is plbx's adapter boundary over the container runtimes.
// docker, podman, OrbStack, and colima differ enough (rootless podman most of
// all) to want one seam, and a fake behind the same interface is what lets the
// rest of plbx be unit-tested with no live runtime.
//
// No runtime-specific type crosses this line.
package runner

import (
	"context"

	"github.com/rhizomatous/planterbox/internal/api"
)

// Runner drives one container runtime.
//
// Every method identifies a sandbox by its name. The container plbx made for it
// is named after it, so the name is the handle, and a runtime that needed
// another one would derive it the same way for itself.
type Runner interface {
	Create(ctx context.Context, spec api.Spec) error
	// PullImage fetches an image if it is not already here, yielding the
	// runtime's own progress a line at a time. An image already present
	// yields nothing: `create` only pulls what is missing, and pulling
	// anyway would put a registry round trip in front of every sandbox.
	PullImage(ctx context.Context, image string) (<-chan string, error)
	Start(ctx context.Context, sandbox string) error
	// Stop halts a sandbox and takes its sidecars down with it. The relay and
	// the port forwarder exist to serve a running sandbox and hold host
	// resources while they live, so they go when it does.
	Stop(ctx context.Context, sandbox string) error
	Remove(ctx context.Context, sandbox string, force bool) error
	Exec(ctx context.Context, sandbox string, req api.ExecRequest, streams api.Streams) (api.ExecResult, error)
	Copy(ctx context.Context, sandbox string, src, dst api.Path) error
	Stats(ctx context.Context, sandbox string) (<-chan api.Stats, error)
	// Publish makes a sandbox's ports reachable from the host, replacing
	// whatever it had published before. An empty list publishes nothing.
	Publish(ctx context.Context, sandbox string, ports []api.Port) error
	// Unpublish takes them back off, tolerating a sandbox that published none.
	Unpublish(ctx context.Context, sandbox string) error
	Inspect(ctx context.Context, sandbox string) (api.State, error)
}
