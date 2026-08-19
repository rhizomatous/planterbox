package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/rhizomatous/planterbox/internal/api"
	"github.com/rhizomatous/planterbox/internal/ui"
)

func newRunCmd(g *globals) *cobra.Command {
	var flags specFlags

	cmd := &cobra.Command{
		Use:   "run [AGENT] [PATH...] [-- AGENT_ARGS...]",
		Short: "start an agent in its sandbox, creating it if needed",
		Long: "Run an agent in a sandbox. If no sandbox exists for the workspace, one is " +
			"created; otherwise the existing one is reattached, with everything you " +
			"installed last time still in place.\n\n" +
			agentList() + "\n\n" +
			"Reattachment is by workspace path, so running plbx again in the same " +
			"directory finds the same sandbox. Use --name to reattach from anywhere.\n\n" +
			"Everything after -- is passed to the agent verbatim.",
		Example: "  plbx run\n" +
			"  plbx run codex\n" +
			"  plbx run --name myrepo\n" +
			"  plbx run claude . ~/src/docs:ro\n" +
			"  plbx run claude -- --continue",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// everything after -- goes to the agent. ArgsLenAtDash is -1 when
			// there is no --, so it has to be checked before it is used to slice.
			var passthrough []string
			if dash := cmd.ArgsLenAtDash(); dash >= 0 {
				passthrough, args = args[dash:], args[:dash]
			}

			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			return g.withService(cmd, func(ctx context.Context, svc api.Service) error {
				return runAgent(ctx, cmd, svc, &flags, args, passthrough, cwd)
			})
		},
	}

	flags.bind(cmd)
	return cmd
}

// runAgent resolves or creates the sandbox, makes sure it is running, and hands
// the terminal to the agent.
func runAgent(
	ctx context.Context, cmd *cobra.Command, svc api.Service,
	flags *specFlags, args, passthrough []string, cwd string,
) error {
	agent, paths := splitAgentAndPaths(args)

	// asked before the sandbox exists, so the answer is in force the first
	// time it reaches for anything rather than one run later.
	if err := ensurePolicy(ctx, cmd, svc); err != nil {
		return err
	}

	sb, created, err := resolveOrCreate(ctx, cmd, svc, flags, agent, paths, cwd)
	if err != nil {
		return err
	}
	if !created {
		warnIgnoredFlags(cmd, flags, sb)
	}

	if sb.State.Status != api.StatusRunning {
		switch err := svc.Start(ctx, api.ByName(sb.Spec.Name)); {
		case errors.Is(err, api.ErrPortsUnavailable):
			startedWithoutPorts(cmd, sb.Spec.Name, err)
		case err != nil:
			return err
		}
	}

	// the sandbox's own agent wins over the one named on the command line: the
	// image was chosen at create time and is what actually has a binary.
	def, err := api.LookupAgent(sb.Spec.Agent)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(cmd.ErrOrStderr(), ui.RenderAttaching(sb, created))

	tty := isTerminal(os.Stdin)
	streams, stop := hostStreams(tty)
	defer stop()

	res, err := svc.Exec(ctx, api.ByName(sb.Spec.Name), api.ExecRequest{
		Cmd:         append(append([]string{}, def.Command...), passthrough...),
		Workdir:     sb.Spec.Workdir(),
		Interactive: true,
		TTY:         tty,
	}, streams)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return exitCodeError{what: "the agent", code: res.ExitCode}
	}
	return nil
}

// resolveOrCreate finds the sandbox this invocation refers to, creating it when
// none exists. It reports whether it created one.
func resolveOrCreate(
	ctx context.Context, cmd *cobra.Command, svc api.Service,
	flags *specFlags, agent string, paths []string, cwd string,
) (api.Sandbox, bool, error) {
	ref, err := runRef(flags, paths, cwd)
	if err != nil {
		return api.Sandbox{}, false, err
	}

	sb, err := svc.Inspect(ctx, ref)
	switch {
	case err == nil:
		return sb, false, nil
	case !errors.Is(err, api.ErrNotFound):
		return api.Sandbox{}, false, err
	}

	// nothing here yet: build one for this workspace.
	spec, err := flags.buildSpec(agent, paths, cwd)
	if err != nil {
		return api.Sandbox{}, false, err
	}
	ports, err := flags.parseSpecPorts()
	if err != nil {
		return api.Sandbox{}, false, err
	}
	// a first run for an agent is a multi-gigabyte download, and this is the
	// only place it has to say so.
	if err := fetchImage(ctx, cmd, svc, spec.Image); err != nil {
		return api.Sandbox{}, false, err
	}
	sb, err = svc.Create(ctx, spec)
	if errors.Is(err, api.ErrRemoteNotAdded) {
		// the sandbox is made either way; what failed is the remote that
		// fetches from it, and saying nothing leaves the user to find out
		// when `git fetch` cannot resolve it.
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), ui.Warn.Render(err.Error()))
	} else if err != nil {
		return api.Sandbox{}, false, err
	}
	if len(ports) > 0 {
		if err := svc.Publish(ctx, api.ByName(sb.Spec.Name), ports); err != nil {
			return api.Sandbox{}, false, err
		}
		sb.Ports = ports
	}
	return sb, true, nil
}

// runRef decides what `run` is pointing at: an explicit --name, else the
// primary workspace path, which is what makes reattach-by-path work.
func runRef(flags *specFlags, paths []string, cwd string) (api.Ref, error) {
	if flags.name != "" {
		return api.ByName(flags.name), nil
	}
	primary := cwd
	if len(paths) > 0 {
		ws, err := parseWorkspace(paths[0], cwd)
		if err != nil {
			return api.Ref{}, err
		}
		primary = ws.Host
	}
	return api.ByPath(primary), nil
}

// warnIgnoredFlags tells the user when create-time settings were given for a
// sandbox that already exists. They are fixed at create time, so silently
// dropping them would leave the user believing a limit was applied.
func warnIgnoredFlags(cmd *cobra.Command, flags *specFlags, sb api.Sandbox) {
	// --publish is not fixed at create time any more: ports live beside the
	// sandbox rather than in its container, so they have a command of their own.
	if cmd.Flags().Changed("publish") {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), ui.Warn.Render(fmt.Sprintf(
			"--publish was ignored: %s already exists. Use `plbx ports` to change what it publishes",
			sb.Spec.Name)))
	}

	set := flags.changed(cmd)
	// --name selects the sandbox rather than configuring it.
	set = slices.DeleteFunc(set, func(f string) bool { return f == "--name" })
	if len(set) == 0 {
		return
	}
	_, _ = fmt.Fprintln(cmd.ErrOrStderr(), ui.Warn.Render(fmt.Sprintf(
		"%s %s fixed when %s was created; recreate the sandbox to change %s",
		strings.Join(set, ", "),
		plural(len(set), "was", "were"),
		sb.Spec.Name,
		plural(len(set), "it", "them"),
	)))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// isTerminal reports whether f is a real terminal. Asking a runtime for a TTY
// when there isn't one makes it refuse outright.
//
// A character-device check is not enough: /dev/null is one, so a command run
// with stdin redirected from it would claim a terminal it does not have.
func isTerminal(f *os.File) bool {
	return term.IsTerminal(f.Fd())
}

// exitCodeError carries a non-zero exit status back to main without printing
// anything: whatever ran has already said what it wanted to. It names what
// exited, because `run` hosts an agent and `exec` hosts whatever was typed.
type exitCodeError struct {
	what string
	code int
}

func (e exitCodeError) Error() string {
	return fmt.Sprintf("%s exited with status %d", e.what, e.code)
}

// Code reports the status to exit with.
func (e exitCodeError) Code() int { return e.code }
