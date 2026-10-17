package tui

import "charm.land/lipgloss/v2"

// ANSI palette only, so the dashboard follows the terminal's own theme.
var (
	styleTitle   = lipgloss.NewStyle().Bold(true)
	styleDim     = lipgloss.NewStyle().Faint(true)
	styleCursor  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	styleOK      = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleBad     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	styleWarn    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleDown    = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	styleUp      = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	styleLatency = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	stylePanel   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("8")).Padding(0, 1)
	styleActive  = stylePanel.BorderForeground(lipgloss.Color("6"))
	styleKeys    = lipgloss.NewStyle().Faint(true)
)
