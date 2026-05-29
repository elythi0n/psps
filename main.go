package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/elythi0n/psps/internal/cli"
	"github.com/elythi0n/psps/internal/kconf"
	"github.com/elythi0n/psps/internal/logfile"
	"github.com/elythi0n/psps/internal/panes/fonts"
	"github.com/elythi0n/psps/internal/panes/keybinds"
	"github.com/elythi0n/psps/internal/panes/sessions"
	"github.com/elythi0n/psps/internal/panes/settings"
	"github.com/elythi0n/psps/internal/panes/themes"
	"github.com/elythi0n/psps/internal/ui"
	"github.com/elythi0n/psps/internal/userconf"
)

type root struct {
	width, height int

	configPath string
	conf       *kconf.Config

	panes        []ui.Pane
	idx          int
	focusSidebar bool

	status      string
	statusKind  string
	statusUntil time.Time

	sparkleFrame int
}

// sparkleGlyphs is the 4-frame symbol pulse rendered in the status bar's
// bottom-right corner. Ambient signal that the TUI is alive.
var sparkleGlyphs = [...]string{"❖", "❇", "✳", "❇"}

// sparkleInterval — how often we advance to the next glyph. Slow enough to be
// calm, not jittery during typing in capture panes.
const sparkleInterval = 800 * time.Millisecond

type sparkleTickMsg struct{}

func sparkleTick() tea.Cmd {
	return tea.Tick(sparkleInterval, func(time.Time) tea.Msg { return sparkleTickMsg{} })
}

func newRoot() (*root, error) {
	path := kconf.Default()
	c, err := kconf.Load(path)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", path, err)
	}
	r := &root{
		configPath: path,
		conf:       c,
		panes: []ui.Pane{
			themes.New(c),
			settings.New(c),
			sessions.New(),
			keybinds.New(c),
			fonts.New(c),
		},
	}
	return r, nil
}

func (r *root) Init() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(r.panes)+1)
	for _, p := range r.panes {
		if c := p.Init(); c != nil {
			cmds = append(cmds, c)
		}
	}
	cmds = append(cmds, sparkleTick())
	return tea.Batch(cmds...)
}

type clearStatusMsg struct{}

func (r *root) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		r.width, r.height = m.Width, m.Height
		r.resizePanes()
	case tea.KeyMsg:
		// Sidebar focus has its own minimal keymap — capture before delegating
		// to the active pane.
		if r.focusSidebar {
			switch m.String() {
			case "ctrl+c", "q":
				return r, tea.Quit
			case "up", "k":
				if r.idx > 0 {
					r.idx--
				}
				return r, nil
			case "down", "j":
				if r.idx < len(r.panes)-1 {
					r.idx++
				}
				return r, nil
			case "right", "l", "enter":
				r.focusSidebar = false
				return r, nil
			case "tab":
				r.idx = (r.idx + 1) % len(r.panes)
				return r, nil
			case "shift+tab":
				r.idx = (r.idx - 1 + len(r.panes)) % len(r.panes)
				return r, nil
			case "1", "2", "3", "4", "5", "6", "7", "8", "9":
				if i := int(m.String()[0] - '1'); i < len(r.panes) {
					r.idx = i
				}
				return r, nil
			}
			return r, nil
		}

		switch m.String() {
		case "ctrl+c", "q":
			if !r.paneCapturingKeys() {
				return r, tea.Quit
			}
		case "left", "h":
			// Leave the pane and focus the sidebar — but only if the pane
			// isn't currently capturing keystrokes (text input, filter mode).
			if !r.paneCapturingKeys() {
				leave := r.leaveActivePane()
				r.focusSidebar = true
				return r, leave
			}
		case "tab":
			if !r.paneCapturingKeys() {
				leave := r.leaveActivePane()
				r.idx = (r.idx + 1) % len(r.panes)
				return r, leave
			}
		case "shift+tab":
			if !r.paneCapturingKeys() {
				leave := r.leaveActivePane()
				r.idx = (r.idx - 1 + len(r.panes)) % len(r.panes)
				return r, leave
			}
		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			if !r.paneCapturingKeys() {
				if i := int(m.String()[0] - '1'); i < len(r.panes) && i != r.idx {
					leave := r.leaveActivePane()
					r.idx = i
					return r, leave
				}
				return r, nil
			}
		}
	case ui.StatusMsg:
		// Persist err/warn status messages to the log so they survive the
		// 3-second flash window. Lets the user investigate failures after
		// the bar has cleared.
		switch m.Kind {
		case "err":
			logfile.Errorf("status: %s (from pane %q)", m.Text, r.panes[r.idx].Title())
		case "warn":
			logfile.Infof("warn: %s (from pane %q)", m.Text, r.panes[r.idx].Title())
		}
		r.status = m.Text
		r.statusKind = m.Kind
		r.statusUntil = time.Now().Add(3 * time.Second)
		return r, tea.Tick(3*time.Second, func(time.Time) tea.Msg { return clearStatusMsg{} })
	case clearStatusMsg:
		if time.Now().After(r.statusUntil) {
			r.status = ""
		}
		return r, nil
	case sparkleTickMsg:
		r.sparkleFrame = (r.sparkleFrame + 1) % len(sparkleGlyphs)
		return r, sparkleTick()
	}

	p, cmd := r.panes[r.idx].Update(msg)
	r.panes[r.idx] = p
	return r, cmd
}

// paneCapturingKeys: panes that take text input set this. We use a sentinel
// type assertion; if no pane signals capture, treat shell shortcuts as active.
func (r *root) paneCapturingKeys() bool {
	type capturer interface{ CapturingKeys() bool }
	if c, ok := r.panes[r.idx].(capturer); ok {
		return c.CapturingKeys()
	}
	return false
}

// leaveActivePane invokes OnLeave on the current pane if it implements the
// ui.Leaver capability. Used to give panes (notably themes, with its hover
// preview) a chance to revert transient state before focus moves elsewhere.
func (r *root) leaveActivePane() tea.Cmd {
	if l, ok := r.panes[r.idx].(ui.Leaver); ok {
		return l.OnLeave()
	}
	return nil
}

// Layout constants. Empirically verified:
//
//	lipgloss .Width(n)/.Height(n) sets the content+padding extent (border is added on top).
//	RoundedBorder adds borderSize in each dimension.
//	Padding(1, 2) = 1 row top+bottom, 2 cols left+right, total 2v / 4h.
//
// Vertical budget (terminal rows):
//
//	title row                    1
//	row containing sidebar/body  r.height - 2
//	status row                   1
//
// Inside the body's content area (after border + padding), we render:
//
//	PaneTitle text + MarginBottom(1)   2 rows
//	pane.View()                        paneH rows
//	"\n" separator                     1 row
//	help text                          1 row
//	total content                      paneH + 4
//
// So paneH = bodyContentH - 4 = (r.height - 2 - border - padV) - 4 = r.height - 10.
const (
	sidebarOuterW  = 22
	borderSize     = 2 // RoundedBorder adds 2 cols and 2 rows
	padH           = 4 // Padding(1, 2) → 2 left + 2 right
	padV           = 2 // Padding(1, 2) → 1 top  + 1 bottom
	titleStatusH   = 2 // top title row + bottom status row
	contentChromeH = 4 // PaneTitle(2) + sep(1) + help(1) inside the content area
)

// clipBlock truncates every line in s to maxW visible cols (ANSI-aware) and
// caps the total line count to maxH. Used to keep pane output within the body
// box budget, since lipgloss's Width()/Height() only set MINIMUM dimensions —
// content that exceeds them wraps or grows, breaking the surrounding layout.
func clipBlock(s string, maxW, maxH int) string {
	if maxW < 1 {
		maxW = 1
	}
	if maxH < 1 {
		maxH = 1
	}
	lines := strings.Split(s, "\n")
	if len(lines) > maxH {
		lines = lines[:maxH]
	}
	for i, ln := range lines {
		if lipgloss.Width(ln) > maxW {
			lines[i] = ansi.Truncate(ln, maxW, "")
		}
	}
	return strings.Join(lines, "\n")
}

func (r *root) resizePanes() {
	rowOuterH := r.height - titleStatusH
	bodyContentW := (r.width - sidebarOuterW) - borderSize - padH
	bodyContentH := rowOuterH - borderSize - padV
	paneH := bodyContentH - contentChromeH
	if bodyContentW < 24 {
		bodyContentW = 24
	}
	if paneH < 6 {
		paneH = 6
	}
	for _, p := range r.panes {
		p.SetSize(bodyContentW, paneH)
	}
}

func (r *root) View() string {
	if r.width == 0 {
		return "loading..."
	}
	title := ui.Title.Render(" psps ") + " " +
		lipgloss.NewStyle().Foreground(lipgloss.Color(ui.Muted)).Render(r.configPath)

	// Sidebar — active item gets a marker; focused state changes the color so
	// it's obvious where keystrokes are going.
	var items []string
	for i, p := range r.panes {
		marker := "  "
		style := ui.SidebarItem
		if i == r.idx {
			marker = "▸ "
			if r.focusSidebar {
				style = ui.SidebarItemActive
			} else {
				style = ui.SidebarItemSelected
			}
		}
		items = append(items, style.Render(marker+p.Title()))
	}
	sidebarStyle := ui.Sidebar
	if r.focusSidebar {
		sidebarStyle = ui.SidebarFocused
	}

	// The Width()/Height() we pass exclude the border (lipgloss adds that
	// outside the value). Sidebar OUTER → sidebarOuterW; body OUTER fills the
	// remainder. See the layout constants at the top of the file.
	rowOuterH := r.height - titleStatusH
	if rowOuterH < 8 {
		rowOuterH = 8
	}
	innerH := rowOuterH - borderSize

	sidebar := sidebarStyle.
		Width(sidebarOuterW - borderSize).
		Height(innerH).
		Render(strings.Join(items, "\n"))

	pane := r.panes[r.idx]
	// PaneTitle has MarginBottom(1), so it provides its own gap to View.
	// The "\n" before help is the single separator between View and help.
	//
	// Pre-clip the inner content to (bodyContentW × bodyContentH). Panes don't
	// always respect their assigned size — a wide row in Settings or a long
	// theme name would otherwise cause lipgloss's Width() to soft-wrap, doubling
	// the line count and forcing the body box past its height budget; that
	// overflow scrolls the alt screen and pushes the title row off the top.
	bodyContentW := (r.width - sidebarOuterW) - borderSize - padH
	bodyContentH := rowOuterH - borderSize - padV
	inner := clipBlock(
		ui.PaneTitle.Render(pane.Title())+pane.View()+
			"\n"+ui.HelpStyle.Render(pane.Help()),
		bodyContentW, bodyContentH,
	)
	body := ui.PaneBox.
		Width(r.width - sidebarOuterW - borderSize).
		Height(innerH).
		Render(inner)

	row := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, body)

	statusLine := ""
	if r.status != "" {
		var sty lipgloss.Style
		switch r.statusKind {
		case "ok":
			sty = ui.OkStyle
		case "warn":
			sty = ui.WarnStyle
		case "err":
			sty = ui.ErrStyle
		}
		statusLine = sty.Render("● ") + r.status
	} else {
		hint := "←/h focus sidebar · tab/⇧tab cycle · 1-5 jump · q quit"
		if r.focusSidebar {
			hint = "↑/↓ select · →/enter open · tab/⇧tab cycle · q quit"
		}
		statusLine = lipgloss.NewStyle().Foreground(lipgloss.Color(ui.Muted)).Render(hint)
	}

	// Pulse a small "❖ psps" tag at the right edge of the status line. Padded
	// with spaces so the gap between the hint text and the tag flexes with the
	// terminal width. lipgloss.Width counts visible cols, so ANSI styling on
	// either side doesn't throw the math off.
	sparkle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ui.Muted)).
		Render(sparkleGlyphs[r.sparkleFrame] + " psps")
	gap := r.width - lipgloss.Width(statusLine) - lipgloss.Width(sparkle)
	if gap < 1 {
		gap = 1
	}
	statusLine = statusLine + strings.Repeat(" ", gap) + sparkle

	return lipgloss.JoinVertical(lipgloss.Left, title, row, statusLine)
}

func main() {
	if len(os.Args) > 1 {
		os.Exit(cli.Run(os.Args[1:]))
	}
	// Logging is opt-in. Resolve in priority order: env var > config file >
	// default off. (The TUI has no flag surface; use the env var for one-off
	// debug sessions, or `log = on` in ~/.config/psps/config to persist.)
	s, _ := userconf.Load(userconf.Default())
	if v := os.Getenv("PSPS_LOG"); v != "" {
		s.LogEnabled = userconf.IsTruthy(v)
	}
	logfile.SetEnabled(s.LogEnabled)

	// Record the env that drives kitty remote-control so failures further
	// down can be correlated against startup conditions. KITTY_LISTEN_ON is
	// the critical one — its absence is the most common cause of session
	// save failures from raw-mode TUIs. No-op when logging is disabled.
	logfile.Infof(
		"psps starting · KITTY_LISTEN_ON=%q · KITTY_PID=%q · TERM=%q",
		os.Getenv("KITTY_LISTEN_ON"),
		os.Getenv("KITTY_PID"),
		os.Getenv("TERM"),
	)
	if p := logfile.Path(); p != "" {
		fmt.Fprintln(os.Stderr, "psps log:", p)
	}
	r, err := newRoot()
	if err != nil {
		logfile.Errorf("fatal at startup: %v", err)
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
	p := tea.NewProgram(r, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		logfile.Errorf("tea program error: %v", err)
		fmt.Fprintln(os.Stderr, "tui error:", err)
		os.Exit(1)
	}
}
