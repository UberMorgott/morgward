package tui

import "charm.land/lipgloss/v2"

var (
	labelStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("252")) // form labels: brighter than the dim footer for clear hierarchy
	focusStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("213")).Bold(true)
	helpStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))              // bottom control hint: stays dim gray
	tipStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("111")).Italic(true) // contextual toggle help: accent-tinted + italic so it reads as form body, not footer
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	pillStyle   = lipgloss.NewStyle().Padding(0, 1).Background(lipgloss.Color("236"))
	pillOnStyle = lipgloss.NewStyle().Padding(0, 1).Background(lipgloss.Color("57")).Foreground(lipgloss.Color("231")).Bold(true)

	// fileSelStyle highlights the selected file-listing row: the SAME accent background
	// (57) + white foreground (231) the codebase uses for a selected pill (pillOnStyle),
	// applied as a full-width row band (no padding) so the selected entry reads as picked.
	fileSelStyle = lipgloss.NewStyle().Background(lipgloss.Color("57")).Foreground(lipgloss.Color("231"))

	// run-phase box chrome: the rounded border drawn by hand (lipgloss v1.1 has
	// no native border labels), tinted to match the form's accent.
	borderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("57"))

	// framed-input chrome (landing form): a dim rounded border when unfocused (240)
	// and an accent rounded border when focused (57), matching the design spec.
	inputBorderDim   = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	inputBorderFocus = lipgloss.NewStyle().Foreground(lipgloss.Color("57"))

	// monitor footer styles: dim chrome + threshold-colored percent.
	monDimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	monLabelStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	monGreenStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	monYellowStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	monRedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
)
