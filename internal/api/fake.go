package api

import (
	"context"
	"slices"

	"github.com/rhizomatous/planterbox/internal/proxy"
)

// Fake is an in-memory [Service] for testing the CLI and the TUI without a
// store, a runner, or a container runtime.
type Fake struct {
	// Sandboxes is the fake's whole world, in list order.
	Sandboxes []Sandbox
	// Err, when set, is returned by every method.
	Err error
	// Samples is what Stats replays, in order.
	Samples []Stats
	// OnExec, when set, runs in place of the default and is handed the
	// session's streams, so a test can drive stdio through a real one.
	OnExec func(ctx context.Context, ref Ref, req ExecRequest, streams Streams) (ExecResult, error)
	// Calls records method names in the order they were called.
	Calls []string
	// NetworkPolicy is what Policy reports. Nil means none has been set.
	NetworkPolicy *proxy.Policy
	// StartErr, when set, fails only Start, and after the sandbox is marked
	// running. That is the case worth telling apart: a start whose ports were
	// refused has still started.
	StartErr error
	// Decisions is what Connections replays.
	Decisions []proxy.Entry
}

var _ Service = (*Fake)(nil)

// NewFake returns a fake holding the given sandboxes.
func NewFake(sandboxes ...Sandbox) *Fake {
	return &Fake{Sandboxes: sandboxes}
}

func (f *Fake) record(name string) error {
	f.Calls = append(f.Calls, name)
	return f.Err
}

// Create appends a sandbox in the created state.
func (f *Fake) Create(_ context.Context, spec Spec) (Sandbox, error) {
	if err := f.record("Create"); err != nil {
		return Sandbox{}, err
	}
	if _, ok := f.find(ByName(spec.Name)); ok {
		return Sandbox{}, ErrExists
	}
	sb := Sandbox{Spec: spec, State: State{Status: StatusCreated}}
	f.Sandboxes = append(f.Sandboxes, sb)
	return sb, nil
}

// List returns every sandbox the fake holds.
func (f *Fake) List(context.Context) ([]Sandbox, error) {
	if err := f.record("List"); err != nil {
		return nil, err
	}
	return slices.Clone(f.Sandboxes), nil
}

// Inspect returns one sandbox by reference.
func (f *Fake) Inspect(_ context.Context, ref Ref) (Sandbox, error) {
	if err := f.record("Inspect"); err != nil {
		return Sandbox{}, err
	}
	i, ok := f.find(ref)
	if !ok {
		return Sandbox{}, ErrNotFound
	}
	return f.Sandboxes[i], nil
}

// Start marks a sandbox running.
func (f *Fake) Start(_ context.Context, ref Ref) error {
	if err := f.setStatus("Start", ref, StatusRunning); err != nil {
		return err
	}
	return f.StartErr
}

// Stop marks a sandbox stopped.
func (f *Fake) Stop(_ context.Context, ref Ref) error {
	return f.setStatus("Stop", ref, StatusStopped)
}

// Remove drops a sandbox, refusing a running one unless force is set.
func (f *Fake) Remove(_ context.Context, ref Ref, force bool) error {
	if err := f.record("Remove"); err != nil {
		return err
	}
	i, ok := f.find(ref)
	if !ok {
		return ErrNotFound
	}
	if f.Sandboxes[i].State.Status == StatusRunning && !force {
		return ErrRunning
	}
	f.Sandboxes = slices.Delete(f.Sandboxes, i, i+1)
	return nil
}

// Exec reports a clean exit for any known sandbox.
func (f *Fake) Exec(ctx context.Context, ref Ref, req ExecRequest, streams Streams) (ExecResult, error) {
	if err := f.record("Exec"); err != nil {
		return ExecResult{}, err
	}
	if _, ok := f.find(ref); !ok {
		return ExecResult{}, ErrNotFound
	}
	if f.OnExec != nil {
		return f.OnExec(ctx, ref, req, streams)
	}
	return ExecResult{}, nil
}

// Copy succeeds for any known sandbox.
func (f *Fake) Copy(_ context.Context, src, dst Path) error {
	if err := f.record("Copy"); err != nil {
		return err
	}
	ref, err := CopyRef(src, dst)
	if err != nil {
		return err
	}
	if _, ok := f.find(ref); !ok {
		return ErrNotFound
	}
	return nil
}

// Publish records a sandbox's published ports.
func (f *Fake) Publish(_ context.Context, ref Ref, ports []Port) error {
	if err := f.record("Publish"); err != nil {
		return err
	}
	if err := ValidatePorts(ports); err != nil {
		return err
	}
	i, ok := f.find(ref)
	if !ok {
		return ErrNotFound
	}
	f.Sandboxes[i].Ports = ports
	return nil
}

// PullImage returns a closed channel; the fake fetches nothing.
func (f *Fake) PullImage(context.Context, string) (<-chan string, error) {
	if err := f.record("PullImage"); err != nil {
		return nil, err
	}
	ch := make(chan string)
	close(ch)
	return ch, nil
}

// Stats replays [Fake.Samples] once and closes, so a caller ranging over the
// channel terminates rather than waiting on a live runtime.
func (f *Fake) Stats(_ context.Context, ref Ref) (<-chan Stats, error) {
	if err := f.record("Stats"); err != nil {
		return nil, err
	}
	if _, ok := f.find(ref); !ok {
		return nil, ErrNotFound
	}
	ch := make(chan Stats, len(f.Samples))
	for _, s := range f.Samples {
		ch <- s
	}
	close(ch)
	return ch, nil
}

// Close does nothing.
func (f *Fake) Close() error { return f.record("Close") }

func (f *Fake) setStatus(call string, ref Ref, status Status) error {
	if err := f.record(call); err != nil {
		return err
	}
	i, ok := f.find(ref)
	if !ok {
		return ErrNotFound
	}
	f.Sandboxes[i].State.Status = status
	return nil
}

// find locates a sandbox by name, then by primary workspace path.
func (f *Fake) find(ref Ref) (int, bool) {
	for i, sb := range f.Sandboxes {
		if ref.Name != "" && sb.Spec.Name == ref.Name {
			return i, true
		}
		if ref.Path != "" && sb.Spec.Primary().Host == ref.Path {
			return i, true
		}
	}
	return 0, false
}

// Policy returns the fake's policy, or ErrNoPolicy when none was set.
func (f *Fake) Policy(context.Context) (proxy.Policy, error) {
	if err := f.record("Policy"); err != nil {
		return proxy.Policy{}, err
	}
	if f.NetworkPolicy == nil {
		return proxy.Policy{}, ErrNoPolicy
	}
	return *f.NetworkPolicy, nil
}

// SetPolicy replaces the fake's policy.
func (f *Fake) SetPolicy(_ context.Context, p proxy.Policy) error {
	if err := f.record("SetPolicy"); err != nil {
		return err
	}
	f.NetworkPolicy = &p
	return nil
}

// Connections returns the fake's recorded decisions after since.
func (f *Fake) Connections(_ context.Context, since uint64) ([]proxy.Entry, error) {
	if err := f.record("Connections"); err != nil {
		return nil, err
	}
	out := make([]proxy.Entry, 0, len(f.Decisions))
	for _, e := range f.Decisions {
		if e.Seq > since {
			out = append(out, e)
		}
	}
	return out, nil
}
