// Package cli is the non-TUI entry point: psps theme apply X, psps set K V,
// etc. Each handler reads the current config, mutates it in memory, and goes
// through the apply pipeline so backup + reload behavior matches the TUI.
package cli

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/elythi0n/psps/internal/apply"
	"github.com/elythi0n/psps/internal/backups"
	"github.com/elythi0n/psps/internal/diff"
	"github.com/elythi0n/psps/internal/installer"
	"github.com/elythi0n/psps/internal/kconf"
	"github.com/elythi0n/psps/internal/kitty"
	"github.com/elythi0n/psps/internal/profile"
	"github.com/elythi0n/psps/internal/sessionlib"
	"github.com/elythi0n/psps/internal/themelib"
)

//go:embed AGENTS.md
var agentGuide string

// cli carries the per-invocation state used by every command handler. The
// only field today is `json` — set from --json on argv — but routing it
// through a struct (rather than a package global) keeps Run() reentrant and
// makes it obvious at the call site that handlers can switch output formats.
type cli struct {
	json bool
}

// emit writes v as a single JSON object to stdout with a trailing newline.
// Only called in JSON mode; the text-mode paths use plain fmt.Println.
func (c *cli) emit(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// fail reports an error and returns the exit code the caller should use. In
// JSON mode the error lands on stdout (so agents only parse one stream); in
// text mode it goes to stderr like before. Always returns `exit` so callers
// can `return c.fail(...)` in one line.
func (c *cli) fail(msg string, exit int) int {
	if c.json {
		c.emit(map[string]any{"error": msg, "exit": exit})
	} else {
		fmt.Fprintln(os.Stderr, msg)
	}
	return exit
}

// extractJSON strips --json from args (anywhere it appears) and returns the
// remainder plus whether the flag was seen.
func extractJSON(args []string) (bool, []string) {
	out := make([]string, 0, len(args))
	seen := false
	for _, a := range args {
		if a == "--json" {
			seen = true
			continue
		}
		out = append(out, a)
	}
	return seen, out
}

// Run dispatches argv (excluding the program name) and returns an exit code.
func Run(args []string) int {
	json, args := extractJSON(args)
	c := &cli{json: json}

	if len(args) == 0 {
		printHelp()
		return 0
	}
	switch args[0] {
	case "-h", "--help", "help":
		printHelp()
		return 0
	case "-v", "--version", "version":
		if c.json {
			c.emit(map[string]any{"version": versionString()})
			return 0
		}
		fmt.Println("psps", versionString())
		return 0
	case "agent-guide":
		fmt.Print(agentGuide)
		return 0
	case "reload":
		return c.cmdReload()
	case "undo":
		return c.cmdUndo()
	case "backups":
		return c.cmdBackups(args[1:])
	case "diff":
		return c.cmdDiff()
	case "set":
		return c.cmdSet(args[1:])
	case "get":
		return c.cmdGet(args[1:])
	case "theme":
		return c.cmdTheme(args[1:])
	case "profile":
		return c.cmdProfile(args[1:])
	case "font":
		return c.cmdFont(args[1:])
	case "zoom":
		return c.cmdZoom(args[1:])
	case "session":
		return c.cmdSession(args[1:])
	case "autosave":
		return c.cmdAutosave(args[1:])
	case "doctor":
		return c.cmdDoctor()
	}
	return c.fail("unknown command: "+args[0]+" (run `psps help` for usage)", 2)
}

var version = "dev" // overridden via -ldflags

func versionString() string { return version }

func (c *cli) loadConf() (*kconf.Config, int) {
	conf, err := kconf.Load(kconf.Default())
	if err != nil {
		return nil, c.fail("load config: "+err.Error(), 1)
	}
	return conf, 0
}

func reloadHint(n int) string {
	if n == 0 {
		return ""
	}
	if n == 1 {
		return " · reloaded 1 kitty"
	}
	return fmt.Sprintf(" · reloaded %d kitty", n)
}

// ─── help ──────────────────────────────────────────────────────────────────

func printHelp() {
	const usage = `psps — kitty config manager

usage:
  psps                              launch the TUI
  psps theme list                   print available themes
  psps theme apply <name>           set the theme and reload kitty
  psps theme install <url|path>     install a theme from a .conf file or git repo
  psps theme remove <name>          delete an installed theme
  psps profile list                 print available profiles
  psps profile apply <name>         merge a profile's components into kitty.conf
  psps profile install <url|path>   install a profile (dir of theme/settings/keybinds/fonts .conf)
  psps profile remove <name>        delete an installed profile
  psps font set <family>            set font_family
  psps font size <n>                set font_size
  psps zoom [get|set|inc|dec] [n]   font_size that persists across reload+theme apply
                                    (bind in kitty.conf to replace ctrl++/ctrl+- defaults)
  psps set <key> <value>            set any kitty.conf directive
  psps get <key>                    print the current value of <key>
  psps reload                       signal running kitty processes (SIGUSR1)
  psps diff                         show what would change vs disk
  psps undo                         restore the most recent backup
  psps backups                      list available backup snapshots
  psps session save [name]          snapshot the current kitty layout (default name: auto)
  psps session list                 list saved sessions
  psps session restore <name>       launch kitty with the given session
  psps session delete <name>        remove a session file
  psps autosave enable              point startup_session at the auto session + take an initial snapshot
  psps autosave disable             clear startup_session
  psps autosave run [--interval=15s] foreground loop that keeps the auto session up to date
  psps doctor                       diagnose common setup issues (kitty.conf, remote-control, paths)
  psps agent-guide                  print the agent-facing guide (machine-friendly invocations)
  psps version

flags:
  --json                            emit a single JSON object on stdout (also routes errors to stdout)
  --dry-run                         print the diff instead of writing (works with set/font/theme apply)
  --yes                             skip confirmation prompts on install commands
  --name <id>                       override the install name derived from the source
`
	fmt.Print(usage)
}

// ─── reload / undo / backups ──────────────────────────────────────────────

func (c *cli) cmdReload() int {
	n := kitty.PidCount()
	if err := kitty.Reload(); err != nil {
		return c.fail("reload: "+err.Error(), 1)
	}
	if c.json {
		c.emit(map[string]any{"reloaded": n})
		return 0
	}
	switch n {
	case 0:
		fmt.Println("no running kitty processes")
	case 1:
		fmt.Println("reloaded 1 kitty")
	default:
		fmt.Printf("reloaded %d kitty\n", n)
	}
	return 0
}

func (c *cli) cmdUndo() int {
	conf, code := c.loadConf()
	if code != 0 {
		return code
	}
	e, err := apply.Undo(conf)
	if err != nil {
		return c.fail("undo: "+err.Error(), 1)
	}
	_ = kitty.Reload()
	n := kitty.PidCount()
	if c.json {
		c.emit(map[string]any{"restored": e.Name, "reloaded": n})
		return 0
	}
	fmt.Printf("restored %s%s\n", e.Name, reloadHint(n))
	return 0
}

func (c *cli) cmdBackups(args []string) int {
	_ = args
	conf, code := c.loadConf()
	if code != 0 {
		return code
	}
	entries, err := backups.List(conf.Path)
	if err != nil {
		return c.fail("backups: "+err.Error(), 1)
	}
	if c.json {
		out := make([]map[string]any, 0, len(entries))
		for _, e := range entries {
			out = append(out, map[string]any{
				"name": e.Name,
				"path": e.Path,
				"when": e.When.UTC().Format(time.RFC3339),
			})
		}
		c.emit(map[string]any{"backups": out})
		return 0
	}
	if len(entries) == 0 {
		fmt.Println("no backups")
		return 0
	}
	for _, e := range entries {
		fmt.Printf("%s  %s\n", e.When.Format("2006-01-02 15:04:05"), e.Name)
	}
	return 0
}

// ─── diff ────────────────────────────────────────────────────────────────

func (c *cli) cmdDiff() int {
	conf, code := c.loadConf()
	if code != 0 {
		return code
	}
	// Compare current in-memory copy to a fresh load — same file, so always
	// "no changes". This subcommand mostly exists so `--dry-run` shows the
	// helper's output format; non-trivial diffs come from set/apply --dry-run.
	disk, _ := kconf.Load(conf.Path)
	changes := diff.Between(disk, conf)
	if c.json {
		c.emitChanges(changes)
		return 0
	}
	fmt.Println(diff.Render(changes, useColor()))
	return 0
}

// emitChanges renders a diff.Change slice as the canonical JSON envelope.
func (c *cli) emitChanges(changes []diff.Change) {
	out := make([]map[string]any, 0, len(changes))
	for _, ch := range changes {
		kind := "modified"
		switch ch.Kind {
		case diff.ChangeAdded:
			kind = "added"
		case diff.ChangeRemoved:
			kind = "removed"
		}
		row := map[string]any{
			"key":  ch.Key,
			"kind": kind,
		}
		if ch.Old != "" {
			row["before"] = ch.Old
		}
		if ch.New != "" {
			row["after"] = ch.New
		}
		out = append(out, row)
	}
	c.emit(map[string]any{
		"changes": out,
		"summary": diff.Summary(changes),
	})
}

// ─── set / get ───────────────────────────────────────────────────────────

func (c *cli) cmdSet(args []string) int {
	dry, rest := extractDryRun(args)
	if len(rest) < 2 {
		return c.fail("usage: psps set <key> <value...>", 2)
	}
	key := rest[0]
	value := strings.Join(rest[1:], " ")

	conf, code := c.loadConf()
	if code != 0 {
		return code
	}
	before, _ := kconf.Load(conf.Path)
	conf.Set(key, value)

	if dry {
		changes := diff.Between(before, conf)
		if c.json {
			c.emitChanges(changes)
			return 0
		}
		fmt.Println(diff.Render(changes, useColor()))
		return 0
	}

	res, err := apply.Save(conf)
	if err != nil {
		return c.fail("save: "+err.Error(), 1)
	}
	if c.json {
		c.emit(map[string]any{"key": key, "value": value, "reloaded": res.Reloaded})
		return 0
	}
	fmt.Printf("%s → %s%s\n", key, value, reloadHint(res.Reloaded))
	return 0
}

func (c *cli) cmdGet(args []string) int {
	if len(args) != 1 {
		return c.fail("usage: psps get <key>", 2)
	}
	conf, code := c.loadConf()
	if code != 0 {
		return code
	}
	key := args[0]
	v := conf.Get(key)
	if c.json {
		c.emit(map[string]any{"key": key, "value": v, "set": v != ""})
		return 0
	}
	if v == "" {
		fmt.Fprintln(os.Stderr, key, "is not set")
		return 1
	}
	fmt.Println(v)
	return 0
}

// ─── theme ───────────────────────────────────────────────────────────────

func (c *cli) cmdTheme(args []string) int {
	if len(args) == 0 {
		return c.fail("usage: psps theme list|apply|install|remove", 2)
	}
	switch args[0] {
	case "list":
		all, err := themelib.LoadAll()
		if err != nil {
			return c.fail("themes: "+err.Error(), 1)
		}
		sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
		if c.json {
			out := make([]map[string]any, 0, len(all))
			for _, t := range all {
				out = append(out, map[string]any{
					"name":   t.Name,
					"source": t.Source,
					"path":   t.Path,
					"colors": t.Colors,
				})
			}
			c.emit(map[string]any{"themes": out})
			return 0
		}
		color := useColor()
		for _, t := range all {
			fmt.Printf("%-30s  %-10s  %s  %s\n",
				t.Name, t.Source, renderSwatch(t, color), t.Path)
		}
		return 0
	case "apply":
		dry, rest := extractDryRun(args[1:])
		if len(rest) < 1 {
			return c.fail("usage: psps theme apply <name>", 2)
		}
		return c.applyThemeByName(rest[0], dry)
	case "install":
		return c.cmdThemeInstall(args[1:])
	case "remove":
		if len(args) < 2 {
			return c.fail("usage: psps theme remove <name>", 2)
		}
		if err := themelib.Remove(args[1]); err != nil {
			return c.fail("remove: "+err.Error(), 1)
		}
		if c.json {
			c.emit(map[string]any{"removed": args[1]})
			return 0
		}
		fmt.Printf("removed theme %s\n", args[1])
		return 0
	}
	return c.fail("unknown theme subcommand: "+args[0], 2)
}

func (c *cli) cmdThemeInstall(args []string) int {
	yes, name, rest := extractInstallFlags(args)
	if len(rest) < 1 {
		return c.fail("usage: psps theme install <url|path> [--name foo] [--yes]", 2)
	}
	if c.json && !yes {
		return c.fail("psps theme install --json requires --yes (the interactive confirm prompt would hang)", 2)
	}
	source := rest[0]
	staged, err := installer.Stage(source)
	if err != nil {
		return c.fail("stage: "+err.Error(), 1)
	}
	defer staged.Cleanup()

	// Locate the actual .conf file. For KindFile we use it directly; for
	// KindDirectory we look for a single .conf at the root, or any .conf in
	// the repo's "themes/" or "colors/" subdir (a common convention).
	confPath, err := staged.FindThemeConf()
	if err != nil {
		return c.fail("install: "+err.Error(), 1)
	}

	if name == "" {
		name = strings.TrimSuffix(filepath.Base(confPath), filepath.Ext(confPath))
	}
	if !c.json {
		fmt.Printf("about to install theme %q from %s (via %s)\n", name, staged.Source, staged.Transport())
		fmt.Println("─── theme contents ────────────────────────────────────────")
		if err := streamFile(os.Stdout, confPath, 120); err != nil {
			return c.fail("preview: "+err.Error(), 1)
		}
		fmt.Println("───────────────────────────────────────────────────────────")
		if !yes && !confirm("install this theme?") {
			fmt.Println("aborted.")
			return 0
		}
	}
	// installer wrote into a tempdir under its preferred name; rename inside
	// the temp area so themelib.Install picks up the user-provided name.
	if filepath.Base(confPath) != name+".conf" {
		renamed := filepath.Join(filepath.Dir(confPath), name+".conf")
		if err := os.Rename(confPath, renamed); err != nil {
			return c.fail("rename: "+err.Error(), 1)
		}
		confPath = renamed
	}
	t, err := themelib.Install(confPath)
	if err != nil {
		return c.fail("install: "+err.Error(), 1)
	}
	if c.json {
		c.emit(map[string]any{
			"installed": t.Name,
			"path":      t.Path,
			"source":    staged.Source,
			"transport": staged.Transport(),
		})
		return 0
	}
	fmt.Printf("installed theme %s → %s\n", t.Name, t.Path)
	return 0
}

// ─── profile ─────────────────────────────────────────────────────────────

func (c *cli) cmdProfile(args []string) int {
	if len(args) == 0 {
		return c.fail("usage: psps profile list|apply|install|remove", 2)
	}
	switch args[0] {
	case "list":
		all, err := profile.List()
		if err != nil {
			return c.fail("profiles: "+err.Error(), 1)
		}
		if c.json {
			out := make([]map[string]any, 0, len(all))
			for _, p := range all {
				comps := make([]string, 0)
				for _, c := range profile.Components {
					if p.Has(c) {
						comps = append(comps, string(c))
					}
				}
				out = append(out, map[string]any{
					"name":       p.Name,
					"dir":        p.Dir,
					"components": comps,
				})
			}
			c.emit(map[string]any{"profiles": out})
			return 0
		}
		if len(all) == 0 {
			fmt.Println("no installed profiles")
			return 0
		}
		for _, p := range all {
			var present []string
			for _, c := range profile.Components {
				if p.Has(c) {
					present = append(present, string(c))
				}
			}
			fmt.Printf("%-24s  [%s]  %s\n", p.Name, strings.Join(present, ", "), p.Dir)
		}
		return 0
	case "apply":
		if len(args) < 2 {
			return c.fail("usage: psps profile apply <name>", 2)
		}
		return c.cmdProfileApply(args[1])
	case "install":
		return c.cmdProfileInstall(args[1:])
	case "remove":
		if len(args) < 2 {
			return c.fail("usage: psps profile remove <name>", 2)
		}
		if err := profile.Remove(args[1]); err != nil {
			return c.fail("remove: "+err.Error(), 1)
		}
		if c.json {
			c.emit(map[string]any{"removed": args[1]})
			return 0
		}
		fmt.Printf("removed profile %s\n", args[1])
		return 0
	}
	return c.fail("unknown profile subcommand: "+args[0], 2)
}

func (c *cli) cmdProfileApply(name string) int {
	p, err := profile.Load(name)
	if err != nil {
		return c.fail(err.Error(), 1)
	}
	conf, code := c.loadConf()
	if code != 0 {
		return code
	}
	res, err := profile.Apply(p, conf)
	if err != nil {
		return c.fail("apply: "+err.Error(), 1)
	}
	components := make([]string, 0)
	for _, comp := range profile.Components {
		if p.Has(comp) {
			components = append(components, string(comp))
		}
	}
	if c.json {
		c.emit(map[string]any{
			"applied":    p.Name,
			"components": components,
			"reloaded":   res.Reloaded,
		})
		return 0
	}
	fmt.Printf("applied profile %s [%s]%s\n", p.Name, strings.Join(components, ", "), reloadHint(res.Reloaded))
	return 0
}

func (c *cli) cmdProfileInstall(args []string) int {
	yes, name, rest := extractInstallFlags(args)
	if len(rest) < 1 {
		return c.fail("usage: psps profile install <url|path> [--name foo] [--yes]", 2)
	}
	if c.json && !yes {
		return c.fail("psps profile install --json requires --yes (the interactive confirm prompt would hang)", 2)
	}
	source := rest[0]
	staged, err := installer.Stage(source)
	if err != nil {
		return c.fail("stage: "+err.Error(), 1)
	}
	defer staged.Cleanup()

	if staged.Kind != installer.KindDirectory {
		return c.fail("profile install expects a directory (git repo or local dir); got a single file", 1)
	}
	if name == "" {
		name = profileNameFromSource(staged.Source)
	}
	if !c.json {
		fmt.Printf("about to install profile %q from %s (via %s)\n", name, staged.Source, staged.Transport())
		fmt.Println("─── components ────────────────────────────────────────────")
		if err := previewProfile(staged.LocalPath); err != nil {
			return c.fail("preview: "+err.Error(), 1)
		}
		fmt.Println("───────────────────────────────────────────────────────────")
		if !yes && !confirm("install this profile?") {
			fmt.Println("aborted.")
			return 0
		}
	}
	p, err := profile.Install(staged.LocalPath, name)
	if err != nil {
		return c.fail("install: "+err.Error(), 1)
	}
	components := make([]string, 0)
	for _, comp := range profile.Components {
		if p.Has(comp) {
			components = append(components, string(comp))
		}
	}
	if c.json {
		c.emit(map[string]any{
			"installed":  p.Name,
			"dir":        p.Dir,
			"components": components,
			"source":     staged.Source,
			"transport":  staged.Transport(),
		})
		return 0
	}
	fmt.Printf("installed profile %s [%s] → %s\n", p.Name, strings.Join(components, ", "), p.Dir)
	return 0
}

func previewProfile(stagedDir string) error {
	// Show which components are present and a short head of each.
	for _, c := range profile.Components {
		// Try both the staging root and one level deeper (common when git
		// repos wrap the profile in a subdir).
		found := ""
		for _, root := range []string{stagedDir, ""} {
			if root == "" {
				entries, _ := os.ReadDir(stagedDir)
				for _, e := range entries {
					if e.IsDir() {
						p := filepath.Join(stagedDir, e.Name(), c.Filename())
						if _, err := os.Stat(p); err == nil {
							found = p
							break
						}
					}
				}
			} else {
				p := filepath.Join(root, c.Filename())
				if _, err := os.Stat(p); err == nil {
					found = p
					break
				}
			}
		}
		if found == "" {
			fmt.Printf("  %-10s (not in this profile)\n", c)
			continue
		}
		fmt.Printf("  %-10s %s\n", c, found)
		if err := streamFile(os.Stdout, found, 15); err != nil {
			return err
		}
	}
	return nil
}

func profileNameFromSource(source string) string {
	// For a repo URL, the last path segment is usually the repo name.
	source = strings.TrimSuffix(source, ".git")
	if i := strings.LastIndex(source, "/"); i >= 0 {
		return source[i+1:]
	}
	return source
}

// ─── zoom ────────────────────────────────────────────────────────────────

// cmdZoom changes font_size in kitty.conf and triggers a kitty reload. Unlike
// kitty's built-in `change_font_size` action — which only mutates the running
// kitty's font and is lost on the next config reload — this writes the value
// into kitty.conf so it survives `psps theme apply`, undo, restarts, etc.
//
// Suggested keybindings (replace kitty's defaults in ~/.config/kitty/kitty.conf):
//
//	map ctrl+equal    launch --type=background psps zoom inc 2
//	map ctrl+minus    launch --type=background psps zoom dec 2
//
// (Use `ctrl+shift+equal` if your terminal sends shift-modified `+`.)
func (c *cli) cmdZoom(args []string) int {
	if len(args) == 0 {
		return c.cmdZoomShow()
	}
	switch args[0] {
	case "get":
		return c.cmdZoomGet()
	case "set":
		if len(args) < 2 {
			return c.fail("usage: psps zoom set <size>", 2)
		}
		return c.cmdZoomSet(args[1])
	case "inc":
		step := 2.0
		if len(args) >= 2 {
			s, err := strconv.ParseFloat(args[1], 64)
			if err != nil {
				return c.fail("invalid step: "+err.Error(), 2)
			}
			step = s
		}
		return c.cmdZoomDelta(+step)
	case "dec":
		step := 2.0
		if len(args) >= 2 {
			s, err := strconv.ParseFloat(args[1], 64)
			if err != nil {
				return c.fail("invalid step: "+err.Error(), 2)
			}
			step = s
		}
		return c.cmdZoomDelta(-step)
	}
	// `psps zoom <number>` as shorthand for `psps zoom set <number>`.
	if _, err := strconv.ParseFloat(args[0], 64); err == nil {
		return c.cmdZoomSet(args[0])
	}
	return c.fail("unknown zoom subcommand: "+args[0], 2)
}

func (c *cli) cmdZoomShow() int {
	conf, code := c.loadConf()
	if code != 0 {
		return code
	}
	cur := conf.Get("font_size")
	if c.json {
		// "show" without args is informational; emit the same shape as `get`
		// so agents have one less variant to handle.
		size, isDefault := zoomCurrent(cur)
		c.emit(map[string]any{"font_size": size, "default": isDefault})
		return 0
	}
	if cur == "" {
		cur = "(unset — kitty uses 11.0)"
	}
	fmt.Println("current font_size:", cur)
	fmt.Println()
	fmt.Println("usage:")
	fmt.Println("  psps zoom set <n>          absolute size")
	fmt.Println("  psps zoom inc [step]       increase by step (default 2.0)")
	fmt.Println("  psps zoom dec [step]       decrease by step (default 2.0)")
	fmt.Println("  psps zoom get              just print the number")
	fmt.Println()
	fmt.Println("bind in kitty.conf to replace the runtime-only ctrl++/ctrl+- defaults:")
	fmt.Println("  map ctrl+equal    launch --type=background psps zoom inc 2")
	fmt.Println("  map ctrl+minus    launch --type=background psps zoom dec 2")
	return 0
}

// zoomCurrent parses the in-config font_size into a float and reports whether
// we fell back to kitty's default (11.0). Centralised so get/show/inc/dec
// agree.
func zoomCurrent(raw string) (float64, bool) {
	if raw == "" {
		return 11.0, true
	}
	if f, err := strconv.ParseFloat(raw, 64); err == nil {
		return f, false
	}
	return 11.0, true
}

func (c *cli) cmdZoomGet() int {
	conf, code := c.loadConf()
	if code != 0 {
		return code
	}
	cur := conf.Get("font_size")
	if c.json {
		size, isDefault := zoomCurrent(cur)
		c.emit(map[string]any{"font_size": size, "default": isDefault})
		return 0
	}
	if cur == "" {
		// Stay terminal-friendly for scripts that pipe this — print the kitty
		// default rather than an empty line.
		fmt.Println("11.0")
		return 0
	}
	fmt.Println(cur)
	return 0
}

func (c *cli) cmdZoomSet(s string) int {
	size, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return c.fail("invalid size: "+err.Error(), 2)
	}
	if size < 1 {
		return c.fail("font_size must be >= 1", 2)
	}
	conf, code := c.loadConf()
	if code != 0 {
		return code
	}
	conf.Set("font_size", formatFontSize(size))
	res, err := apply.Save(conf)
	if err != nil {
		return c.fail("save: "+err.Error(), 1)
	}
	if c.json {
		c.emit(map[string]any{
			"font_size": size,
			"reloaded":  res.Reloaded,
		})
		return 0
	}
	fmt.Printf("font_size → %s%s\n", formatFontSize(size), reloadHint(res.Reloaded))
	return 0
}

func (c *cli) cmdZoomDelta(delta float64) int {
	conf, code := c.loadConf()
	if code != 0 {
		return code
	}
	cur, _ := zoomCurrent(conf.Get("font_size"))
	next := cur + delta
	if next < 1 {
		next = 1
	}
	conf.Set("font_size", formatFontSize(next))
	res, err := apply.Save(conf)
	if err != nil {
		return c.fail("save: "+err.Error(), 1)
	}
	if c.json {
		c.emit(map[string]any{
			"font_size": next,
			"previous":  cur,
			"reloaded":  res.Reloaded,
		})
		return 0
	}
	fmt.Printf("font_size %s → %s%s\n",
		formatFontSize(cur), formatFontSize(next), reloadHint(res.Reloaded))
	return 0
}

// formatFontSize prints a font size with one decimal place to match kitty's
// usual style ("13.0", "14.5") but trims trailing zero after the dot for
// integer-ish values so we don't write "13.00".
func formatFontSize(v float64) string {
	s := strconv.FormatFloat(v, 'f', 1, 64)
	return s
}

// ─── small helpers for install commands ───────────────────────────────────

func extractInstallFlags(args []string) (yes bool, name string, rest []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--yes" || a == "-y":
			yes = true
		case a == "--name":
			if i+1 < len(args) {
				name = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--name="):
			name = strings.TrimPrefix(a, "--name=")
		default:
			rest = append(rest, a)
		}
	}
	return
}

func confirm(prompt string) bool {
	fmt.Print(prompt, " [y/N] ")
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil {
		return false
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "y")
}

// streamFile copies up to maxLines lines from path to w, then truncates with
// an "(N more lines)" footer if longer.
func streamFile(w *os.File, path string, maxLines int) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	count := 0
	for sc.Scan() {
		if count >= maxLines {
			break
		}
		fmt.Fprintln(w, sc.Text())
		count++
	}
	if err := sc.Err(); err != nil {
		return err
	}
	rest := 0
	for sc.Scan() {
		rest++
	}
	if rest > 0 {
		fmt.Fprintf(w, "  …(%d more lines)\n", rest)
	}
	return nil
}

// ─── font ────────────────────────────────────────────────────────────────

func (c *cli) cmdFont(args []string) int {
	if len(args) == 0 {
		return c.fail("usage: psps font set <family> | psps font size <n>", 2)
	}
	dry, rest := extractDryRun(args)
	if len(rest) < 2 {
		return c.fail("usage: psps font set <family> | psps font size <n>", 2)
	}
	conf, code := c.loadConf()
	if code != 0 {
		return code
	}
	before, _ := kconf.Load(conf.Path)

	switch rest[0] {
	case "set":
		conf.Set("font_family", strings.Join(rest[1:], " "))
	case "size":
		conf.Set("font_size", rest[1])
	default:
		return c.fail("unknown font subcommand: "+rest[0], 2)
	}

	if dry {
		changes := diff.Between(before, conf)
		if c.json {
			c.emitChanges(changes)
			return 0
		}
		fmt.Println(diff.Render(changes, useColor()))
		return 0
	}

	res, err := apply.Save(conf)
	if err != nil {
		return c.fail("save: "+err.Error(), 1)
	}
	if c.json {
		c.emit(map[string]any{
			"font_family": conf.Get("font_family"),
			"font_size":   conf.Get("font_size"),
			"reloaded":    res.Reloaded,
		})
		return 0
	}
	fmt.Printf("font updated%s\n", reloadHint(res.Reloaded))
	return 0
}

// ─── small helpers ────────────────────────────────────────────────────────

func extractDryRun(args []string) (bool, []string) {
	out := args[:0:0]
	dry := false
	for _, a := range args {
		switch a {
		case "--dry-run", "-n":
			dry = true
		default:
			out = append(out, a)
		}
	}
	return dry, out
}

func useColor() bool {
	if v := os.Getenv("NO_COLOR"); v != "" {
		return false
	}
	fi, _ := os.Stdout.Stat()
	return fi != nil && (fi.Mode()&os.ModeCharDevice) != 0
}

// swatchOrder picks the ANSI palette keys we render as colored blocks. color0
// (background-ish black) is skipped because it usually matches the terminal
// background and reads as a void; color7 (foreground-ish white) is kept for
// contrast. Six accent colors is wide enough to convey theme character without
// dominating the row.
var swatchOrder = []string{"color1", "color2", "color3", "color4", "color5", "color6"}

// renderSwatch returns a small colored row that shows the theme's accent
// palette. When color is false (NO_COLOR, redirected stdout) we emit a plain
// row of "#" so the column still aligns.
func renderSwatch(t themelib.Theme, color bool) string {
	const block = "██"
	if !color {
		return strings.Repeat("##", len(swatchOrder))
	}
	var b strings.Builder
	for _, k := range swatchOrder {
		hex := t.Colors[k]
		if hex == "" {
			b.WriteString("  ")
			continue
		}
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(hex)).Render(block))
	}
	return b.String()
}

func (c *cli) applyThemeByName(name string, dry bool) int {
	t, err := themelib.Find(name)
	if err != nil {
		return c.fail(err.Error(), 1)
	}
	conf, code := c.loadConf()
	if code != 0 {
		return code
	}
	if dry {
		before, _ := kconf.Load(conf.Path)
		themelib.SetColors(conf, t)
		changes := diff.Between(before, conf)
		if c.json {
			c.emitChanges(changes)
			return 0
		}
		fmt.Println(diff.Render(changes, useColor()))
		return 0
	}
	res, err := themelib.Apply(conf, t)
	if err != nil {
		return c.fail("apply: "+err.Error(), 1)
	}
	if c.json {
		c.emit(map[string]any{
			"applied":  t.Name,
			"path":     t.Path,
			"reloaded": res.Reloaded,
		})
		return 0
	}
	fmt.Printf("applied theme %s%s\n", t.Name, reloadHint(res.Reloaded))
	return 0
}

// ─── session ─────────────────────────────────────────────────────────────

func (c *cli) cmdSession(args []string) int {
	if len(args) == 0 {
		return c.fail("usage: psps session save|list|restore|delete [name]", 2)
	}
	switch args[0] {
	case "save":
		name := sessionlib.AutoName
		if len(args) >= 2 {
			name = args[1]
		}
		s, err := sessionlib.Save(name)
		if err != nil {
			return c.fail("session save: "+err.Error(), 1)
		}
		if c.json {
			c.emit(map[string]any{"saved": s.Name, "path": s.Path})
			return 0
		}
		fmt.Printf("saved %s → %s\n", s.Name, s.Path)
		return 0
	case "list":
		entries, err := sessionlib.List()
		if err != nil {
			return c.fail("session list: "+err.Error(), 1)
		}
		if c.json {
			out := make([]map[string]any, 0, len(entries))
			for _, s := range entries {
				out = append(out, map[string]any{
					"name":     s.Name,
					"path":     s.Path,
					"modified": s.Mod.UTC().Format(time.RFC3339),
				})
			}
			c.emit(map[string]any{"sessions": out})
			return 0
		}
		if len(entries) == 0 {
			fmt.Println("no saved sessions")
			return 0
		}
		for _, s := range entries {
			fmt.Printf("%s  %-24s  %s\n",
				s.Mod.Format("2006-01-02 15:04:05"), s.Name, s.Path)
		}
		return 0
	case "restore":
		if len(args) < 2 {
			return c.fail("usage: psps session restore <name>", 2)
		}
		if err := sessionlib.Restore(args[1]); err != nil {
			return c.fail("restore: "+err.Error(), 1)
		}
		if c.json {
			c.emit(map[string]any{"restored": args[1]})
			return 0
		}
		fmt.Printf("launched kitty with session %q\n", args[1])
		return 0
	case "delete":
		if len(args) < 2 {
			return c.fail("usage: psps session delete <name>", 2)
		}
		if err := sessionlib.Delete(args[1]); err != nil {
			return c.fail("delete: "+err.Error(), 1)
		}
		if c.json {
			c.emit(map[string]any{"deleted": args[1]})
			return 0
		}
		fmt.Printf("deleted session %q\n", args[1])
		return 0
	}
	return c.fail("unknown session subcommand: "+args[0], 2)
}

// ─── autosave ─────────────────────────────────────────────────────────────

func (c *cli) cmdAutosave(args []string) int {
	if len(args) == 0 {
		return c.fail("usage: psps autosave enable|disable|run", 2)
	}
	switch args[0] {
	case "enable":
		return c.cmdAutosaveEnable()
	case "disable":
		return c.cmdAutosaveDisable()
	case "run":
		return c.cmdAutosaveRun(args[1:])
	}
	return c.fail("unknown autosave subcommand: "+args[0], 2)
}

func (c *cli) cmdAutosaveEnable() int {
	conf, code := c.loadConf()
	if code != 0 {
		return code
	}
	conf.Set("startup_session", sessionlib.PathFor(sessionlib.AutoName))
	if _, err := apply.Save(conf); err != nil {
		return c.fail("save kitty.conf: "+err.Error(), 1)
	}
	var snapshotPath, snapshotErr string
	if s, err := sessionlib.Save(sessionlib.AutoName); err != nil {
		snapshotErr = err.Error()
	} else {
		snapshotPath = s.Path
	}
	if c.json {
		out := map[string]any{
			"autosave":        "enabled",
			"startup_session": sessionlib.PathFor(sessionlib.AutoName),
		}
		if snapshotPath != "" {
			out["snapshot"] = snapshotPath
		}
		if snapshotErr != "" {
			out["warning"] = "initial snapshot failed: " + snapshotErr + " (set `allow_remote_control yes` in kitty.conf, then re-run)"
		}
		c.emit(out)
		return 0
	}
	if snapshotErr != "" {
		fmt.Fprintln(os.Stderr, "warning: initial snapshot failed:", snapshotErr)
		fmt.Fprintln(os.Stderr, "  (set `allow_remote_control yes` in kitty.conf, then re-run)")
	} else {
		fmt.Println("snapshot →", snapshotPath)
	}
	fmt.Println("startup_session points at the auto session — next kitty launch will restore it.")
	fmt.Println("to keep the snapshot fresh while you work, run in background:")
	fmt.Println("  nohup psps autosave run >/dev/null 2>&1 &")
	return 0
}

func (c *cli) cmdAutosaveDisable() int {
	conf, code := c.loadConf()
	if code != 0 {
		return code
	}
	cur := conf.Get("startup_session")
	if cur == "" {
		if c.json {
			c.emit(map[string]any{"autosave": "already-disabled"})
			return 0
		}
		fmt.Println("startup_session was not set — nothing to do")
		return 0
	}
	// Remove the directive by writing an empty value. kitty treats an empty
	// startup_session as "no session" — and the directive becomes a noop on
	// the next save pass either way. The simpler alternative would be to
	// physically drop the line; that's more invasive on the line list.
	conf.Set("startup_session", "none")
	if _, err := apply.Save(conf); err != nil {
		return c.fail("save kitty.conf: "+err.Error(), 1)
	}
	if c.json {
		c.emit(map[string]any{"autosave": "disabled"})
		return 0
	}
	fmt.Println("startup_session cleared (set to `none`)")
	fmt.Println("if you also want the daemon stopped: pkill -f 'psps autosave run'")
	return 0
}

func (c *cli) cmdAutosaveRun(args []string) int {
	interval := 15 * time.Second
	for _, a := range args {
		if strings.HasPrefix(a, "--interval=") {
			d, err := time.ParseDuration(strings.TrimPrefix(a, "--interval="))
			if err != nil {
				return c.fail("bad --interval value: "+err.Error(), 2)
			}
			interval = d
		}
	}
	if interval < time.Second {
		interval = time.Second
	}

	// Catch SIGTERM/SIGINT so the loop exits cleanly.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	fmt.Fprintf(os.Stderr, "autosave: snapshotting %q every %s (Ctrl-C to stop)\n",
		sessionlib.AutoName, interval)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	save := func() {
		if _, err := sessionlib.Save(sessionlib.AutoName); err != nil {
			fmt.Fprintln(os.Stderr, "autosave:", err)
			return
		}
		// Quiet success — uncomment if you want noisy heartbeats.
		// fmt.Fprintln(os.Stderr, "autosave: snapshot", time.Now().Format(time.RFC3339))
	}
	save() // do one immediately so the file exists

	for {
		select {
		case <-stop:
			fmt.Fprintln(os.Stderr, "autosave: stopping")
			return 0
		case <-ticker.C:
			save()
		}
	}
}
