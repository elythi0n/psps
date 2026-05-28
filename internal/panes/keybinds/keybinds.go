package keybinds

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/elythi0n/psps/internal/kconf"
	"github.com/elythi0n/psps/internal/ui"
)

type Bind struct {
	Chord    string
	Action   string
	Conflict bool
}

func (b Bind) FilterValue() string { return b.Chord + " " + b.Action }
func (b Bind) Title() string {
	if b.Conflict {
		return ui.WarnStyle.Render("⚠ "+b.Chord) + "  " + b.Action
	}
	return ui.Hl.Render(b.Chord) + "  " + b.Action
}
func (b Bind) Description() string {
	if b.Conflict {
		return ui.WarnStyle.Render("conflict — another binding overrides this")
	}
	return ""
}

type Model struct {
	conf *kconf.Config
	list list.Model
	w, h int
}

func New(conf *kconf.Config) *Model {
	delegate := list.NewDefaultDelegate()
	delegate.SetSpacing(0)
	delegate.ShowDescription = true

	l := list.New(nil, delegate, 0, 0)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetFilteringEnabled(true)

	m := &Model{conf: conf, list: l}
	m.reload()
	return m
}

func (m *Model) reload() {
	maps := m.conf.Maps()
	counts := map[string]int{}
	for _, l := range maps {
		counts[l.Key]++
	}
	binds := make([]Bind, 0, len(maps))
	for _, l := range maps {
		binds = append(binds, Bind{
			Chord:    l.Key,
			Action:   l.Value,
			Conflict: counts[l.Key] > 1,
		})
	}
	sort.SliceStable(binds, func(i, j int) bool {
		if binds[i].Conflict != binds[j].Conflict {
			return binds[i].Conflict // conflicts first
		}
		return binds[i].Chord < binds[j].Chord
	})
	items := make([]list.Item, len(binds))
	for i, b := range binds {
		items[i] = b
	}
	m.list.SetItems(items)
}

func (m *Model) Title() string { return "Keybinds" }
func (m *Model) Init() tea.Cmd { return nil }

func (m *Model) Update(msg tea.Msg) (ui.Pane, tea.Cmd) {
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m *Model) View() string {
	conflictCount := 0
	for _, it := range m.list.Items() {
		if b, ok := it.(Bind); ok && b.Conflict {
			conflictCount++
		}
	}
	header := lipgloss.NewStyle().Foreground(lipgloss.Color(ui.Subtext)).
		Render(fmt.Sprintf("%d bindings · %s", len(m.list.Items()),
			conflictBadge(conflictCount)))
	return header + "\n" + m.list.View()
}

func conflictBadge(n int) string {
	if n == 0 {
		return ui.OkStyle.Render("no conflicts")
	}
	return ui.WarnStyle.Render(fmt.Sprintf("%d conflicts", n))
}

func (m *Model) Help() string {
	return strings.Join([]string{
		"↑/↓ move", "/ filter", "edit by hand in kitty.conf",
	}, " · ")
}

func (m *Model) SetSize(w, h int) {
	m.w, m.h = w, h
	m.list.SetSize(w, h-4)
}

func (m *Model) CapturingKeys() bool {
	return m.list.FilterState() == list.Filtering
}
