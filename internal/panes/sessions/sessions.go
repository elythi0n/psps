package sessions

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/elythi0n/psps/internal/sessionlib"
	"github.com/elythi0n/psps/internal/ui"
)

type item struct{ s sessionlib.Session }

func (i item) FilterValue() string { return i.s.Name }
func (i item) Title() string       { return i.s.Name }
func (i item) Description() string {
	return i.s.Mod.Format("2006-01-02 15:04") + "  ·  " +
		humanSize(len(i.s.Body))
}

func humanSize(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f KiB", float64(n)/1024)
}

type mode int

const (
	modeList mode = iota
	modeNaming
)

// Result messages from background shell-outs. `kitty @ ls` can block on the
// remote-control socket, so Dump/Save must run off the Update goroutine —
// otherwise the whole TUI freezes for the duration of the exec.
type dumpResultMsg struct {
	body string
	err  error
	// tried is "socket" when we used the env-driven --to socket path (fast,
	// non-disruptive) or "suspend" when we released the TTY via tea.ExecProcess.
	// The Update handler uses this to fall back from socket → suspend on
	// failure, but never the other way around (suspend is the last resort).
	tried string
}

type saveResultMsg struct {
	name  string
	err   error
	tried string // "socket" or "suspend" — same fallback semantics as dumpResultMsg
}

// dumpCmd runs `kitty @ ls` in a goroutine over the socket. Only useful when
// $KITTY_LISTEN_ON is set — otherwise kitty's CLI falls back to TTY-IPC which
// races against bubbletea's raw-mode terminal ownership and times out.
func dumpCmd() tea.Cmd {
	return func() tea.Msg {
		body, err := sessionlib.Dump()
		return dumpResultMsg{body: body, err: err, tried: "socket"}
	}
}

// dumpSuspendCmd is the fallback: ask bubbletea to suspend the program so the
// kitty CLI can talk to its parent kitty over the TTY without contention. The
// TUI briefly flickers but the call is reliable even without `listen_on` set
// in kitty.conf.
func dumpSuspendCmd() tea.Cmd {
	var stdout bytes.Buffer
	cmd := sessionlib.MakeKittyLsCmd()
	cmd.Stdout = &stdout
	// Leave Stdin/Stderr unset; bubbletea wires them to the real TTY so the
	// kitty CLI can do its IPC dance.
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		if err != nil {
			return dumpResultMsg{err: err, tried: "suspend"}
		}
		body, perr := sessionlib.ParseKittyLs(stdout.Bytes())
		return dumpResultMsg{body: body, err: perr, tried: "suspend"}
	})
}

func saveCmd(name string) tea.Cmd {
	return func() tea.Msg {
		_, err := sessionlib.Save(name)
		return saveResultMsg{name: name, err: err, tried: "socket"}
	}
}

// saveSuspendCmd is the suspend-path counterpart of saveCmd. Captures the
// kitty @ ls output via tea.ExecProcess, then writes the parsed body to disk
// under the given name. Used when KITTY_LISTEN_ON is unset.
func saveSuspendCmd(name string) tea.Cmd {
	var stdout bytes.Buffer
	cmd := sessionlib.MakeKittyLsCmd()
	cmd.Stdout = &stdout
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		if err != nil {
			return saveResultMsg{name: name, err: err}
		}
		body, perr := sessionlib.ParseKittyLs(stdout.Bytes())
		if perr != nil {
			return saveResultMsg{name: name, err: perr}
		}
		if _, werr := sessionlib.WriteBody(name, body); werr != nil {
			return saveResultMsg{name: name, err: werr, tried: "suspend"}
		}
		return saveResultMsg{name: name, tried: "suspend"}
	})
}

// hasKittySocket reports whether we have a socket path to talk to kitty on.
// When false, we should skip the goroutine path entirely and go straight to
// the bubbletea-suspend path.
func hasKittySocket() bool {
	return os.Getenv("KITTY_LISTEN_ON") != ""
}

type Model struct {
	list    list.Model
	mode    mode
	name    textinput.Model
	pending string
	w, h    int
	// busy is true while a `kitty @ ls` shell-out is in flight. Without this
	// guard, rapid `s`/`a` presses fire concurrent goroutines that race on
	// kitty's remote-control socket and return out of order — the user sees
	// stale errors landing after a successful save.
	busy bool
}

func New() *Model {
	delegate := list.NewDefaultDelegate()
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.
		Foreground(lipgloss.Color(ui.Mauve)).
		BorderForeground(lipgloss.Color(ui.Mauve))
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.
		Foreground(lipgloss.Color(ui.Subtext)).
		BorderForeground(lipgloss.Color(ui.Mauve))

	l := list.New(nil, delegate, 0, 0)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetFilteringEnabled(true)

	ti := textinput.New()
	ti.Placeholder = "session-name"
	ti.Prompt = "name: "
	ti.CharLimit = 60

	m := &Model{list: l, name: ti}
	m.reload()
	return m
}

func (m *Model) reload() {
	sess, _ := sessionlib.List()
	items := make([]list.Item, 0, len(sess))
	for _, s := range sess {
		items = append(items, item{s})
	}
	m.list.SetItems(items)
}

func (m *Model) Title() string { return "Sessions" }
func (m *Model) Init() tea.Cmd { return nil }

func (m *Model) Update(msg tea.Msg) (ui.Pane, tea.Cmd) {
	switch msg := msg.(type) {
	case dumpResultMsg:
		if msg.err != nil {
			// Socket attempt failed — fall back to suspend (releases the
			// TTY, so kitty's TTY-IPC works reliably). Keep busy=true to
			// hold the snapshot guard until the retry resolves.
			if msg.tried == "socket" {
				return m, tea.Batch(
					ui.Warn("socket failed, retrying with TTY suspend…"),
					dumpSuspendCmd(),
				)
			}
			m.busy = false
			return m, ui.Errf(msg.err.Error())
		}
		m.busy = false
		m.pending = msg.body
		m.mode = modeNaming
		m.name.Focus()
		return m, ui.Ok("review and name the snapshot")
	case saveResultMsg:
		if msg.err != nil {
			if msg.tried == "socket" {
				return m, tea.Batch(
					ui.Warn("socket failed, retrying save with TTY suspend…"),
					saveSuspendCmd(msg.name),
				)
			}
			m.busy = false
			return m, ui.Errf(msg.err.Error())
		}
		m.busy = false
		m.reload()
		return m, ui.Ok("saved as " + msg.name)
	case tea.KeyMsg:
		if m.mode == modeNaming {
			switch msg.String() {
			case "esc":
				m.mode = modeList
				m.name.Blur()
				return m, nil
			case "enter":
				name := strings.TrimSpace(m.name.Value())
				if name == "" {
					return m, ui.Warn("name required")
				}
				if _, err := sessionlib.WriteBody(name, m.pending); err != nil {
					return m, ui.Errf(err.Error())
				}
				m.mode = modeList
				m.name.Blur()
				m.name.SetValue("")
				m.reload()
				return m, ui.Ok("saved " + name)
			}
			var cmd tea.Cmd
			m.name, cmd = m.name.Update(msg)
			return m, cmd
		}
		if m.list.FilterState() == list.Filtering {
			break
		}
		switch msg.String() {
		case "s":
			if m.busy {
				return m, ui.Warn("snapshot already in progress…")
			}
			m.busy = true
			// Prefer the socket path (no flicker). Without $KITTY_LISTEN_ON
			// the kitty CLI has to use TTY-IPC, which fights bubbletea's
			// raw mode — so we skip straight to the suspend path.
			if hasKittySocket() {
				return m, tea.Batch(ui.Ok("snapshotting kitty layout…"), dumpCmd())
			}
			return m, tea.Batch(ui.Ok("snapshotting (briefly suspending TUI)…"), dumpSuspendCmd())
		case "a":
			if m.busy {
				return m, ui.Warn("snapshot already in progress…")
			}
			m.busy = true
			if hasKittySocket() {
				return m, tea.Batch(
					ui.Ok("saving "+sessionlib.AutoName+"…"),
					saveCmd(sessionlib.AutoName),
				)
			}
			return m, tea.Batch(
				ui.Ok("saving "+sessionlib.AutoName+" (briefly suspending TUI)…"),
				saveSuspendCmd(sessionlib.AutoName),
			)
		case "enter":
			if it, ok := m.list.SelectedItem().(item); ok {
				if err := sessionlib.Restore(it.s.Name); err != nil {
					return m, ui.Errf(err.Error())
				}
				return m, ui.Ok("launched " + it.s.Name)
			}
		case "d":
			if it, ok := m.list.SelectedItem().(item); ok {
				if err := sessionlib.Delete(it.s.Name); err != nil {
					return m, ui.Errf(err.Error())
				}
				m.reload()
				return m, ui.Ok("deleted " + it.s.Name)
			}
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m *Model) View() string {
	if m.mode == modeNaming {
		body := m.pending
		if len(body) > 600 {
			body = body[:600] + "\n…"
		}
		preview := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(ui.Overlay)).
			Padding(1, 2).
			Foreground(lipgloss.Color(ui.Subtext)).
			Width(m.w / 2).
			Render(body)
		form := lipgloss.NewStyle().Padding(1, 2).Render(
			ui.Accent.Render("save current kitty layout") + "\n\n" +
				m.name.View() + "\n\n" +
				lipgloss.NewStyle().Foreground(lipgloss.Color(ui.Muted)).Render("enter save · esc cancel"))
		return lipgloss.JoinHorizontal(lipgloss.Top, form, preview)
	}
	if len(m.list.Items()) == 0 {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(ui.Subtext)).Render(
			"no saved sessions yet — press s to snapshot, a to quick-save as 'auto'")
	}
	left := m.list.View()
	sel, ok := m.list.SelectedItem().(item)
	if !ok {
		return left
	}
	preview := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ui.Overlay)).
		Padding(1, 2).
		Foreground(lipgloss.Color(ui.Subtext)).
		Render(sel.s.Body)
	// See themes.View(): bubbles list reserves a top row for its filter input
	// bar, so the preview's top border needs a one-row offset to line up with
	// the list's first item.
	return lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", "\n"+preview)
}

func (m *Model) Help() string {
	if m.mode == modeNaming {
		return "enter save · esc cancel"
	}
	return "s snapshot · a quick-save auto · enter launch · d delete · / filter"
}

func (m *Model) SetSize(w, h int) {
	m.w, m.h = w, h
	listW := w/2 - 2
	if listW < 24 {
		listW = 24
	}
	m.list.SetSize(listW, h-4)
	m.name.Width = w/2 - 4
}

func (m *Model) CapturingKeys() bool {
	return m.mode == modeNaming || m.list.FilterState() == list.Filtering
}
