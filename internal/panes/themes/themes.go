package themes

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/elythi0n/psps/internal/installer"
	"github.com/elythi0n/psps/internal/kconf"
	"github.com/elythi0n/psps/internal/kitty"
	"github.com/elythi0n/psps/internal/themelib"
	"github.com/elythi0n/psps/internal/ui"
)

// previewDebounce gates how soon after the last cursor move we hand colors
// to kitty. Short enough to feel live, long enough that holding ↓ through 20
// themes doesn't issue 20 socket calls.
const previewDebounce = 120 * time.Millisecond

// previewTickMsg fires after the debounce window. Each one carries the ID it
// was issued for so stale ticks (cursor moved again in the meantime) can be
// discarded.
type previewTickMsg struct{ id int }

// installResultMsg is the async result of an install attempt. theme is zero
// on failure; err is nil on success.
type installResultMsg struct {
	theme themelib.Theme
	err   error
}

// themeItem wraps themelib.Theme for the bubbles list. Renamed from `item`
// when installItem joined the party.
type themeItem struct{ t themelib.Theme }

func (i themeItem) FilterValue() string { return i.t.Name }
func (i themeItem) Title() string       { return i.t.Name }
func (i themeItem) Description() string {
	return fmt.Sprintf("%s · bg %s · fg %s",
		i.t.Source, i.t.Colors["background"], i.t.Colors["foreground"])
}

// installItem is the sentinel row that opens the install flow. FilterValue
// is empty so it's hidden whenever the user is searching — at that point
// they're looking for an existing theme, not adding a new one.
type installItem struct{}

func (installItem) FilterValue() string { return "" }
func (installItem) Title() string       { return "＋ Install new theme…" }
func (installItem) Description() string { return "paste a URL, git repo, or local path" }

type Model struct {
	conf *kconf.Config
	list list.Model
	w, h int

	// Live-preview state. `previewing` is true once we've sent at least one
	// `kitty @ set-colors` for the current navigation session — used to decide
	// whether OnLeave needs to send a reset. `previewID` is incremented on each
	// cursor move so debounced ticks can discard stale work, and `lastIdx`
	// detects when the bubbles list moves under us.
	previewing bool
	previewID  int
	lastIdx    int

	// Install-flow state. Engaged when the user picks the installItem row and
	// presses enter; the textinput captures keys until enter (install) or esc
	// (cancel). installErr holds the last failure message so the guide panel
	// can show it without losing the typed URL.
	installing bool
	input      textinput.Model
	installErr string
}

func New(conf *kconf.Config) *Model {
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
	ti.Placeholder = "URL · git repo · ~/path/to/theme.conf"
	ti.Prompt = "› "
	ti.CharLimit = 512

	m := &Model{conf: conf, list: l, input: ti}
	m.refreshItems("")
	return m
}

// refreshItems repopulates the list with the install sentinel followed by the
// current set of installed themes. If selectName is non-empty, post-refresh
// the cursor lands on that theme.
func (m *Model) refreshItems(selectName string) {
	all, _ := themelib.LoadAll()
	items := make([]list.Item, 0, len(all)+1)
	items = append(items, installItem{})
	selectIdx := -1
	for i, t := range all {
		items = append(items, themeItem{t})
		if selectName != "" && t.Name == selectName {
			selectIdx = i + 1 // +1 for the sentinel at idx 0
		}
	}
	m.list.SetItems(items)
	if selectIdx >= 0 {
		m.list.Select(selectIdx)
	}
}

func (m *Model) Title() string { return "Themes" }
func (m *Model) Init() tea.Cmd { return nil }

func (m *Model) Update(msg tea.Msg) (ui.Pane, tea.Cmd) {
	switch msg := msg.(type) {
	case previewTickMsg:
		if msg.id != m.previewID {
			return m, nil
		}
		// Only preview when a real theme is highlighted. The install sentinel
		// has no colors and shouldn't trigger any kitty traffic.
		if it, ok := m.list.SelectedItem().(themeItem); ok {
			_ = kitty.PreviewColors(it.t.Colors)
			m.previewing = true
		}
		return m, nil

	case installResultMsg:
		if msg.err != nil {
			m.installErr = msg.err.Error()
			// Stay in install mode so the user can fix the URL and retry —
			// previous text is preserved in the input.
			return m, ui.Errf("install failed: " + msg.err.Error())
		}
		m.installing = false
		m.installErr = ""
		m.input.Reset()
		m.input.Blur()
		m.refreshItems(msg.theme.Name)
		return m, ui.Ok(fmt.Sprintf("installed theme %s", msg.theme.Name))

	case tea.KeyMsg:
		if m.installing {
			return m.updateInstalling(msg)
		}
		if m.list.FilterState() == list.Filtering {
			break
		}
		switch msg.String() {
		case "enter":
			return m.handleEnter()
		case "esc":
			if m.previewing {
				m.cancelPreview()
				return m, ui.Warn("preview reverted")
			}
		}
	}

	prevIdx := m.list.Index()
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)

	// Detect cursor movement and schedule a debounced preview. Comparing
	// indices after the list updates catches every path the bubbles list
	// might take to change selection.
	if m.list.Index() != prevIdx || m.list.Index() != m.lastIdx {
		m.lastIdx = m.list.Index()
		// Moving onto the install sentinel revokes any active preview so the
		// user sees their real colors while reading the install guide.
		if _, ok := m.list.SelectedItem().(installItem); ok {
			m.cancelPreview()
			return m, cmd
		}
		m.previewID++
		id := m.previewID
		tick := tea.Tick(previewDebounce, func(time.Time) tea.Msg {
			return previewTickMsg{id: id}
		})
		return m, tea.Batch(cmd, tick)
	}
	return m, cmd
}

// updateInstalling handles keys while the URL input is active. Enter kicks
// off the async install; esc cancels and returns to the list.
func (m *Model) updateInstalling(msg tea.KeyMsg) (ui.Pane, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.installing = false
		m.installErr = ""
		m.input.Reset()
		m.input.Blur()
		return m, nil
	case "enter":
		src := strings.TrimSpace(m.input.Value())
		if src == "" {
			return m, nil
		}
		m.installErr = ""
		return m, runInstall(src)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// handleEnter dispatches the enter key in normal (non-installing) mode based
// on what's currently highlighted in the list.
func (m *Model) handleEnter() (ui.Pane, tea.Cmd) {
	sel := m.list.SelectedItem()
	if _, ok := sel.(installItem); ok {
		// Drop any live preview before swapping to the input panel — keeps the
		// guide readable against real colors.
		m.cancelPreview()
		m.installing = true
		m.installErr = ""
		m.input.Focus()
		return m, textinput.Blink
	}
	if it, ok := sel.(themeItem); ok {
		res, err := themelib.Apply(m.conf, it.t)
		if err != nil {
			return m, ui.Errf("apply failed: " + err.Error())
		}
		// Apply wrote the colors into kitty.conf and triggered SIGUSR1, which
		// kitty processes as a config reload — that supersedes any pending
		// `@ set-colors` preview state. Mark preview cleared so OnLeave
		// doesn't send a redundant reset.
		m.previewing = false
		out := "applied theme " + it.t.Name
		if res.Reloaded > 0 {
			out += fmt.Sprintf(" · reloaded %d kitty", res.Reloaded)
		}
		return m, ui.Ok(out)
	}
	return m, nil
}

// runInstall returns a tea.Cmd that performs the (potentially slow) install
// off the UI thread and reports the outcome back as an installResultMsg.
func runInstall(source string) tea.Cmd {
	return func() tea.Msg {
		staged, err := installer.Stage(source)
		if err != nil {
			return installResultMsg{err: err}
		}
		defer staged.Cleanup()
		confPath, err := staged.FindThemeConf()
		if err != nil {
			return installResultMsg{err: err}
		}
		t, err := themelib.Install(confPath)
		if err != nil {
			return installResultMsg{err: fmt.Errorf("install %s: %w", filepath.Base(confPath), err)}
		}
		return installResultMsg{theme: t}
	}
}

// cancelPreview tells kitty to forget any @ set-colors state and clears our
// tracking flag. Safe to call when nothing is active.
func (m *Model) cancelPreview() {
	if !m.previewing {
		return
	}
	_ = kitty.ResetPreview()
	m.previewing = false
}

// OnLeave fulfils ui.Leaver — invoked by root when the user navigates away
// (sidebar focus, tab to another pane). Reverts any live preview and bails
// out of any in-progress input so leaving and returning doesn't strand the
// pane in install mode.
func (m *Model) OnLeave() tea.Cmd {
	m.cancelPreview()
	if m.installing {
		m.installing = false
		m.input.Reset()
		m.input.Blur()
	}
	return nil
}

func (m *Model) View() string {
	left := m.list.View()
	right := m.renderRight()
	if right == "" {
		return left
	}
	// The bubbles list reserves a top row for the filter input bar even when
	// not actively filtering (default SetShowFilter is true), so its first
	// item lands on row 1, not row 0. Prepend a blank row to the right column
	// so its top border lines up with the first list item.
	return lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", "\n"+right)
}

// renderRight picks the right-hand panel: install input, install guide, or
// the colour preview, depending on current mode.
func (m *Model) renderRight() string {
	if m.installing {
		return renderInstallPanel(m.input.View(), m.installErr)
	}
	switch sel := m.list.SelectedItem().(type) {
	case installItem:
		return renderGuide("")
	case themeItem:
		return renderPreview(sel.t)
	}
	return ""
}

func (m *Model) Help() string {
	if m.installing {
		return "enter install · esc cancel · paste URL, git repo, or local path"
	}
	return "↑/↓ live-preview · / filter · enter apply or install · esc revert"
}

func (m *Model) SetSize(w, h int) {
	m.w, m.h = w, h
	listW := w/2 - 2
	if listW < 24 {
		listW = 24
	}
	m.list.SetSize(listW, h-4)
	// Width the input to the right column, minus the panel's padding/border.
	inputW := (w - listW) - 8
	if inputW < 24 {
		inputW = 24
	}
	m.input.Width = inputW
}

func (m *Model) CapturingKeys() bool {
	if m.installing {
		return true
	}
	return m.list.FilterState() == list.Filtering
}

func renderPreview(t themelib.Theme) string {
	swatch := func(hex string) string {
		if hex == "" {
			return ""
		}
		return lipgloss.NewStyle().
			Background(lipgloss.Color(hex)).
			Foreground(lipgloss.Color(hex)).
			Render("   ")
	}

	var rows []string
	rows = append(rows, ui.PaneTitle.Render(t.Name))
	rows = append(rows, fmt.Sprintf("bg %s %s", swatch(t.Colors["background"]), t.Colors["background"]))
	rows = append(rows, fmt.Sprintf("fg %s %s", swatch(t.Colors["foreground"]), t.Colors["foreground"]))
	rows = append(rows, "")
	rows = append(rows, "palette:")
	var line strings.Builder
	for i := 0; i < 16; i++ {
		k := fmt.Sprintf("color%d", i)
		line.WriteString(swatch(t.Colors[k]))
		if i == 7 {
			rows = append(rows, line.String())
			line.Reset()
		}
	}
	rows = append(rows, line.String())

	return panelBox().Render(strings.Join(rows, "\n"))
}

// renderGuide is the side panel shown while the install sentinel is
// highlighted (but the user hasn't entered input mode yet). Pure read.
func renderGuide(_ string) string {
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(ui.Muted))
	mauve := lipgloss.NewStyle().Foreground(lipgloss.Color(ui.Mauve))

	var rows []string
	rows = append(rows, ui.PaneTitle.Render("Install a theme"))
	rows = append(rows, "Press "+mauve.Render("enter")+" to paste a source.")
	rows = append(rows, "")
	rows = append(rows, mauve.Render("Accepted sources"))
	rows = append(rows, "  · URL to a .conf file")
	rows = append(rows, "  · git repository URL")
	rows = append(rows, "  · owner/repo (GitHub shorthand)")
	rows = append(rows, "  · local path (e.g. ~/x.conf)")
	rows = append(rows, "")
	rows = append(rows, muted.Render("For repos, psps looks for a single"))
	rows = append(rows, muted.Render(".conf at the root or under themes/,"))
	rows = append(rows, muted.Render("colors/, or kitty-themes/."))
	rows = append(rows, "")
	rows = append(rows, mauve.Render("Make your own"))
	rows = append(rows, "Theme files are kitty-native. Drop")
	rows = append(rows, ".conf files into:")
	rows = append(rows, "  "+muted.Render("~/.local/share/psps/themes/"))
	rows = append(rows, "")
	rows = append(rows, muted.Render("Edit any installed theme — your"))
	rows = append(rows, muted.Render("changes survive theme switches."))

	return panelBox().Render(strings.Join(rows, "\n"))
}

// renderInstallPanel is the side panel shown while the user is typing into
// the install input. Shows the prompt + last error (if any) + a condensed
// guide footer so accepted sources stay in view.
func renderInstallPanel(inputView, errMsg string) string {
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(ui.Muted))
	mauve := lipgloss.NewStyle().Foreground(lipgloss.Color(ui.Mauve))

	var rows []string
	rows = append(rows, ui.PaneTitle.Render("Install a theme"))
	rows = append(rows, inputView)
	rows = append(rows, "")
	if errMsg != "" {
		rows = append(rows, ui.ErrStyle.Render("✗ "+errMsg))
		rows = append(rows, "")
	}
	rows = append(rows, mauve.Render("Accepted"))
	rows = append(rows, "  · raw .conf URL")
	rows = append(rows, "  · git repo URL or owner/repo")
	rows = append(rows, "  · local path")
	rows = append(rows, "")
	rows = append(rows, muted.Render("enter to install · esc to cancel"))

	return panelBox().Render(strings.Join(rows, "\n"))
}

// panelBox returns the styled box used by both the preview and the install
// panels so they share padding/border treatment.
func panelBox() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ui.Overlay)).
		Padding(1, 2)
}
