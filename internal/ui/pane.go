package ui

import tea "github.com/charmbracelet/bubbletea"

// Pane is the contract every feature panel implements.
type Pane interface {
	Title() string
	Init() tea.Cmd
	Update(tea.Msg) (Pane, tea.Cmd)
	View() string
	SetSize(width, height int)
	// Help returns a short hint line shown at the bottom of the pane.
	Help() string
}

// Leaver is an optional pane capability: implementers receive an OnLeave call
// when the user navigates away from the pane (sidebar focus, tab switch).
// Used by the themes pane to revert any in-flight live preview. Panes that
// don't care can simply omit this method.
type Leaver interface {
	OnLeave() tea.Cmd
}

// StatusMsg flashes a transient line in the root shell.
type StatusMsg struct {
	Text string
	Kind string // "ok", "warn", "err"
}

func Ok(s string) tea.Cmd   { return func() tea.Msg { return StatusMsg{Text: s, Kind: "ok"} } }
func Warn(s string) tea.Cmd { return func() tea.Msg { return StatusMsg{Text: s, Kind: "warn"} } }
func Errf(s string) tea.Cmd { return func() tea.Msg { return StatusMsg{Text: s, Kind: "err"} } }
