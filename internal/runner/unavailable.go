package runner

import (
	"context"

	"github.com/rhizomatous/jardiniere/internal/api"
)

// unavailable is a Runner standing in for a runtime that could not be found or
// reached. Every operation fails with the detection error, so the user sees why
// rather than a generic exec failure — but jard still starts, and commands that
// only read stored records still work.
type unavailable struct{ err error }

var _ Runner = unavailable{}

// Unavailable returns a Runner that fails every operation with err.
func Unavailable(err error) Runner { return unavailable{err: err} }

func (u unavailable) Create(context.Context, api.Spec) (ID, error)      { return "", u.err }
func (u unavailable) Start(context.Context, ID, string) error           { return u.err }
func (u unavailable) Stop(context.Context, ID) error                    { return u.err }
func (u unavailable) Remove(context.Context, ID, string, bool) error    { return u.err }
func (u unavailable) Publish(context.Context, string, []api.Port) error { return u.err }
func (u unavailable) Unpublish(context.Context, string) error           { return u.err }
func (u unavailable) Copy(context.Context, ID, api.Path, api.Path) error {
	return u.err
}
func (u unavailable) Stats(context.Context, ID) (<-chan api.Stats, error) { return nil, u.err }
func (u unavailable) Inspect(context.Context, ID) (api.State, error)      { return api.State{}, u.err }

func (u unavailable) Exec(context.Context, ID, api.ExecRequest, api.Streams) (api.ExecResult, error) {
	return api.ExecResult{}, u.err
}
