// Package ui holds plbx's terminal presentation: a shared logger, a palette,
// and the renderers the CLI prints.
//
// Styling degrades to plain text automatically when output is piped or
// redirected.
package ui

import (
	"os"

	"charm.land/lipgloss/v2"
	"charm.land/log/v2"
)

// Log is the shared stderr logger.
var Log = log.NewWithOptions(os.Stderr, log.Options{ReportTimestamp: false})

// colors chosen to read on both light and dark terminals.
var (
	Title  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("2"))
	Header = lipgloss.NewStyle().Faint(true)
	Value  = lipgloss.NewStyle().Bold(true)
	OK     = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	Warn   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	Bad    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	Faint  = lipgloss.NewStyle().Faint(true)
)
