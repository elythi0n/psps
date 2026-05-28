// Package diff computes and renders the pending change set between an
// in-memory *kconf.Config and what's currently on disk.
//
// Since kconf edits are key-scoped (Set replaces or appends a directive),
// we compare directive-by-directive rather than running a text diff. This
// yields readable rows even when the change is small (one color updated).
package diff

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/elythi0n/psps/internal/kconf"
	"github.com/elythi0n/psps/internal/ui"
)

type ChangeKind int

const (
	ChangeAdded ChangeKind = iota
	ChangeModified
	ChangeRemoved
)

type Change struct {
	Key  string
	Kind ChangeKind
	Old  string
	New  string
}

// Between returns the directive-level differences between two configs. Map
// directives are NOT included (they're handled by the keybind editor and use
// a different identity model — chord, not key).
func Between(prev, next *kconf.Config) []Change {
	prevD := directives(prev)
	nextD := directives(next)

	var out []Change
	for k, nv := range nextD {
		pv, had := prevD[k]
		switch {
		case !had:
			out = append(out, Change{Key: k, Kind: ChangeAdded, New: nv})
		case pv != nv:
			out = append(out, Change{Key: k, Kind: ChangeModified, Old: pv, New: nv})
		}
	}
	for k, pv := range prevD {
		if _, ok := nextD[k]; !ok {
			out = append(out, Change{Key: k, Kind: ChangeRemoved, Old: pv})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func directives(c *kconf.Config) map[string]string {
	out := map[string]string{}
	for _, l := range c.Lines {
		if l.Kind == kconf.LineDirective {
			out[l.Key] = l.Value // last-write-wins, matches kitty's resolution
		}
	}
	return out
}

// Render returns a colored multi-line string for terminal display. Pass
// useColor=false for plain stdout.
func Render(changes []Change, useColor bool) string {
	if len(changes) == 0 {
		if useColor {
			return ui.OkStyle.Render("no changes")
		}
		return "no changes"
	}
	var lines []string
	for _, ch := range changes {
		switch ch.Kind {
		case ChangeAdded:
			lines = append(lines, colored("+ "+ch.Key+" "+ch.New, ui.Green, useColor))
		case ChangeRemoved:
			lines = append(lines, colored("- "+ch.Key+" "+ch.Old, ui.Red, useColor))
		case ChangeModified:
			lines = append(lines,
				colored("- "+ch.Key+" "+ch.Old, ui.Red, useColor),
				colored("+ "+ch.Key+" "+ch.New, ui.Green, useColor),
			)
		}
	}
	return strings.Join(lines, "\n")
}

func colored(s, hex string, useColor bool) string {
	if !useColor {
		return s
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(hex)).Render(s)
}

// Summary returns a one-line summary like "12 changes (3+, 8~, 1-)".
func Summary(changes []Change) string {
	var add, mod, del int
	for _, ch := range changes {
		switch ch.Kind {
		case ChangeAdded:
			add++
		case ChangeModified:
			mod++
		case ChangeRemoved:
			del++
		}
	}
	if len(changes) == 0 {
		return "no changes"
	}
	return fmt.Sprintf("%d changes (%d+, %d~, %d-)", len(changes), add, mod, del)
}
