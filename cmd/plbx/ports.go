package main

import (
	"context"
	"fmt"
	"slices"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/rhizomatous/planterbox/internal/api"
	"github.com/rhizomatous/planterbox/internal/ui"
)

func newPortsCmd(g *globals) *cobra.Command {
	var (
		publish   []string
		unpublish []string
	)

	cmd := &cobra.Command{
		Use:   "ports [SANDBOX]",
		Short: "show or change what a sandbox publishes on the host",
		Long: "List the ports a sandbox publishes, or change them.\n\n" +
			"Unlike the settings fixed when a sandbox is created, ports can change at " +
			"any time: a sandbox cannot publish for itself, so its ports are carried by " +
			"a forwarder alongside it. A change takes effect at once on a running " +
			"sandbox, and on next start otherwise.\n\n" +
			"Defaults to the sandbox for the current directory.",
		Example: "  plbx ports\n" +
			"  plbx ports --publish 3000\n" +
			"  plbx ports --publish 8080:80 --publish 5432\n" +
			"  plbx ports api --unpublish 3000",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := refArg(args)
			if err != nil {
				return err
			}
			added, err := parsePorts(publish)
			if err != nil {
				return err
			}
			removed, err := parseHostPorts(unpublish)
			if err != nil {
				return err
			}

			return g.withService(cmd, func(ctx context.Context, svc api.Service) error {
				sb, err := svc.Inspect(ctx, ref)
				if err != nil {
					return err
				}
				if len(added) == 0 && len(removed) == 0 {
					_, err := lipgloss.Fprintln(cmd.OutOrStdout(), ui.RenderPorts(sb))
					return err
				}

				ports := applyPorts(sb.Ports, added, removed)
				if err := svc.Publish(ctx, api.ByName(sb.Spec.Name), ports); err != nil {
					return err
				}
				sb.Ports = ports
				_, err = lipgloss.Fprintln(cmd.OutOrStdout(), ui.RenderPorts(sb))
				return err
			})
		},
	}

	cmd.Flags().StringArrayVarP(&publish, "publish", "p", nil,
		"publish a port, host:sandbox or a bare port (repeatable)")
	cmd.Flags().StringArrayVarP(&unpublish, "unpublish", "u", nil,
		"stop publishing a host port (repeatable)")
	return cmd
}

// parseHostPorts reads --unpublish arguments, which name a host port only:
// that is the side the user sees, and the side that has to be unique.
func parseHostPorts(args []string) ([]int, error) {
	out := make([]int, 0, len(args))
	for _, a := range args {
		p, err := parsePort(a)
		if err != nil {
			return nil, err
		}
		out = append(out, p.Host)
	}
	return out, nil
}

// applyPorts folds additions and removals into the set a sandbox publishes.
//
// A published host port is replaced rather than duplicated, so publishing 8080
// twice with different sandbox ports means the last one, and the order of the
// remaining entries is left alone so the listing does not shuffle.
func applyPorts(current, added []api.Port, removed []int) []api.Port {
	ports := slices.Clone(current)
	for _, add := range added {
		ports = slices.DeleteFunc(ports, func(p api.Port) bool { return p.Host == add.Host })
		ports = append(ports, add)
	}
	for _, host := range removed {
		ports = slices.DeleteFunc(ports, func(p api.Port) bool { return p.Host == host })
	}
	slices.SortStableFunc(ports, func(a, b api.Port) int { return a.Host - b.Host })
	return ports
}

// startedWithoutPorts reports a start that could not publish. The sandbox is
// running either way, so this is said rather than returned: a start that
// reported failure would send the user looking for a sandbox that is right
// there.
func startedWithoutPorts(cmd *cobra.Command, name string, err error) {
	_, _ = fmt.Fprintln(cmd.ErrOrStderr(), ui.Warn.Render(fmt.Sprintf(
		"%s is running, but %v", name, err)))
}
