package fonts

import (
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/elythi0n/psps/internal/apply"
	"github.com/elythi0n/psps/internal/kconf"
	"github.com/elythi0n/psps/internal/ui"
)

type Font struct {
	Family string
	Nerd   bool
}

func (f Font) FilterValue() string { return f.Family }
func (f Font) Title() string {
	if f.Nerd {
		return f.Family + "  " + ui.Accent.Render("[nerd]")
	}
	return f.Family
}
func (f Font) Description() string {
	return "monospace"
}

func listMonoFonts() []Font {
	out, err := exec.Command("fc-list", ":spacing=mono", "family").Output()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var fonts []Font
	for _, line := range strings.Split(string(out), "\n") {
		fam := strings.TrimSpace(strings.SplitN(line, ",", 2)[0])
		if fam == "" || seen[fam] {
			continue
		}
		seen[fam] = true
		fonts = append(fonts, Font{
			Family: fam,
			Nerd:   strings.Contains(strings.ToLower(fam), "nerd"),
		})
	}
	sort.Slice(fonts, func(i, j int) bool { return fonts[i].Family < fonts[j].Family })
	return fonts
}

type Model struct {
	conf *kconf.Config
	list list.Model
	w, h int
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

	m := &Model{conf: conf, list: l}
	all := listMonoFonts()
	items := make([]list.Item, 0, len(all))
	for _, f := range all {
		items = append(items, f)
	}
	l.SetItems(items)
	return m
}

func (m *Model) Title() string { return "Fonts" }
func (m *Model) Init() tea.Cmd { return nil }

func (m *Model) Update(msg tea.Msg) (ui.Pane, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.list.FilterState() == list.Filtering {
			break
		}
		switch msg.String() {
		case "enter":
			if f, ok := m.list.SelectedItem().(Font); ok {
				m.conf.Set("font_family", f.Family)
				res, err := apply.Save(m.conf)
				if err != nil {
					return m, ui.Errf(err.Error())
				}
				return m, ui.Ok(reloadMsg("font_family → "+f.Family, res.Reloaded))
			}
		case "+", "=":
			return m, m.bumpSize(+1)
		case "-", "_":
			return m, m.bumpSize(-1)
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m *Model) bumpSize(delta int) tea.Cmd {
	size := 13.0
	if cur := m.conf.Get("font_size"); cur != "" {
		if f, err := strconv.ParseFloat(cur, 64); err == nil {
			size = f
		}
	}
	size += float64(delta)
	if size < 6 {
		size = 6
	}
	str := fmt.Sprintf("%.1f", size)
	m.conf.Set("font_size", str)
	res, err := apply.Save(m.conf)
	if err != nil {
		return ui.Errf(err.Error())
	}
	return ui.Ok(reloadMsg("font_size → "+str, res.Reloaded))
}

func reloadMsg(s string, reloaded int) string {
	if reloaded > 0 {
		return fmt.Sprintf("%s · reloaded %d kitty", s, reloaded)
	}
	return s
}

func (m *Model) View() string {
	sel, ok := m.list.SelectedItem().(Font)
	left := m.list.View()
	if !ok {
		return left
	}
	preview := renderPreview(sel, m.conf.Get("font_size"))
	return lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", preview)
}

func renderPreview(f Font, size string) string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(ui.Overlay)).
		Padding(1, 2)

	lines := []string{
		ui.PaneTitle.Render(f.Family),
		"current size: " + valOr(size, "13.0"),
		"",
		"The quick brown fox jumps over the lazy dog.",
		"0123456789  !@#$%^&*()  →←↑↓  ✓ ✗ ★ ☆",
		"const greet = (name: string) => `hi ${name}`;",
		"git status · npm install · cargo build",
	}
	return box.Render(strings.Join(lines, "\n"))
}

func valOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func (m *Model) Help() string {
	return "enter apply font · +/- size · / filter"
}

func (m *Model) SetSize(w, h int) {
	m.w, m.h = w, h
	listW := w/2 - 2
	if listW < 24 {
		listW = 24
	}
	m.list.SetSize(listW, h-4)
}

func (m *Model) CapturingKeys() bool {
	return m.list.FilterState() == list.Filtering
}
