// Package ui carries shared lipgloss styles. Palette is Catppuccin Mocha to
// match the user's kitty config; tweak in one place to reskin everything.
package ui

import "github.com/charmbracelet/lipgloss"

const (
	Base     = "#1E1E2E"
	Surface  = "#313244"
	Overlay  = "#45475A"
	Text     = "#CDD6F4"
	Subtext  = "#A6ADC8"
	Muted    = "#7F849C"
	Blue     = "#89B4FA"
	Mauve    = "#CBA6F7"
	Green    = "#A6E3A1"
	Yellow   = "#F9E2AF"
	Red      = "#F38BA8"
	Teal     = "#94E2D5"
	Peach    = "#FAB387"
	Rosewater = "#F5E0DC"
)

var (
	Title = lipgloss.NewStyle().
		Foreground(lipgloss.Color(Base)).
		Background(lipgloss.Color(Mauve)).
		Bold(true).
		Padding(0, 2)

	Sidebar = lipgloss.NewStyle().
		Foreground(lipgloss.Color(Text)).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(Overlay)).
		Padding(1, 2)

	// SidebarFocused: border picks up the accent color so it's obvious
	// keystrokes are landing in the sidebar.
	SidebarFocused = lipgloss.NewStyle().
		Foreground(lipgloss.Color(Text)).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(Mauve)).
		Padding(1, 2)

	SidebarItem = lipgloss.NewStyle().
		Foreground(lipgloss.Color(Subtext)).
		Padding(0, 1)

	// SidebarItemSelected: current pane while sidebar is NOT focused
	// — dimmer highlight than SidebarItemActive.
	SidebarItemSelected = lipgloss.NewStyle().
		Foreground(lipgloss.Color(Text)).
		Background(lipgloss.Color(Surface)).
		Padding(0, 1)

	// SidebarItemActive: current pane while the sidebar HAS focus.
	SidebarItemActive = lipgloss.NewStyle().
		Foreground(lipgloss.Color(Base)).
		Background(lipgloss.Color(Mauve)).
		Bold(true).
		Padding(0, 1)

	PaneBox = lipgloss.NewStyle().
		Foreground(lipgloss.Color(Text)).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(Overlay)).
		Padding(1, 2)

	PaneTitle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(Mauve)).
		Bold(true).
		MarginBottom(1)

	HelpStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(Muted))

	OkStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color(Green)).Bold(true)
	WarnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(Yellow)).Bold(true)
	ErrStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color(Red)).Bold(true)

	Hl     = lipgloss.NewStyle().Foreground(lipgloss.Color(Blue))
	Accent = lipgloss.NewStyle().Foreground(lipgloss.Color(Peach))
)
