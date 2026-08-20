package main

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/rhizomatous/planterbox/internal/doctor"
	"github.com/rhizomatous/planterbox/internal/ui"
)

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "check that this installation can run a sandbox",
		Long: "Look at the things plbx needs that are not part of any one sandbox: a " +
			"container runtime that answers, a daemon on the same build as this " +
			"binary, and a state directory it can actually write to.\n\n" +
			"It changes nothing. Anything that fails says what would fix it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			checks := doctor.Run(cmd.Context(), buildVersion())
			_, err := lipgloss.Fprintln(cmd.OutOrStdout(), renderChecks(checks))
			if err != nil {
				return err
			}
			// a failing check is a failing exit status, so this is usable in
			// a setup script rather than only by eye.
			for _, c := range checks {
				if !c.OK {
					return failedChecks{}
				}
			}
			return nil
		},
	}
}

// failedChecks makes a failing doctor a failing command, so it is usable in a
// setup script rather than only by eye.
//
// It carries a Code, which is what stops the CLI printing it: the report has
// already said what is wrong and what fixes it, and an ERROR block underneath
// would be plbx repeating itself in a louder voice.
type failedChecks struct{}

func (failedChecks) Error() string { return "some checks did not pass" }
func (failedChecks) Code() int     { return 1 }

// renderChecks lays the findings out one per line, with the fix under any that
// needs one.
func renderChecks(checks []doctor.Check) string {
	width := 0
	for _, c := range checks {
		if n := lipgloss.Width(c.Name); n > width {
			width = n
		}
	}

	var failed int
	lines := make([]string, 0, len(checks)+2)
	for _, c := range checks {
		mark, style := ui.OK.Render("✓"), ui.Value
		if !c.OK {
			mark, style = ui.Bad.Render("✗"), ui.Warn
			failed++
		}
		lines = append(lines, "  "+mark+" "+style.Render(c.Name+strings.Repeat(" ", width-lipgloss.Width(c.Name)))+"  "+ui.Faint.Render(c.Detail))
		if c.Fix != "" {
			lines = append(lines, "    "+ui.Faint.Render(strings.Repeat(" ", width)+"  try  ")+ui.Value.Render(c.Fix))
		}
	}

	summary := ui.OK.Render("everything checks out")
	if failed > 0 {
		summary = ui.Warn.Render(fmt.Sprintf("%d %s failed", failed, plural(failed, "check", "checks")))
	}
	return strings.Join(lines, "\n") + "\n\n  " + summary
}
