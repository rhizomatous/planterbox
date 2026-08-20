package runner

import (
	"context"
	"fmt"

	"github.com/rhizomatous/planterbox/internal/api"
)

// Fake is an in-memory [Runner]. Store and service logic test against it with
// no container runtime present.
type Fake struct {
	// States is the fake's whole world, keyed by sandbox name.
	States map[string]api.State
	// Err, when set, is returned by every method.
	Err error
	// Calls records method names in the order they were called.
	Calls []string
	// Published is what each sandbox currently has on the host.
	Published map[string][]api.Port
	// PublishErr, when set, fails only Publish, which is the case worth telling
	// apart: a sandbox whose ports were refused has still started.
	PublishErr error
}

var _ Runner = (*Fake)(nil)

// NewFake returns an empty fake runner.
func NewFake() *Fake {
	return &Fake{States: map[string]api.State{}, Published: map[string][]api.Port{}}
}

func (f *Fake) record(name string) error {
	f.Calls = append(f.Calls, name)
	return f.Err
}

// Create registers a container in the created state.
func (f *Fake) Create(_ context.Context, spec api.Spec) error {
	if err := f.record("Create"); err != nil {
		return err
	}
	f.States[spec.Name] = api.State{Status: api.StatusCreated, ContainerID: containerName(spec.Name)}
	return nil
}

// Start marks a container running.
func (f *Fake) Start(_ context.Context, sandbox string) error {
	return f.setStatus("Start", sandbox, api.StatusRunning)
}

// Stop marks a container stopped.
func (f *Fake) Stop(ctx context.Context, sandbox string) error {
	if err := f.setStatus("Stop", sandbox, api.StatusStopped); err != nil {
		return err
	}
	return f.Unpublish(ctx, sandbox)
}

// Remove drops a container.
func (f *Fake) Remove(_ context.Context, sandbox string, _ bool) error {
	if err := f.record("Remove"); err != nil {
		return err
	}
	delete(f.States, sandbox)
	return nil
}

// Publish records a sandbox's published ports.
func (f *Fake) Publish(_ context.Context, sandbox string, ports []api.Port) error {
	if err := f.record("Publish"); err != nil {
		return err
	}
	if f.PublishErr != nil {
		return f.PublishErr
	}
	if len(ports) == 0 {
		delete(f.Published, sandbox)
		return nil
	}
	f.Published[sandbox] = ports
	return nil
}

// Unpublish drops them.
func (f *Fake) Unpublish(_ context.Context, sandbox string) error {
	if err := f.record("Unpublish"); err != nil {
		return err
	}
	delete(f.Published, sandbox)
	return nil
}

// Exec reports a clean exit.
func (f *Fake) Exec(_ context.Context, sandbox string, _ api.ExecRequest, _ api.Streams) (api.ExecResult, error) {
	if err := f.record("Exec"); err != nil {
		return api.ExecResult{}, err
	}
	if _, ok := f.States[sandbox]; !ok {
		return api.ExecResult{}, fmt.Errorf("no such container %q", sandbox)
	}
	return api.ExecResult{}, nil
}

// Copy succeeds for any known container.
func (f *Fake) Copy(_ context.Context, sandbox string, _, _ api.Path) error {
	if err := f.record("Copy"); err != nil {
		return err
	}
	if _, ok := f.States[sandbox]; !ok {
		return fmt.Errorf("no such container %q", sandbox)
	}
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

// Stats returns a closed channel; the fake streams nothing.
func (f *Fake) Stats(context.Context, string) (<-chan api.Stats, error) {
	if err := f.record("Stats"); err != nil {
		return nil, err
	}
	ch := make(chan api.Stats)
	close(ch)
	return ch, nil
}

// Inspect reports a container's state, or StatusMissing when it has none.
func (f *Fake) Inspect(_ context.Context, sandbox string) (api.State, error) {
	if err := f.record("Inspect"); err != nil {
		return api.State{}, err
	}
	st, ok := f.States[sandbox]
	if !ok {
		return api.State{Status: api.StatusMissing}, nil
	}
	return st, nil
}

func (f *Fake) setStatus(call, sandbox string, status api.Status) error {
	if err := f.record(call); err != nil {
		return err
	}
	st, ok := f.States[sandbox]
	if !ok {
		return fmt.Errorf("no such container %q", sandbox)
	}
	st.Status = status
	f.States[sandbox] = st
	return nil
}
