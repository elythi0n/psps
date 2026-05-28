// Package settings is an interactive form for the common kitty.conf knobs
// you'd otherwise edit by hand. Each row knows how to validate its own type
// and is written back through the apply pipeline so live-reload + backup
// happen automatically.
package settings

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/elythi0n/psps/internal/apply"
	"github.com/elythi0n/psps/internal/kconf"
	"github.com/elythi0n/psps/internal/ui"
)

type fieldKind int

const (
	kindFloat fieldKind = iota // 0.0–1.0 (opacity)
	kindInt
	kindEnum
	kindBool
)

type Field struct {
	Key     string
	Label   string
	Hint    string
	Kind    fieldKind
	Default string

	// Validation bounds (ints/floats)
	Min, Max float64

	// For enums
	Options []string
}

// fields defines the editable set. Add a row here to expose a new setting —
// nothing else in this file needs to know about it.
var fields = []Field{
	{Key: "background_opacity", Label: "Background opacity", Hint: "0.0 (transparent) – 1.0 (opaque)", Kind: kindFloat, Default: "0.95", Min: 0, Max: 1},
	{Key: "background_blur", Label: "Background blur", Hint: "compositor blur radius; 0 disables", Kind: kindInt, Default: "0", Min: 0, Max: 64},
	{Key: "window_padding_width", Label: "Window padding", Hint: "cells of padding inside the window", Kind: kindInt, Default: "10", Min: 0, Max: 80},
	{Key: "window_margin_width", Label: "Window margin", Hint: "space between split panes", Kind: kindInt, Default: "0", Min: 0, Max: 40},
	{Key: "cursor_trail", Label: "Cursor trail", Hint: "0 = off, 3 = subtle, higher = longer", Kind: kindInt, Default: "0", Min: 0, Max: 16},
	{Key: "cursor_blink_interval", Label: "Cursor blink interval", Hint: "seconds; 0 disables blinking", Kind: kindFloat, Default: "0.5", Min: 0, Max: 5},
	{Key: "scrollback_lines", Label: "Scrollback lines", Hint: "history retained per window", Kind: kindInt, Default: "2000", Min: 0, Max: 1000000},
	{Key: "tab_bar_style", Label: "Tab bar style", Hint: "visual style for the tab strip", Kind: kindEnum, Default: "fade", Options: []string{"fade", "slant", "separator", "powerline", "custom", "hidden"}},
	{Key: "tab_powerline_style", Label: "Powerline style", Hint: "only used when tab_bar_style = powerline", Kind: kindEnum, Default: "angled", Options: []string{"angled", "slanted", "round"}},
	{Key: "enable_audio_bell", Label: "Audio bell", Hint: "ring the terminal bell on alerts", Kind: kindBool, Default: "no"},
	{Key: "shell_integration", Label: "Shell integration", Hint: "enabled = jump-to-prompt + click-to-cd", Kind: kindEnum, Default: "enabled", Options: []string{"enabled", "disabled", "no-rc", "no-cursor", "no-title"}},
}

type Model struct {
	conf  *kconf.Config
	idx   int
	input textinput.Model
	edit  bool
	w, h  int
}

func New(conf *kconf.Config) *Model {
	ti := textinput.New()
	ti.CharLimit = 32
	ti.Prompt = "› "
	return &Model{conf: conf, input: ti}
}

func (m *Model) Title() string { return "Settings" }
func (m *Model) Init() tea.Cmd { return nil }

func (m *Model) currentValue(f Field) string {
	if v := m.conf.Get(f.Key); v != "" {
		return v
	}
	return f.Default
}

func (m *Model) Update(msg tea.Msg) (ui.Pane, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.edit {
			switch msg.String() {
			case "esc":
				m.edit = false
				m.input.Blur()
				return m, nil
			case "enter":
				f := fields[m.idx]
				val := strings.TrimSpace(m.input.Value())
				if err := validate(f, val); err != nil {
					return m, ui.Errf(err.Error())
				}
				m.conf.Set(f.Key, val)
				res, err := apply.Save(m.conf)
				if err != nil {
					return m, ui.Errf(err.Error())
				}
				m.edit = false
				m.input.Blur()
				msg := fmt.Sprintf("%s → %s", f.Key, val)
				if res.Reloaded > 0 {
					msg += fmt.Sprintf(" · reloaded %d kitty", res.Reloaded)
				}
				return m, ui.Ok(msg)
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}

		switch msg.String() {
		case "up", "k":
			if m.idx > 0 {
				m.idx--
			}
		case "down", "j":
			if m.idx < len(fields)-1 {
				m.idx++
			}
		case "enter", "e":
			f := fields[m.idx]
			m.input.SetValue(m.currentValue(f))
			m.input.Focus()
			m.edit = true
			return m, nil
		case " ":
			// quick toggle for bool / cycle for enum
			f := fields[m.idx]
			next := cycleValue(f, m.currentValue(f))
			if next == "" {
				return m, nil
			}
			m.conf.Set(f.Key, next)
			res, err := apply.Save(m.conf)
			if err != nil {
				return m, ui.Errf(err.Error())
			}
			out := fmt.Sprintf("%s → %s", f.Key, next)
			if res.Reloaded > 0 {
				out += fmt.Sprintf(" · reloaded %d kitty", res.Reloaded)
			}
			return m, ui.Ok(out)
		}
	}
	return m, nil
}

func cycleValue(f Field, cur string) string {
	switch f.Kind {
	case kindBool:
		if isYes(cur) {
			return "no"
		}
		return "yes"
	case kindEnum:
		for i, opt := range f.Options {
			if opt == cur {
				return f.Options[(i+1)%len(f.Options)]
			}
		}
		if len(f.Options) > 0 {
			return f.Options[0]
		}
	}
	return ""
}

func isYes(s string) bool {
	switch strings.ToLower(s) {
	case "yes", "y", "true", "1", "on":
		return true
	}
	return false
}

func validate(f Field, val string) error {
	switch f.Kind {
	case kindInt:
		n, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("%s must be an integer", f.Key)
		}
		if float64(n) < f.Min || float64(n) > f.Max {
			return fmt.Errorf("%s must be between %d and %d", f.Key, int(f.Min), int(f.Max))
		}
	case kindFloat:
		x, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return fmt.Errorf("%s must be a number", f.Key)
		}
		if x < f.Min || x > f.Max {
			return fmt.Errorf("%s must be between %v and %v", f.Key, f.Min, f.Max)
		}
	case kindEnum:
		for _, opt := range f.Options {
			if opt == val {
				return nil
			}
		}
		return fmt.Errorf("%s must be one of: %s", f.Key, strings.Join(f.Options, ", "))
	case kindBool:
		switch strings.ToLower(val) {
		case "yes", "no", "y", "n", "true", "false":
			return nil
		default:
			return fmt.Errorf("%s must be yes or no", f.Key)
		}
	}
	return nil
}

func (m *Model) View() string {
	rowStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ui.Text))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ui.Muted))
	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ui.Subtext))
	valStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ui.Blue))
	selectedRow := lipgloss.NewStyle().
		Foreground(lipgloss.Color(ui.Base)).
		Background(lipgloss.Color(ui.Mauve)).
		Bold(true).
		Padding(0, 1)

	var rows []string
	for i, f := range fields {
		cur := m.currentValue(f)
		label := fmt.Sprintf("%-26s", f.Label)
		keyTxt := fmt.Sprintf("%-26s", f.Key)
		valTxt := cur

		line := rowStyle.Render(label) + keyStyle.Render(keyTxt) + valStyle.Render(valTxt)
		if i == m.idx {
			line = selectedRow.Render("▸ " + label + keyTxt + valTxt)
		}
		rows = append(rows, line)
		if i == m.idx {
			rows = append(rows, hintStyle.Render("    "+f.Hint))
		}
	}

	body := strings.Join(rows, "\n")

	if m.edit {
		f := fields[m.idx]
		var hint string
		switch f.Kind {
		case kindEnum:
			hint = "one of: " + strings.Join(f.Options, ", ")
		case kindBool:
			hint = "yes / no"
		case kindInt:
			hint = fmt.Sprintf("integer in [%d, %d]", int(f.Min), int(f.Max))
		case kindFloat:
			hint = fmt.Sprintf("number in [%v, %v]", f.Min, f.Max)
		}
		editor := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(ui.Mauve)).
			Padding(1, 2).
			Render(
				ui.Accent.Render("edit "+f.Key) + "\n" +
					hintStyle.Render(hint) + "\n\n" +
					m.input.View() + "\n\n" +
					hintStyle.Render("enter save · esc cancel"))
		return body + "\n\n" + editor
	}
	return body
}

func (m *Model) Help() string {
	return "↑/↓ move · enter edit · space toggle/cycle · saves trigger reload"
}

func (m *Model) SetSize(w, h int) {
	m.w, m.h = w, h
	m.input.Width = 32
}

func (m *Model) CapturingKeys() bool { return m.edit }
