// Package api defines the boundary between plbx's user-facing layers and the
// machinery that runs sandboxes. The CLI and the TUI hold a [Service] and never
// reach past it to a store, a runner, or a container runtime.
//
// Nothing in [Service] assumes where the work happens, so an implementation is
// free to do it in-process or hand it to a daemon without callers noticing.
package api

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strconv"
	"time"

	"github.com/rhizomatous/planterbox/internal/proxy"
)

// sentinel errors callers are expected to match with errors.Is.
var (
	ErrNotFound       = errors.New("sandbox not found")
	ErrExists         = errors.New("sandbox already exists")
	ErrRunning        = errors.New("sandbox is running")
	ErrNotImplemented = errors.New("not implemented")
	// ErrNoPolicy means no egress policy has been chosen yet, which is how a
	// first run knows to ask.
	ErrNoPolicy = errors.New("no network policy has been set")
	// ErrRemoteNotAdded is its own sentinel because the sandbox exists and
	// works; what failed is the remote your repository fetches from.
	ErrRemoteNotAdded = errors.New("could not add the sandbox as a git remote")
	// ErrCloneFailed is its own sentinel because the sandbox is running and
	// usable; what is missing is the copy the agent was meant to work in.
	ErrCloneFailed = errors.New("could not make the sandbox's clone")
	// ErrPortsUnavailable is its own sentinel because a start that hits it has
	// still started the sandbox. Usually the host already holds one of the
	// ports.
	ErrPortsUnavailable = errors.New("ports could not be published")
)

// Service is everything plbx can do to a sandbox.
type Service interface {
	// Create registers a new sandbox and its container, without starting it.
	Create(ctx context.Context, spec Spec) (Sandbox, error)
	// List returns every known sandbox, each with its observed state.
	List(ctx context.Context) ([]Sandbox, error)
	// Inspect returns one sandbox by reference.
	Inspect(ctx context.Context, ref Ref) (Sandbox, error)
	// Start boots a created or stopped sandbox.
	Start(ctx context.Context, ref Ref) error
	// Stop halts a running sandbox, leaving its contents intact.
	Stop(ctx context.Context, ref Ref) error
	// Remove deletes a sandbox and everything in it. It refuses a running
	// sandbox unless force is set.
	Remove(ctx context.Context, ref Ref, force bool) error
	// Exec runs a command inside a sandbox, with streams wired to its stdio.
	Exec(ctx context.Context, ref Ref, req ExecRequest, streams Streams) (ExecResult, error)
	// Copy moves files between the host and a sandbox. Exactly one of src and
	// dst must name a sandbox, and that is the sandbox operated on.
	Copy(ctx context.Context, src, dst Path) error
	// Publish replaces the set of ports a sandbox publishes on the host. It
	// takes effect at once on a running sandbox, and on next start otherwise.
	// An empty list publishes nothing.
	Publish(ctx context.Context, ref Ref, ports []Port) error
	// Stats streams resource samples for a running sandbox until it stops or
	// ctx is cancelled. Callers must drain the channel or cancel ctx.
	Stats(ctx context.Context, ref Ref) (<-chan Stats, error)
	// Policy returns the host's egress policy, or ErrNoPolicy when none has
	// been chosen yet.
	Policy(ctx context.Context) (proxy.Policy, error)
	// SetPolicy replaces the host's egress policy. It applies to every
	// sandbox from the next connection onward.
	SetPolicy(ctx context.Context, p proxy.Policy) error
	// PullImage fetches a sandbox image, yielding the runtime's progress a
	// line at a time and closing when it is done. An image already present
	// yields nothing at all, because nothing has to happen.
	//
	// Separate from Create because it is the slow half, and the only half
	// with anything to report.
	PullImage(ctx context.Context, image string) (<-chan string, error)

	// Connections returns the proxy's decisions recorded after since. Passing
	// zero returns everything still held.
	Connections(ctx context.Context, since uint64) ([]proxy.Entry, error)
	// Close releases whatever the implementation holds open.
	Close() error
}

// Informer is a [Service] that can also describe the daemon behind it. It is
// separate because the in-process implementation has no daemon to describe, so
// a caller asks for it rather than assuming it.
type Informer interface {
	Info(ctx context.Context) (DaemonInfo, error)
}

// SSHDomain is the suffix plbx's ssh hosts carry. `myrepo.plbx` is the sandbox
// `myrepo`, and the suffix is what keeps a managed `Host *.plbx` block in an
// ssh config from matching anything else in it.
const SSHDomain = "plbx"

// AgentHome is the base image contract's home directory. It gets its own named
// volume, which makes a sandbox persistent: packages, shell history, and
// agent state all live under it.
const AgentHome = "/home/agent"

// SSHHost is the ssh hostname for a sandbox.
func SSHHost(sandbox string) string { return sandbox + "." + SSHDomain }

// Ref identifies a sandbox, either by name or by a workspace path it was
// created for.
type Ref struct {
	Name string
	Path string
}

// ByName returns a Ref matching on sandbox name.
func ByName(name string) Ref { return Ref{Name: name} }

// ByPath returns a Ref matching on primary workspace path.
func ByPath(path string) Ref { return Ref{Path: path} }

// String renders the ref for error messages.
func (r Ref) String() string {
	switch {
	case r.Name != "":
		return r.Name
	case r.Path != "":
		return r.Path
	default:
		return "<unspecified>"
	}
}

// Spec is what a sandbox's container was built from, and it does not change.
//
// Everything in it is an argument to the runtime's create: changing any of it
// means building a different container, which costs everything the old one held
// outside its home volume. Ports are deliberately absent: they are not part of
// the container. See [Sandbox.Ports].
type Spec struct {
	Name       string            `json:"name"`
	Agent      string            `json:"agent,omitempty"`
	Image      string            `json:"image"`
	Workspaces []Workspace       `json:"workspaces,omitempty"`
	Resources  Resources         `json:"resources,omitzero"`
	Env        map[string]string `json:"env,omitempty"`
	// Clone gives the sandbox a private clone of the primary workspace rather
	// than write access to it. The host repository is mounted read-only, so
	// nothing the agent does reaches your tree: not a stray edit, not a
	// .git/hooks script that would run on your machine later.
	Clone     bool      `json:"clone,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// CloneDir is where a clone-mode sandbox keeps its copy of the primary
// workspace.
//
// Under the home volume so it persists, and not at the workspace's own path,
// which the read-only mount of the original occupies.
func (s Spec) CloneDir() string {
	primary := s.Primary().Host
	if primary == "" {
		return ""
	}
	return "/home/agent/" + filepath.Base(primary)
}

// Workdir is the directory a session opens in.
func (s Spec) Workdir() string {
	if s.Clone {
		return s.CloneDir()
	}
	return s.Primary().Host
}

// Primary returns the sandbox's primary [Workspace], or the zero Workspace when
// it has none.
func (s Spec) Primary() Workspace {
	if len(s.Workspaces) == 0 {
		return Workspace{}
	}
	return s.Workspaces[0]
}

// Workspace is a host directory bound into the sandbox at the same absolute
// path it has on the host, so paths in errors and build output resolve on both
// sides.
//
// A sandbox can have several workspaces. The first is the primary: it becomes
// the sandbox's working directory, and it is the path reattach matches on. The
// rest are extras, usually read-only.
type Workspace struct {
	Host     string `json:"host"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

// Resources specifies limits for a sandbox's resource consumption. Zero means unlimited.
type Resources struct {
	CPUs   float64 `json:"cpus,omitempty"`
	Memory int64   `json:"memory,omitempty"` // bytes
}

// Port publishes a sandbox port on the host.
type Port struct {
	Host    int    `json:"host"`
	Sandbox int    `json:"sandbox"`
	Proto   string `json:"proto,omitempty"` // "tcp", which is also the default
	// Bind is the host address to publish on. Empty means every interface,
	// the runtime's own default. "127.0.0.1" keeps it on this machine, which
	// is what anything unauthenticated needs; plbx's git-daemon uses it.
	Bind string `json:"bind,omitempty"`
}

// Address renders a port the way a runtime's --publish reads it.
func (p Port) Address() string {
	s := strconv.Itoa(p.Host) + ":" + strconv.Itoa(p.Sandbox)
	if p.Bind != "" {
		s = p.Bind + ":" + s
	}
	if p.Proto != "" && p.Proto != "tcp" {
		s += "/" + p.Proto
	}
	return s
}

// State is what plbx last observed about a sandbox, derived from the runtime.
type State struct {
	Status      Status    `json:"status"`
	ContainerID string    `json:"container_id,omitempty"`
	StartedAt   time.Time `json:"started_at,omitzero"`
	ExitCode    int       `json:"exit_code,omitempty"`
}

// Status is a sandbox's lifecycle position.
type Status string

// the statuses a sandbox can report.
const (
	StatusUnknown Status = "unknown" // the runtime had nothing to say
	StatusCreated Status = "created" // exists, never started
	StatusRunning Status = "running"
	StatusStopped Status = "stopped"
	StatusMissing Status = "missing" // recorded here, gone from the runtime
)

// Sandbox is a stored spec, the state plbx last observed, and the ports the
// sandbox publishes.
type Sandbox struct {
	Spec  Spec  `json:"spec"`
	State State `json:"state"`
	// Ports are published on the host while the sandbox runs. Unlike Spec they
	// can change at any time: a sandbox cannot publish for itself, so its ports
	// live in a forwarder beside it that is rebuilt on every start anyway.
	Ports []Port `json:"ports,omitempty"`
}

// DaemonInfo is what a running daemon reports about itself.
//
// The version matters because plbx autostarts plbxd, and the daemon outlives
// the upgrade that replaced it. A CLI talking to a daemon from the previous
// build is the normal result of installing a new version, and nothing else
// about it looks wrong.
type DaemonInfo struct {
	Version   string    `json:"version"`
	StartedAt time.Time `json:"started_at,omitzero"`
	PID       int       `json:"pid,omitempty"`
}

// ExecRequest describes a command to run inside a sandbox.
type ExecRequest struct {
	Cmd         []string
	Env         map[string]string
	Workdir     string
	User        string
	Interactive bool // wire up stdin
	TTY         bool // allocate a pseudo-terminal
}

// ExecResult is what a finished exec reports back.
type ExecResult struct {
	ExitCode int
}

// Streams are the ends of a session's stdio.
//
// Exec takes them explicitly because the process running the command is not
// always the one holding the terminal. In-process they are the CLI's own
// stdio; with a daemon they are the far end of a socket, and the daemon has no
// terminal of its own to fall back on.
type Streams struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// Resize carries the terminal's dimensions, the first before the session
	// starts and one on every change. Nil when there is no terminal to track.
	Resize <-chan Size
}

// Size is a terminal's dimensions.
type Size struct {
	Rows, Cols uint16
}

// Stats is one sample of a running sandbox's resource use.
type Stats struct {
	CPUPercent  float64
	MemoryBytes int64
	MemoryLimit int64
}

// MemoryPercent is memory use as a share of the limit, or 0 when no limit is
// known, which is what an unlimited sandbox reports.
func (s Stats) MemoryPercent() float64 {
	if s.MemoryLimit <= 0 {
		return 0
	}
	return float64(s.MemoryBytes) / float64(s.MemoryLimit) * 100
}

// Path names a file on one side of a copy. Sandbox paths are written
// "<sandbox>:/path"; a bare path is on the host.
type Path struct {
	Sandbox string // empty for a host path
	Path    string
}

// InSandbox reports whether the path lives inside a sandbox.
func (p Path) InSandbox() bool { return p.Sandbox != "" }

// CopyRef picks the sandbox a copy runs against. Exactly one side may name one:
// two sandbox paths have no host leg to route through, and zero is a
// host-to-host copy that has nothing to do with plbx.
func CopyRef(src, dst Path) (Ref, error) {
	switch {
	case src.InSandbox() && dst.InSandbox():
		return Ref{}, errors.New("only one side of a copy may name a sandbox")
	case src.InSandbox():
		return ByName(src.Sandbox), nil
	case dst.InSandbox():
		return ByName(dst.Sandbox), nil
	default:
		return Ref{}, errors.New("one side of a copy must name a sandbox, as <sandbox>:/path")
	}
}

// String renders the path in the form it was parsed from.
func (p Path) String() string {
	if p.InSandbox() {
		return p.Sandbox + ":" + p.Path
	}
	return p.Path
}
