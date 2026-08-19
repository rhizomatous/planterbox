// Package direct implements api.Service in-process, against a local store and
// container runtime.
package direct

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rhizomatous/planterbox/internal/api"
	"github.com/rhizomatous/planterbox/internal/proxy"
	"github.com/rhizomatous/planterbox/internal/runner"
	"github.com/rhizomatous/planterbox/internal/store"
)

// Service is the in-process implementation of [api.Service].
type Service struct {
	store  *store.Store
	runner runner.Runner
	now    func() time.Time
	// log is the proxy's record of what it decided, when a proxy is running.
	log *proxy.Log
}

// WithConnectionLog gives the service the proxy's log to read. The daemon
// passes the same one it hands the proxy.
func WithConnectionLog(l *proxy.Log) Option {
	return func(s *Service) { s.log = l }
}

var _ api.Service = (*Service)(nil)

// Option configures a [Service].
type Option func(*Service)

// WithClock replaces the clock used for created-at timestamps.
func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

// New returns a service backed by st and rn.
func New(st *store.Store, rn runner.Runner, opts ...Option) *Service {
	s := &Service{store: st, runner: rn, now: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Create registers a sandbox and builds its container, without starting it.
func (s *Service) Create(ctx context.Context, spec api.Spec) (api.Sandbox, error) {
	if err := spec.Validate(); err != nil {
		return api.Sandbox{}, err
	}
	if _, err := s.store.Get(spec.Name); err == nil {
		return api.Sandbox{}, fmt.Errorf("%w: %q", api.ErrExists, spec.Name)
	} else if !errors.Is(err, store.ErrNotFound) {
		return api.Sandbox{}, err
	}

	if spec.CreatedAt.IsZero() {
		spec.CreatedAt = s.now().UTC()
	}
	id, err := s.runner.Create(ctx, spec)
	if err != nil {
		return api.Sandbox{}, err
	}

	sb := api.Sandbox{
		Spec:  spec,
		State: api.State{Status: api.StatusCreated, ContainerID: string(id)},
	}
	if err := s.store.Put(sb); err != nil {
		return api.Sandbox{}, err
	}
	// a clone-mode sandbox becomes a remote in the repository it was made for,
	// so its work can be fetched back. Not fatal: the sandbox is made either
	// way, and this writes to a repository that belongs to the user.
	if err := s.addHostRemote(ctx, sb); err != nil {
		return sb, fmt.Errorf("%w: %w", api.ErrRemoteNotAdded, err)
	}
	return sb, nil
}

// List returns every known sandbox, each refreshed against the runtime.
func (s *Service) List(ctx context.Context) ([]api.Sandbox, error) {
	stored, err := s.store.List()
	if err != nil {
		return nil, err
	}
	for i, sb := range stored {
		stored[i].State = s.observe(ctx, sb)
	}
	return stored, nil
}

// Inspect returns one sandbox, refreshed against the runtime.
func (s *Service) Inspect(ctx context.Context, ref api.Ref) (api.Sandbox, error) {
	sb, err := s.find(ref)
	if err != nil {
		return api.Sandbox{}, err
	}
	sb.State = s.observe(ctx, sb)
	return sb, nil
}

// Start boots a created or stopped sandbox, and publishes its ports.
//
// Publishing happens on every start rather than at create time. A sandbox is
// alone on an internal network, where a runtime accepts --publish and creates
// nothing, so the mapping lives in a forwarder beside the sandbox instead of
// in the sandbox's own container.
func (s *Service) Start(ctx context.Context, ref api.Ref) error {
	var started api.Sandbox
	err := s.act(ctx, ref, func(sb api.Sandbox, id runner.ID) error {
		started = sb
		return s.runner.Start(ctx, id, sb.Spec.Name)
	})
	if err != nil {
		return err
	}
	// the clone needs a running container to be made in, so it happens here
	// rather than at create.
	if err := s.ensureClone(ctx, started); err != nil {
		return err
	}
	// publishing is separate from the start, and after the state is written.
	// A sandbox whose ports were refused is running all the same, and a record
	// saying otherwise would be wrong.
	return s.publish(ctx, started)
}

// Stop halts a running sandbox, leaving its contents intact.
//
// Its ports come off the host with it. A forwarder outliving the sandbox would
// hold those ports bound and answer on them, against something no longer
// listening.
func (s *Service) Stop(ctx context.Context, ref api.Ref) error {
	return s.act(ctx, ref, func(sb api.Sandbox, id runner.ID) error {
		if err := s.runner.Stop(ctx, id); err != nil {
			return err
		}
		return s.runner.Unpublish(ctx, sb.Spec.Name)
	})
}

// Remove deletes a sandbox and everything in it. A running sandbox is refused
// unless force is set, since removing one out from under a live session loses
// whatever it was doing.
func (s *Service) Remove(ctx context.Context, ref api.Ref, force bool) error {
	sb, err := s.find(ref)
	if err != nil {
		return err
	}
	if !force && s.observe(ctx, sb).Status == api.StatusRunning {
		return fmt.Errorf("%w: %q (use --force)", api.ErrRunning, sb.Spec.Name)
	}
	if err := s.runner.Remove(ctx, containerID(sb), sb.Spec.Name, force); err != nil {
		return err
	}
	// the remote goes with the sandbox it pointed at; left behind it names a
	// host that no longer answers.
	s.dropHostRemote(ctx, sb)
	return s.store.Delete(sb.Spec.Name)
}

// Exec runs a command inside a sandbox.
func (s *Service) Exec(ctx context.Context, ref api.Ref, req api.ExecRequest, streams api.Streams) (api.ExecResult, error) {
	if err := req.Validate(); err != nil {
		return api.ExecResult{}, err
	}
	sb, err := s.find(ref)
	if err != nil {
		return api.ExecResult{}, err
	}
	return s.runner.Exec(ctx, containerID(sb), req, streams)
}

// Publish replaces the ports a sandbox publishes on the host.
//
// The record is written first and the forwarder brought into line after, so
// the set survives a restart even if the runtime refuses it now. A sandbox
// that is not running gets the record only: there is nothing to forward to
// yet, and a forwarder standing by would hold the host ports against it.
func (s *Service) Publish(ctx context.Context, ref api.Ref, ports []api.Port) error {
	if err := api.ValidatePorts(ports); err != nil {
		return err
	}
	sb, err := s.find(ref)
	if err != nil {
		return err
	}
	sb.Ports = ports
	if err := s.store.Put(sb); err != nil {
		return err
	}
	if s.observe(ctx, sb).Status != api.StatusRunning {
		return nil
	}
	return s.publish(ctx, sb)
}

// publish brings the forwarder into line with what the record says.
func (s *Service) publish(ctx context.Context, sb api.Sandbox) error {
	if err := s.runner.Publish(ctx, sb.Spec.Name, sb.Ports); err != nil {
		return fmt.Errorf("%w: %w", api.ErrPortsUnavailable, err)
	}
	return nil
}

// Copy moves files between the host and a sandbox, named by whichever side of
// the copy carries one.
func (s *Service) Copy(ctx context.Context, src, dst api.Path) error {
	ref, err := api.CopyRef(src, dst)
	if err != nil {
		return err
	}
	sb, err := s.find(ref)
	if err != nil {
		return err
	}
	return s.runner.Copy(ctx, containerID(sb), src, dst)
}

// Stats streams resource samples for a running sandbox.
func (s *Service) Stats(ctx context.Context, ref api.Ref) (<-chan api.Stats, error) {
	sb, err := s.find(ref)
	if err != nil {
		return nil, err
	}
	return s.runner.Stats(ctx, containerID(sb))
}

// PullImage fetches an image if the runtime does not already have it.
func (s *Service) PullImage(ctx context.Context, image string) (<-chan string, error) {
	return s.runner.PullImage(ctx, image)
}

// Close releases nothing: the direct service holds no long-lived handles.
func (s *Service) Close() error { return nil }

// act resolves a ref and applies fn to its container. fn gets the stored
// sandbox as well as its runtime handle, because the two name different things:
// the handle is whatever the runtime last called the container, while anything
// plbx created alongside it is named after the sandbox.
func (s *Service) act(ctx context.Context, ref api.Ref, fn func(api.Sandbox, runner.ID) error) error {
	sb, err := s.find(ref)
	if err != nil {
		return err
	}
	if err := fn(sb, containerID(sb)); err != nil {
		return err
	}
	sb.State = s.observe(ctx, sb)
	return s.store.Put(sb)
}

// find resolves a ref to a stored sandbox, translating the store's not-found
// into the api's so callers only match one sentinel.
func (s *Service) find(ref api.Ref) (api.Sandbox, error) {
	sb, err := s.store.Find(ref)
	if errors.Is(err, store.ErrNotFound) {
		return api.Sandbox{}, fmt.Errorf("%w: %q", api.ErrNotFound, ref)
	}
	return sb, err
}

// observe asks the runtime for a sandbox's live state, falling back to the last
// state we recorded when the runtime can't answer. A stale status is better
// than an error on a command that only wanted to list.
func (s *Service) observe(ctx context.Context, sb api.Sandbox) api.State {
	state, err := s.runner.Inspect(ctx, containerID(sb))
	if err != nil {
		return sb.State
	}
	if state.ContainerID == "" {
		state.ContainerID = sb.State.ContainerID
	}
	return state
}

// containerID is the runtime handle for a sandbox, recovered from its name when
// the record predates one.
func containerID(sb api.Sandbox) runner.ID {
	if sb.State.ContainerID != "" {
		return runner.ID(sb.State.ContainerID)
	}
	return runner.ID(runner.ContainerName(sb.Spec.Name))
}

// Policy returns the host's egress policy.
func (s *Service) Policy(context.Context) (proxy.Policy, error) {
	p, err := s.store.Policy()
	if errors.Is(err, store.ErrNoPolicy) {
		return proxy.Policy{}, api.ErrNoPolicy
	}
	return p, err
}

// SetPolicy replaces the host's egress policy.
func (s *Service) SetPolicy(_ context.Context, p proxy.Policy) error {
	return s.store.SetPolicy(p)
}

// SSHHostKeyPath reports where the ssh gateway's identity is kept. The daemon
// asks, because the store is what knows where anything durable goes.
func (s *Service) SSHHostKeyPath() string { return s.store.SSHHostKeyPath() }

// Connections returns the proxy's decisions recorded after since.
//
// A service with no proxy behind it reports none rather than failing: the
// in-process path serves --dry-run and --state-dir, neither of which has one.
func (s *Service) Connections(_ context.Context, since uint64) ([]proxy.Entry, error) {
	if s.log == nil {
		return nil, nil
	}
	return s.log.Since(since), nil
}
