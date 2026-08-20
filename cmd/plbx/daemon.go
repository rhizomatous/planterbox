package main

import (
	"runtime"
	"strconv"
	"strings"
	"time"

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
	cmd.AddCommand(newDaemonStartCmd(g), newDaemonStopCmd(), newDaemonRestartCmd(g), newDaemonStatusCmd())
	return cmd
}

func newDaemonStartCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "start the daemon, if it isn't already running",
		Long: "Start the daemon, and report its process id. One already running is " +
			"left alone.\n\n" +
			"You rarely need this. Any command that needs a daemon starts one.",
		Args: cobra.NoArgs,
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

func newDaemonRestartCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "restart the daemon, which is what picks up an upgrade",
		Long: "Stop the daemon and start a new one. Sandboxes are containers in their " +
			"own right and keep running throughout.\n\n" +
			"This is what picks up an upgrade: plbx starts the daemon on demand, and " +
			"the one already running stays on the build it was started from until " +
			"something replaces it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			env := daemon.HostEnv(runtime.GOOS)

			if _, ok := daemon.Running(ctx, env); ok {
				if err := daemon.Stop(ctx, env); err != nil {
					return err
				}
			}
			if err := daemon.Start(ctx, env, g.stateDir); err != nil {
				return err
			}
			pid, _ := daemon.Running(ctx, env)
			return report(cmd, ui.OK, "restarted", pid)
		},
	}
}

// daemonVersionLabel names the build a daemon reports, or says it could not
// say. Only a daemon predating the call can fail to answer, so that silence is
// itself an answer about its age.
func daemonVersionLabel(v string) string {
	if v == "" {
		return "unknown (too old to say)"
	}
	return v
}

// versionSkew reports the mismatch between this plbx and the daemon answering
// it, if there is one.
//
// plbx autostarts plbxd and the daemon then outlives the upgrade that replaced
// it, so a CLI talking to the previous build is the ordinary result of
// installing a new version rather than an exotic failure. Nothing else about
// it looks wrong: commands work, and answer as the old build would.
func versionSkew(daemonVersion string) string {
	cli := buildVersion()
	if daemonVersion == cli {
		return ""
	}
	if daemonVersion == "" {
		return "this daemon predates plbx " + cli + " and is answering as its own build does. " +
			"plbx daemon restart picks up the current one."
	}
	return "plbx is " + cli + " and the daemon is " + daemonVersion +
		". They will disagree wherever the two builds do. plbx daemon restart fixes it."
}

func newDaemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "report whether the daemon is running, and where",
		Long: "Report whether a daemon is running, and if so its build, process id, " +
			"and uptime. The socket and log path are printed either way, so there is " +
			"somewhere to look when it is not running.\n\n" +
			"A daemon on a different build from this binary is called out here: plbx " +
			"starts one on demand, and it keeps the build it was started from until " +
			"`plbx daemon restart` replaces it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			env := daemon.HostEnv(runtime.GOOS)

			socket, err := daemon.Socket(env)
			if err != nil {
				return err
			}
			logPath, _ := daemon.LogPath(env)

			pid, ok := daemon.Running(ctx, env)
			if !ok {
				_, err := lipgloss.Fprintln(cmd.OutOrStdout(),
					ui.Faint.Render("not running")+"\n"+
						ui.Faint.Render("  socket  ")+socket+"\n"+
						ui.Faint.Render("  log     ")+logPath)
				return err
			}

			lines := []string{ui.OK.Render("running")}
			info, _ := daemon.Info(ctx, env)
			// a daemon too old to answer reports nothing, which is itself the
			// answer: it is not this build.
			lines = append(lines,
				ui.Faint.Render("  version ")+ui.Value.Render(daemonVersionLabel(info.Version)),
				ui.Faint.Render("  pid     ")+ui.Value.Render(pidLabel(pid)))
			if !info.StartedAt.IsZero() {
				lines = append(lines, ui.Faint.Render("  uptime  ")+ui.Uptime(info.StartedAt, time.Now()))
			}
			lines = append(lines,
				ui.Faint.Render("  socket  ")+socket,
				ui.Faint.Render("  log     ")+logPath)
			if warning := versionSkew(info.Version); warning != "" {
				lines = append(lines, "", ui.Warn.Render(warning))
			}
			_, err = lipgloss.Fprintln(cmd.OutOrStdout(), strings.Join(lines, "\n"))
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
