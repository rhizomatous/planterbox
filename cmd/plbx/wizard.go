package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/rhizomatous/planterbox/internal/api"
	"github.com/rhizomatous/planterbox/internal/proxy"
	"github.com/rhizomatous/planterbox/internal/ui"
)

// ensurePolicy asks which posture to start from, once, before the first
// sandbox exists. That is the moment the answer starts mattering, and the only
// moment the user is plainly thinking about giving an agent a network.
//
// Asking anywhere else gets it wrong in both directions: a question in front
// of `plbx policy ls` answers something nobody asked, and a sandbox created
// without ever being asked gets a policy by default that the user never saw.
//
// Without a terminal it takes the default rather than failing. A script
// running `plbx run` should not stop on a question nobody is there to answer.
func ensurePolicy(ctx context.Context, cmd *cobra.Command, svc api.Service) error {
	// either a policy is already set, or the store cannot be read. Neither is
	// something to interrupt with a prompt; a store we cannot read is the
	// caller's problem to report.
	if _, err := svc.Policy(ctx); !errors.Is(err, api.ErrNoPolicy) {
		return nil
	}
	_, err := choosePolicy(ctx, cmd, svc)
	return err
}

// choosePolicy asks which posture to start from and stores the answer.
func choosePolicy(ctx context.Context, cmd *cobra.Command, svc api.Service) (proxy.Policy, error) {
	preset := proxy.Default().Preset

	if isTerminal(os.Stdin) {
		var err error
		if preset, err = askPreset(cmd); err != nil {
			return proxy.Policy{}, err
		}
	} else {
		_, _ = lipgloss.Fprintln(cmd.ErrOrStderr(), ui.Faint.Render(
			"no network policy set; starting from the balanced preset. `plbx policy` changes it."))
	}

	p := proxy.New(preset)
	if err := svc.SetPolicy(ctx, p); err != nil {
		return proxy.Policy{}, err
	}
	return p, nil
}

// askPreset runs the chooser.
func askPreset(cmd *cobra.Command) (proxy.Preset, error) {
	options := make([]huh.Option[proxy.Preset], 0, len(proxy.Presets))
	for _, p := range proxy.Presets {
		options = append(options, huh.NewOption(string(p)+" — "+p.Description(), p))
	}

	choice := proxy.Default().Preset
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[proxy.Preset]().
			Title("What should sandboxes be allowed to reach?").
			Description("A sandbox has no way out except through plbx's proxy.\n" +
				"This can be changed at any time with `plbx policy`.").
			Options(options...).
			Value(&choice),
	))

	if err := form.Run(); err != nil {
		return "", fmt.Errorf("choosing a network policy: %w", err)
	}
	_, _ = lipgloss.Fprintln(cmd.ErrOrStderr(),
		ui.OK.Render("network policy ")+ui.Value.Render(string(choice)))
	return choice, nil
}
