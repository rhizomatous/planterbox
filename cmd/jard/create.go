package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/rhizomatous/jardiniere/internal/api"
	"github.com/rhizomatous/jardiniere/internal/ui"
)

func newCreateCmd(g *globals) *cobra.Command {
	var flags specFlags

	cmd := &cobra.Command{
		Use:   "create [AGENT] [PATH...]",
		Short: "create a sandbox without starting it",
		Long: "Create a sandbox for the given agent over the given workspaces, without " +
			"starting it. AGENT defaults to " + api.DefaultAgent + ", and the workspace " +
			"defaults to the current directory.\n\n" +
			"The first workspace is the primary: it becomes the sandbox's working " +
			"directory and the path `jard run` reattaches by. Later ones take a :ro suffix " +
			"to mount read-only.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			agent, paths := splitAgentAndPaths(args)
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			spec, err := flags.buildSpec(agent, paths, cwd)
			if err != nil {
				return err
			}
			ports, err := flags.parsePorts()
			if err != nil {
				return err
			}
			return g.withService(cmd, func(ctx context.Context, svc api.Service) error {
				if err := ensurePolicy(ctx, cmd, svc); err != nil {
					return err
				}
				sb, err := svc.Create(ctx, spec)
				if errors.Is(err, api.ErrRemoteNotAdded) {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), ui.Warn.Render(err.Error()))
				} else if err != nil {
					return err
				}
				// ports are not part of the spec, so they are a second call.
				// The sandbox is not running yet, so this only records them.
				if len(ports) > 0 {
					if err := svc.Publish(ctx, api.ByName(sb.Spec.Name), ports); err != nil {
						return err
					}
					sb.Ports = ports
				}
				_, err = lipgloss.Fprintln(cmd.OutOrStdout(), ui.RenderCreated(sb))
				return err
			})
		},
	}

	flags.bind(cmd)
	return cmd
}

// splitAgentAndPaths reads the positional arguments of `create` and `run`.
//
// The first argument is the agent when it names one, and a workspace path
// otherwise, so both `jard run claude .` and `jard run .` work. An argument
// that looks like neither is left to the workspace parser to reject, which
// gives a better error than guessing.
func splitAgentAndPaths(args []string) (agent string, paths []string) {
	if len(args) == 0 {
		return api.DefaultAgent, nil
	}
	if _, err := api.LookupAgent(args[0]); err == nil {
		return args[0], args[1:]
	}
	return api.DefaultAgent, args
}
