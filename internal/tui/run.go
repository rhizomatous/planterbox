package tui

import (
	"context"
	"io"

	tea "charm.land/bubbletea/v2"

	"github.com/rhizomatous/planterbox/internal/api"
)

// Options configure a dashboard run. The zero value uses the real terminal.
type Options struct {
	Input  io.Reader
	Output io.Writer
}

// Run opens the dashboard and blocks until the user leaves it.
//
// Attaching exits the dashboard rather than nesting a session inside it: the
// agent wants the whole terminal, and handing it over cleanly is simpler to
// reason about than suspending and restoring the renderer around it. The
// returned request, when non-nil, is the session the caller should run.
func Run(ctx context.Context, svc api.Service, opts Options) (*AttachRequest, error) {
	m := New(svc)

	programOpts := []tea.ProgramOption{tea.WithContext(ctx)}
	if opts.Input != nil {
		programOpts = append(programOpts, tea.WithInput(opts.Input))
	}
	if opts.Output != nil {
		programOpts = append(programOpts, tea.WithOutput(opts.Output))
	}

	final, err := tea.NewProgram(m, programOpts...).Run()
	if err != nil {
		return nil, err
	}
	if done, ok := final.(*Model); ok {
		return done.Attach(), nil
	}
	return nil, nil
}
