package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/rhizomatous/planterbox/internal/api"
	"github.com/rhizomatous/planterbox/internal/ui"
)

// refArg turns an optional positional sandbox name into a Ref, falling back to
// the current directory so `plbx stop` works from inside a workspace the same
// way `plbx run` does.
func refArg(args []string) (api.Ref, error) {
	if len(args) > 0 {
		return api.ByName(args[0]), nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return api.Ref{}, err
	}
	return api.ByPath(cwd), nil
}

// refArgs is refArg for the commands that accept several sandboxes. No names
// still means the one for the current directory, so `plbx stop` on its own is
// unchanged.
func refArgs(args []string) ([]api.Ref, error) {
	if len(args) == 0 {
		ref, err := refArg(nil)
		if err != nil {
			return nil, err
		}
		return []api.Ref{ref}, nil
	}
	refs := make([]api.Ref, len(args))
	for i, name := range args {
		refs[i] = api.ByName(name)
	}
	return refs, nil
}

// act resolves a ref, applies fn, and reports the outcome under the sandbox's
// own name — which a by-path ref does not carry, and which reads far better
// than echoing an absolute path back at the user.
func act(
	g *globals, cmd *cobra.Command, args []string,
	verb string, style lipgloss.Style,
	fn func(context.Context, api.Service, api.Ref) error,
) error {
	refs, err := refArgs(args)
	if err != nil {
		return err
	}
	return g.withService(cmd, func(ctx context.Context, svc api.Service) error {
		// one bad name does not cancel the rest: asking for four sandboxes
		// and getting none because the third was a typo is worse than being
		// told which one it was.
		var errs []error
		for _, ref := range refs {
			sb, err := svc.Inspect(ctx, ref)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if err := fn(ctx, svc, api.ByName(sb.Spec.Name)); err != nil {
				errs = append(errs, err)
				continue
			}
			if _, err := lipgloss.Fprintln(cmd.OutOrStdout(),
				style.Render(verb+" ")+ui.Value.Render(sb.Spec.Name)); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	})
}

// execCommand reads the command out of `exec`'s positional arguments.
//
// A leading "--" is dropped. Separating a command from the flags in front of
// it is what "--" is for, `docker exec` and `kubectl exec` both take it, and
// anyone who has typed either will type it here — but it arrives as an
// ordinary argument, so without this the runtime is asked to run a program
// called "--" and says it cannot find one.
func execCommand(args []string) ([]string, error) {
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		return nil, errors.New("no command given")
	}
	return args, nil
}

func newStopCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "stop [SANDBOX...]",
		Short: "stop a sandbox, keeping everything in it",
		Long: "Stop a running sandbox. Its contents survive: packages, shell history, and " +
			"agent state are all still there when you start it again.\n\n" +
			"Defaults to the sandbox for the current directory.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return act(g, cmd, args, "stopped", ui.Faint,
				func(ctx context.Context, svc api.Service, ref api.Ref) error {
					return svc.Stop(ctx, ref)
				})
		},
	}
}

func newStartCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "start [SANDBOX...]",
		Short: "start a stopped sandbox without attaching",
		Long: "Start a sandbox and leave it running, without handing it your terminal. " +
			"Use it to bring a sandbox up for `plbx exec`, for ssh, or for the ports it " +
			"publishes.\n\n" +
			"`plbx run` starts a sandbox too, and attaches the agent to it. This is the " +
			"same thing without the second half.\n\n" +
			"Defaults to the sandbox for the current directory.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return act(g, cmd, args, "started", ui.OK,
				func(ctx context.Context, svc api.Service, ref api.Ref) error {
					err := svc.Start(ctx, ref)
					if errors.Is(err, api.ErrPortsUnavailable) {
						startedWithoutPorts(cmd, ref.Name, err)
						return nil
					}
					return err
				})
		},
	}
}

func newRmCmd(g *globals) *cobra.Command {
	var force, all bool

	cmd := &cobra.Command{
		Use:     "rm [SANDBOX...]",
		Aliases: []string{"remove", "delete"},
		Short:   "delete a sandbox and everything in it",
		Long: "Delete a sandbox, its container, and its home volume. Anything you installed " +
			"inside is gone; your workspace files on the host are untouched.\n\n" +
			"You are asked to confirm, and a running sandbox is refused outright unless " +
			"--force is given. --force also answers the question, which is what a script " +
			"wants: without a terminal there is nobody to ask, so plbx refuses instead " +
			"of assuming.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if all && len(args) > 0 {
				return errors.New("--all removes every sandbox, so it takes no names")
			}
			return g.withService(cmd, func(ctx context.Context, svc api.Service) error {
				targets, err := removalTargets(ctx, svc, args, all)
				if err != nil || len(targets) == 0 {
					return err
				}
				if !force {
					// a running sandbox is refused outright, and it is refused
					// before the question rather than after it.
					for _, sb := range targets {
						if sb.State.Status == api.StatusRunning {
							return fmt.Errorf("%w: %q (use --force)", api.ErrRunning, sb.Spec.Name)
						}
					}
					names := make([]string, len(targets))
					for i, sb := range targets {
						names[i] = sb.Spec.Name
					}
					ok, err := confirmRemove(cmd.OutOrStdout(), cmd.InOrStdin(), isTerminal(os.Stdin), names)
					if err != nil {
						return err
					}
					if !ok {
						_, err := lipgloss.Fprintln(cmd.OutOrStdout(),
							ui.Faint.Render("left ")+ui.Value.Render(strings.Join(names, ", "))+ui.Faint.Render(" alone"))
						return err
					}
				}
				var errs []error
				for _, sb := range targets {
					if err := svc.Remove(ctx, api.ByName(sb.Spec.Name), force); err != nil {
						errs = append(errs, err)
						continue
					}
					if _, err := lipgloss.Fprintln(cmd.OutOrStdout(),
						ui.Faint.Render("removed ")+ui.Value.Render(sb.Spec.Name)); err != nil {
						errs = append(errs, err)
					}
				}
				return errors.Join(errs...)
			})
		},
	}

	cmd.Flags().BoolVarP(&force, "force", "f", false, "remove even while running")
	cmd.Flags().BoolVar(&all, "all", false, "remove every sandbox")
	return cmd
}

// removalTargets resolves what a removal is about to act on, so the question
// can name all of it at once rather than once per sandbox.
func removalTargets(ctx context.Context, svc api.Service, args []string, all bool) ([]api.Sandbox, error) {
	if all {
		return svc.List(ctx)
	}
	refs, err := refArgs(args)
	if err != nil {
		return nil, err
	}
	targets := make([]api.Sandbox, 0, len(refs))
	for _, ref := range refs {
		sb, err := svc.Inspect(ctx, ref)
		if err != nil {
			return nil, err
		}
		targets = append(targets, sb)
	}
	return targets, nil
}

// confirmRemove asks before a removal discards a sandbox's home volume, which
// is everything ever installed in it. The workspace is untouched either way,
// so the question is only ever about the part that cannot be got back.
//
// Off a terminal there is nobody to ask, so it refuses rather than assuming:
// --force is how a script says yes deliberately.
func confirmRemove(out io.Writer, in io.Reader, interactive bool, names []string) (bool, error) {
	what := strings.Join(names, ", ")
	if !interactive {
		return false, fmt.Errorf(
			"removing %s discards everything installed in it, and there is no terminal to confirm on (use --force)", what)
	}
	if _, err := fmt.Fprintf(out, "remove %s and everything installed in it? [y/N] ", what); err != nil {
		return false, err
	}
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return false, nil // EOF with nothing typed reads as no
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

func newInspectCmd(g *globals) *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "inspect [SANDBOX]",
		Short: "show a sandbox's full definition and state",
		Long: "Show what a sandbox was built from and what it is doing now: its agent, " +
			"image, workspaces, and the ports it publishes.\n\n" +
			"Most of this is fixed when the sandbox is created and cannot be changed " +
			"afterwards — ports are the exception. --json prints the same record " +
			"unformatted.\n\n" +
			"Defaults to the sandbox for the current directory.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := refArg(args)
			if err != nil {
				return err
			}
			return g.withService(cmd, func(ctx context.Context, svc api.Service) error {
				sb, err := svc.Inspect(ctx, ref)
				if err != nil {
					return err
				}
				if asJSON {
					enc := json.NewEncoder(cmd.OutOrStdout())
					enc.SetIndent("", "  ")
					return enc.Encode(sb)
				}
				_, err = lipgloss.Fprintln(cmd.OutOrStdout(), ui.RenderSandbox(sb, time.Now()))
				return err
			})
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON")
	return cmd
}

func newExecCmd(g *globals) *cobra.Command {
	var (
		workdir string
		user    string
		env     []string
		noTTY   bool
	)

	cmd := &cobra.Command{
		Use:   "exec SANDBOX COMMAND [ARGS...]",
		Short: "run a command inside a sandbox",
		Long: "Run one command inside a running sandbox and exit with its status. The " +
			"sandbox has to be running: `plbx start` brings one up.\n\n" +
			"A terminal is allocated when yours is one, so `plbx exec box bash` gives " +
			"you an interactive shell and a piped command still reads its stdin to EOF. " +
			"--no-tty forces it off.\n\n" +
			"Flags go before the sandbox name. Everything after it belongs to the " +
			"command, so its own flags arrive intact.",
		Example: "  plbx exec myrepo bash\n" +
			"  plbx exec myrepo bash -lc 'npm test'\n" +
			"  plbx exec -u root myrepo apt-get update\n" +
			"  plbx exec -w /tmp myrepo pwd",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			command, err := execCommand(args[1:])
			if err != nil {
				return err
			}
			req := api.ExecRequest{
				Cmd:         command,
				Workdir:     workdir,
				User:        user,
				Interactive: true,
				TTY:         !noTTY && isTerminal(os.Stdin),
			}
			for _, e := range env {
				k, v, ok := cutEnv(e)
				if !ok {
					return fmt.Errorf("env %q: expected NAME=VALUE", e)
				}
				if req.Env == nil {
					req.Env = map[string]string{}
				}
				req.Env[k] = v
			}
			streams, stop := hostStreams(req.TTY)
			defer stop()

			return g.withService(cmd, func(ctx context.Context, svc api.Service) error {
				// exec runs against a live container, so a stopped sandbox
				// would otherwise surface as the runtime's own complaint
				// wearing an exit status that is not the command's.
				sb, err := svc.Inspect(ctx, api.ByName(args[0]))
				if err != nil {
					return err
				}
				if sb.State.Status != api.StatusRunning {
					return fmt.Errorf("sandbox is not running: %q (start it with plbx start)", sb.Spec.Name)
				}
				res, err := svc.Exec(ctx, api.ByName(args[0]), req, streams)
				if err != nil {
					return err
				}
				if res.ExitCode != 0 {
					return exitCodeError{what: "the command", code: res.ExitCode}
				}
				return nil
			})
		},
	}

	// stop parsing flags at the first positional, or `plbx exec box bash -lc ...`
	// has its -lc read as plbx's own. Flags go before the sandbox name, exactly
	// as they do for `docker exec`.
	cmd.Flags().SetInterspersed(false)

	fl := cmd.Flags()
	fl.StringVarP(&workdir, "workdir", "w", "", "working directory inside the sandbox")
	fl.StringVarP(&user, "user", "u", "", "user to run as")
	fl.StringArrayVarP(&env, "env", "e", nil, "environment variable, NAME=VALUE (repeatable)")
	fl.BoolVar(&noTTY, "no-tty", false, "do not allocate a terminal")
	return cmd
}

func newCpCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "cp SRC DST",
		Short: "copy files between the host and a sandbox",
		Long: "Copy files in or out of a sandbox. Exactly one of SRC and DST names a " +
			"sandbox, written SANDBOX:/path; the other is a host path.\n\n" +
			"A host path that would otherwise look like a sandbox reference can be " +
			"written ./like-this.",
		Example: "  plbx cp ./config.json myrepo:/home/agent/\n" +
			"  plbx cp myrepo:/home/agent/notes.md ./notes.md\n" +
			"  plbx cp ./src myrepo:/home/agent/src\n" +
			"  plbx cp ./myrepo:weird-name myrepo:/home/agent/",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := parseCopyPath(args[0])
			if err != nil {
				return err
			}
			dst, err := parseCopyPath(args[1])
			if err != nil {
				return err
			}
			return g.withService(cmd, func(ctx context.Context, svc api.Service) error {
				return svc.Copy(ctx, src, dst)
			})
		},
	}
}

// cutEnv splits NAME=VALUE, accepting an empty value but not an empty name.
func cutEnv(s string) (name, value string, ok bool) {
	for i := range len(s) {
		if s[i] == '=' {
			if i == 0 {
				return "", "", false
			}
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}
