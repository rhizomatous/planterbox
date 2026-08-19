package main

import (
	"context"
	"os"

	"github.com/spf13/cobra"

	"github.com/rhizomatous/planterbox/internal/api"
	"github.com/rhizomatous/planterbox/internal/tui"
)

// runDashboard opens the TUI, and keeps reopening it after each session the
// user attaches to, so attaching and coming back is one continuous loop rather
// than a trip back to the shell.
//
// Without a terminal to draw on (piped, redirected, or run from a script) it
// prints the listing instead. `plbx | grep` doing something useful beats it
// failing on a missing TTY.
func runDashboard(cmd *cobra.Command, g *globals) error {
	if !isTerminal(os.Stdin) {
		return g.withService(cmd, func(ctx context.Context, svc api.Service) error {
			sandboxes, err := svc.List(ctx)
			if err != nil {
				return err
			}
			return writeSandboxes(cmd, sandboxes, false, false)
		})
	}

	return g.withService(cmd, func(ctx context.Context, svc api.Service) error {
		for {
			attach, err := tui.Run(ctx, svc, tui.Options{})
			if err != nil {
				return err
			}
			if attach == nil {
				return nil // the user quit
			}
			if err := attachSession(ctx, cmd, svc, attach); err != nil {
				return err
			}
			select {
			case <-ctx.Done():
				// Ctrl-C during the session: leave rather than reopening.
				return nil
			default:
			}
		}
	})
}

// attachSession hands the terminal to a sandbox until the session ends. A
// non-zero exit is the agent's own, and is not worth ending the dashboard over.
func attachSession(ctx context.Context, cmd *cobra.Command, svc api.Service, req *tui.AttachRequest) error {
	if err := startForSession(ctx, cmd, svc, req.Sandbox); err != nil {
		return err
	}
	tty := isTerminal(os.Stdin)
	streams, stop := hostStreams(tty)
	defer stop()

	_, err := svc.Exec(ctx, api.ByName(req.Sandbox), api.ExecRequest{
		Cmd:         req.Cmd,
		Workdir:     req.Workdir,
		Interactive: true,
		TTY:         tty,
	}, streams)
	return err
}
