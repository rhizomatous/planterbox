package main

import (
	"runtime"
	"strconv"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/rhizomatous/planterbox/internal/daemon"
	"github.com/rhizomatous/planterbox/internal/ui"
)

func newDaemonCmd(g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "manage the background process that owns your sandboxes",
		Long: "plbx runs a small daemon that owns sandbox state and lifecycle. It starts " +
			"on its own the first time a command needs it, so these exist for when you " +
			"want to start, stop, or look at it deliberately.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(newDaemonStartCmd(g), newDaemonStopCmd(), newDaemonStatusCmd())
	return cmd
}

func newDaemonStartCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "start the daemon, if it isn't already running",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			env := daemon.HostEnv(runtime.GOOS)

			if pid, ok := daemon.Running(ctx, env); ok {
				return report(cmd, ui.Faint, "already running", pid)
			}
			if err := daemon.Start(ctx, env, g.stateDir); err != nil {
				return err
			}
			pid, _ := daemon.Running(ctx, env)
			return report(cmd, ui.OK, "started", pid)
		},
	}
}

func newDaemonStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "stop the daemon, leaving every sandbox as it is",
		Long: "Stop the daemon. Sandboxes are containers in their own right and keep " +
			"running; the next command that needs a daemon starts a new one.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := daemon.Stop(cmd.Context(), daemon.HostEnv(runtime.GOOS)); err != nil {
				return err
			}
			_, err := lipgloss.Fprintln(cmd.OutOrStdout(), ui.Faint.Render("stopped the plbx daemon"))
			return err
		},
	}
}

func newDaemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "report whether the daemon is running, and where",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			env := daemon.HostEnv(runtime.GOOS)

			socket, err := daemon.Socket(env)
			if err != nil {
				return err
			}
			pid, ok := daemon.Running(ctx, env)
			if !ok {
				_, err := lipgloss.Fprintln(cmd.OutOrStdout(),
					ui.Faint.Render("not running")+"\n"+
						ui.Faint.Render("  socket  ")+socket)
				return err
			}
			_, err = lipgloss.Fprintln(cmd.OutOrStdout(),
				ui.OK.Render("running")+"\n"+
					ui.Faint.Render("  pid     ")+ui.Value.Render(pidLabel(pid))+"\n"+
					ui.Faint.Render("  socket  ")+socket)
			return err
		},
	}
}

// report prints a one-line outcome naming the daemon's pid.
func report(cmd *cobra.Command, style lipgloss.Style, verb string, pid int) error {
	line := style.Render(verb) + ui.Faint.Render(" the plbx daemon")
	if pid != 0 {
		line += ui.Faint.Render("  pid " + pidLabel(pid))
	}
	_, err := lipgloss.Fprintln(cmd.OutOrStdout(), line)
	return err
}

// pidLabel renders a pid, or a placeholder when the daemon answers but its
// record is missing.
func pidLabel(pid int) string {
	if pid == 0 {
		return "unknown"
	}
	return strconv.Itoa(pid)
}
