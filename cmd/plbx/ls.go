package main

import (
	"context"
	"encoding/json"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/rhizomatous/planterbox/internal/api"
	"github.com/rhizomatous/planterbox/internal/ui"
)

func newLsCmd(g *globals) *cobra.Command {
	var (
		asJSON bool
		quiet  bool
	)

	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list", "ps"},
		Short:   "list sandboxes",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return g.withService(cmd, func(ctx context.Context, svc api.Service) error {
				sandboxes, err := svc.List(ctx)
				if err != nil {
					return err
				}
				return writeSandboxes(cmd, sandboxes, asJSON, quiet)
			})
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "print only sandbox names")
	return cmd
}

// writeSandboxes renders a listing in whichever form was asked for.
func writeSandboxes(cmd *cobra.Command, sandboxes []api.Sandbox, asJSON, quiet bool) error {
	out := cmd.OutOrStdout()
	switch {
	case asJSON:
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		// an empty listing is [], not null: callers pipe this into jq.
		if sandboxes == nil {
			sandboxes = []api.Sandbox{}
		}
		return enc.Encode(sandboxes)
	case quiet:
		for _, sb := range sandboxes {
			if _, err := lipgloss.Fprintln(out, sb.Spec.Name); err != nil {
				return err
			}
		}
		return nil
	default:
		_, err := lipgloss.Fprintln(out, ui.RenderSandboxes(sandboxes, time.Now()))
		return err
	}
}
