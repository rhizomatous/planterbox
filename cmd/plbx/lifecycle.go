package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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

// act resolves a ref, applies fn, and reports the outcome under the sandbox's
// own name — which a by-path ref does not carry, and which reads far better
// than echoing an absolute path back at the user.
func act(
	g *globals, cmd *cobra.Command, args []string,
	verb string, style lipgloss.Style,
	fn func(context.Context, api.Service, api.Ref) error,
) error {
	ref, err := refArg(args)
	if err != nil {
		return err
	}
	return g.withService(cmd, func(ctx context.Context, svc api.Service) error {
		sb, err := svc.Inspect(ctx, ref)
		if err != nil {
			return err
		}
		if err := fn(ctx, svc, api.ByName(sb.Spec.Name)); err != nil {
			return err
		}
		_, err = lipgloss.Fprintln(cmd.OutOrStdout(), style.Render(verb+" ")+ui.Value.Render(sb.Spec.Name))
		return err
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
		Use:   "stop [SANDBOX]",
		Short: "stop a sandbox, keeping everything in it",
		Long: "Stop a running sandbox. Its contents survive: packages, shell history, and " +
			"agent state are all still there when you start it again.\n\n" +
			"Defaults to the sandbox for the current directory.",
		Args: cobra.MaximumNArgs(1),
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
		Use:   "start [SANDBOX]",
		Short: "start a stopped sandbox without attaching",
		Args:  cobra.MaximumNArgs(1),
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
	var force bool

	cmd := &cobra.Command{
		Use:     "rm [SANDBOX]",
		Aliases: []string{"remove"},
		Short:   "delete a sandbox and everything in it",
		Long: "Delete a sandbox, its container, and its home volume. Anything you installed " +
			"inside is gone; your workspace files on the host are untouched.\n\n" +
			"A running sandbox is refused unless --force is given.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return act(g, cmd, args, "removed", ui.Faint,
				func(ctx context.Context, svc api.Service, ref api.Ref) error {
					return svc.Remove(ctx, ref, force)
				})
		},
	}

	cmd.Flags().BoolVarP(&force, "force", "f", false, "remove even while running")
	return cmd
}

func newInspectCmd(g *globals) *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "inspect [SANDBOX]",
		Short: "show a sandbox's full definition and state",
		Args:  cobra.MaximumNArgs(1),
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
		Args:  cobra.MinimumNArgs(2),
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
				res, err := svc.Exec(ctx, api.ByName(args[0]), req, streams)
				if err != nil {
					return err
				}
				if res.ExitCode != 0 {
					return exitCodeError(res.ExitCode)
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

func newAgentsCmd(_ *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "agents",
		Short: "list the agents plbx can run",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := lipgloss.Fprintln(cmd.OutOrStdout(), ui.RenderAgents(api.Agents(), api.DefaultAgent))
			return err
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
