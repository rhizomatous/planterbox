package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/rhizomatous/jardiniere/internal/api"
	"github.com/rhizomatous/jardiniere/internal/proxy"
	"github.com/rhizomatous/jardiniere/internal/ui"
)

func newPolicyCmd(g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "policy",
		Short: "control what sandboxes are allowed to reach",
		Long: "Egress policy is set here and nowhere else. A sandbox has no route out " +
			"except through jard's proxy, and the proxy answers to this policy — so a " +
			"repository cannot ask for its own permissions.\n\n" +
			"Changes apply to every sandbox from its next connection.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(
		newPolicyLsCmd(g),
		newPolicyAllowCmd(g),
		newPolicyDenyCmd(g),
		newPolicyRmCmd(g),
		newPolicyCheckCmd(g),
		newPolicyLogCmd(g),
		newPolicyPresetCmd(g),
	)
	return cmd
}

func newPolicyLsCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "show the current policy",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return g.withService(cmd, func(ctx context.Context, svc api.Service) error {
				p, err := effectivePolicy(ctx, svc)
				if err != nil {
					return err
				}
				_, err = lipgloss.Fprintln(cmd.OutOrStdout(), ui.RenderPolicy(p))
				return err
			})
		},
	}
}

func newPolicyAllowCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "allow PATTERN...",
		Short: "let sandboxes reach a host",
		Long: "Allow one or more hosts. A pattern is a host, optionally with a port:\n" +
			"  example.com          every port\n" +
			"  example.com:443      that port only\n" +
			"  *.example.com        subdomains, but not example.com itself\n\n" +
			"List both forms when a service uses both.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return editRules(g, cmd, args, true)
		},
	}
}

func newPolicyDenyCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "deny PATTERN...",
		Short: "stop sandboxes reaching a host",
		Long: "Deny one or more hosts. A deny beats any allow that would otherwise " +
			"cover the same host, whichever was added first.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return editRules(g, cmd, args, false)
		},
	}
}

// editRules adds allow or deny rules, replacing any rule already holding the
// same pattern so that `allow` after `deny` means what it looks like.
func editRules(g *globals, cmd *cobra.Command, patterns []string, allow bool) error {
	return g.withService(cmd, func(ctx context.Context, svc api.Service) error {
		p, err := effectivePolicy(ctx, svc)
		if err != nil {
			return err
		}
		for _, pattern := range patterns {
			if err := validPattern(pattern); err != nil {
				return err
			}
			p.Rules = slices.DeleteFunc(p.Rules, func(r proxy.Rule) bool {
				return r.Pattern == pattern
			})
			p.Rules = append(p.Rules, proxy.Rule{Pattern: pattern, Allow: allow})
		}
		if err := svc.SetPolicy(ctx, p); err != nil {
			return err
		}

		verb, style := "denied", ui.Faint
		if allow {
			verb, style = "allowed", ui.OK
		}
		_, err = lipgloss.Fprintln(cmd.OutOrStdout(),
			style.Render(verb+" ")+ui.Value.Render(strings.Join(patterns, " ")))
		return err
	})
}

func newPolicyRmCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "rm PATTERN...",
		Short: "drop a rule, leaving the preset to decide",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return g.withService(cmd, func(ctx context.Context, svc api.Service) error {
				p, err := effectivePolicy(ctx, svc)
				if err != nil {
					return err
				}
				var dropped int
				for _, pattern := range args {
					before := len(p.Rules)
					p.Rules = slices.DeleteFunc(p.Rules, func(r proxy.Rule) bool {
						return r.Pattern == pattern
					})
					dropped += before - len(p.Rules)
				}
				if dropped == 0 {
					return fmt.Errorf("no rule matches %s", strings.Join(args, " "))
				}
				if err := svc.SetPolicy(ctx, p); err != nil {
					return err
				}
				_, err = lipgloss.Fprintln(cmd.OutOrStdout(),
					ui.Faint.Render("dropped ")+ui.Value.Render(strconv.Itoa(dropped))+
						ui.Faint.Render(" "+plural(dropped, "rule", "rules")))
				return err
			})
		},
	}
}

func newPolicyCheckCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "check TARGET",
		Short: "ask what the policy would do, without connecting",
		Long: "Report whether a sandbox could reach TARGET, and which rule decides it. " +
			"Nothing is connected to; this reads the policy only.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return g.withService(cmd, func(ctx context.Context, svc api.Service) error {
				p, err := effectivePolicy(ctx, svc)
				if err != nil {
					return err
				}
				target, err := parsePolicyTarget(args[0])
				if err != nil {
					return err
				}
				// a check that reports a denial still exits clean: it answered
				// the question it was asked.
				_, err = lipgloss.Fprintln(cmd.OutOrStdout(), ui.RenderVerdict(target, p.Check(target)))
				return err
			})
		},
	}
}

func newPolicyLogCmd(g *globals) *cobra.Command {
	var (
		limit  int
		denied bool
	)
	cmd := &cobra.Command{
		Use:   "log",
		Short: "show what sandboxes have been reaching for",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return g.withService(cmd, func(ctx context.Context, svc api.Service) error {
				entries, err := svc.Connections(ctx, 0)
				if err != nil {
					return err
				}
				if denied {
					entries = slices.DeleteFunc(entries, func(e proxy.Entry) bool { return e.Allowed })
				}
				if limit > 0 && len(entries) > limit {
					entries = entries[len(entries)-limit:]
				}
				_, err = lipgloss.Fprintln(cmd.OutOrStdout(), ui.RenderConnections(entries))
				return err
			})
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "n", 50, "how many entries to show")
	cmd.Flags().BoolVar(&denied, "denied", false, "only what was refused")
	return cmd
}

func newPolicyPresetCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "preset [NAME]",
		Short: "show or change the starting posture",
		Long: "Change the preset, keeping the rules layered over it. With no argument, " +
			"reports the current one.\n\n" +
			"Presets: " + presetNames(),
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return g.withService(cmd, func(ctx context.Context, svc api.Service) error {
				p, err := effectivePolicy(ctx, svc)
				if err != nil {
					return err
				}
				if len(args) == 0 {
					_, err := lipgloss.Fprintln(cmd.OutOrStdout(),
						ui.Value.Render(string(p.Preset))+ui.Faint.Render("  "+p.Preset.Description()))
					return err
				}
				preset := proxy.Preset(args[0])
				if !preset.Valid() {
					return fmt.Errorf("unknown preset %q: expected one of %s", args[0], presetNames())
				}
				// the rules stay: they are what was added by hand, and a
				// preset carries its own allowances rather than copying them
				// in, so switching changes the baseline and nothing else.
				p.Preset = preset
				if err := svc.SetPolicy(ctx, p); err != nil {
					return err
				}
				_, err = lipgloss.Fprintln(cmd.OutOrStdout(),
					ui.OK.Render("preset ")+ui.Value.Render(string(preset)))
				return err
			})
		},
	}
}

// effectivePolicy reads the policy that is in force.
//
// A store with nothing in it is not a special case: the default preset is the
// policy until someone says otherwise, and it is what the proxy is enforcing.
// Reporting "none chosen yet" would describe whether a file exists, which is
// jard's business rather than the reader's.
//
// It never writes. Asking what the policy is, or what it would do, should not
// be the thing that decides it — and the answer to `policy allow x` is to
// allow x, not to ask which preset it should be allowed under.
func effectivePolicy(ctx context.Context, svc api.Service) (proxy.Policy, error) {
	p, err := svc.Policy(ctx)
	switch {
	case err == nil:
		return p, nil
	case errors.Is(err, api.ErrNoPolicy):
		return proxy.New(defaultPreset), nil
	default:
		return proxy.Policy{}, err
	}
}

// validPattern rejects a rule that would never match, which is otherwise a
// silent no-op the user only discovers when their traffic is still blocked.
func validPattern(pattern string) error {
	if pattern == "" {
		return errors.New("an empty pattern matches nothing")
	}
	if strings.ContainsAny(pattern, " \t/") {
		return fmt.Errorf("invalid pattern %q: expected a host like example.com or *.example.com", pattern)
	}
	if strings.HasPrefix(pattern, "*") && !strings.HasPrefix(pattern, "*.") && pattern != "*" {
		return fmt.Errorf("invalid pattern %q: a wildcard is written *.example.com", pattern)
	}
	return nil
}

// parsePolicyTarget reads "host" or "host:port", defaulting to https.
func parsePolicyTarget(s string) (proxy.Target, error) {
	host, port, found := strings.Cut(s, ":")
	if !found {
		return proxy.Target{Host: s, Port: 443}, nil
	}
	n, err := strconv.Atoi(port)
	if err != nil || n <= 0 || n > 65535 {
		return proxy.Target{}, fmt.Errorf("invalid port in %q", s)
	}
	return proxy.Target{Host: host, Port: n}, nil
}

func presetNames() string {
	names := make([]string, 0, len(proxy.Presets))
	for _, p := range proxy.Presets {
		names = append(names, string(p))
	}
	return strings.Join(names, ", ")
}
