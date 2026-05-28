// Package themelib manages kitty colour themes as drop-in .conf files under
// $XDG_DATA_HOME/psps/themes/ (defaults to ~/.local/share/psps/themes/).
//
// Theme files are kitty-native: any file kitty itself can `include` is a valid
// theme. This means user-installed themes work in raw `kitty --config` too —
// psps isn't a parallel ecosystem.
//
// A small set of defaults (Catppuccin-Mocha, Gruvbox-Dark, Nord, Rose-Pine,
// Tokyo-Night, Dracula) is embedded in the binary and seeded into the themes
// dir on first run. Users can then add, remove, edit, or `psps theme install`
// more without us getting in the way. Existing user files are never
// overwritten by the seeder.
package themelib

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/elythi0n/psps/internal/apply"
	"github.com/elythi0n/psps/internal/kconf"
)

//go:embed bundled/*.conf
var bundled embed.FS

const bundledDir = "bundled"

// Theme represents one resolved theme file on disk.
type Theme struct {
	Name   string // filename without .conf
	Source string // "bundled" (filename matches an embedded default) or "installed"
	Path   string // absolute path on disk
	Colors map[string]string
}

// Dir returns the absolute path of the themes storage directory. Respects
// $XDG_DATA_HOME; falls back to ~/.local/share/psps/themes.
func Dir() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "psps", "themes")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "psps", "themes")
	}
	return filepath.Join(home, ".local", "share", "psps", "themes")
}

// EnsureSeeded creates the themes directory if missing and copies any embedded
// default that doesn't already exist on disk. User-created or user-modified
// files are never overwritten — a default only lands if the filename is absent
// entirely. Safe to call repeatedly; cheap once the dir is populated.
func EnsureSeeded() error {
	dir := Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create themes dir: %w", err)
	}
	entries, err := bundled.ReadDir(bundledDir)
	if err != nil {
		return nil // nothing to seed
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".conf") {
			continue
		}
		target := filepath.Join(dir, e.Name())
		if _, err := os.Stat(target); err == nil {
			continue // already present
		}
		data, err := bundled.ReadFile(bundledDir + "/" + e.Name())
		if err != nil {
			continue
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return fmt.Errorf("seed %s: %w", e.Name(), err)
		}
	}
	return nil
}

// bundledNames returns the set of theme filenames (without .conf) shipped in
// the binary. Used to tag a Theme's Source.
func bundledNames() map[string]struct{} {
	out := map[string]struct{}{}
	entries, err := bundled.ReadDir(bundledDir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".conf") {
			continue
		}
		out[strings.TrimSuffix(e.Name(), ".conf")] = struct{}{}
	}
	return out
}

// LoadAll walks the themes directory and returns every parseable theme.
// Seeds defaults if the directory is missing or empty.
func LoadAll() ([]Theme, error) {
	if err := EnsureSeeded(); err != nil {
		return nil, err
	}
	dir := Dir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read themes dir %s: %w", dir, err)
	}
	bundledSet := bundledNames()

	var out []Theme
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".conf") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".conf")
		source := "installed"
		if _, ok := bundledSet[name]; ok {
			source = "bundled"
		}
		t := parse(name, source, string(data))
		t.Path = path
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func Find(name string) (Theme, error) {
	all, err := LoadAll()
	if err != nil {
		return Theme{}, err
	}
	for _, t := range all {
		if strings.EqualFold(t.Name, name) {
			return t, nil
		}
	}
	return Theme{}, fmt.Errorf("theme %q not found — run `psps theme list`", name)
}

// Apply writes a theme's colour directives into the config and runs the full
// save pipeline (backup + write + reload).
func Apply(c *kconf.Config, t Theme) (apply.Result, error) {
	if len(t.Colors) == 0 {
		return apply.Result{}, fmt.Errorf("theme %q has no colors", t.Name)
	}
	for k, v := range t.Colors {
		c.Set(k, v)
	}
	return apply.Save(c)
}

// SetColors mutates c without saving — useful for previewing a theme change
// before commit (e.g. with --dry-run).
func SetColors(c *kconf.Config, t Theme) {
	for k, v := range t.Colors {
		c.Set(k, v)
	}
}

// Install copies a kitty-native .conf file from srcPath into the themes dir.
// The destination filename is derived from the source basename. Returns the
// installed Theme on success; errors if a theme with that name already exists
// (the caller decides whether to overwrite by removing first).
func Install(srcPath string) (Theme, error) {
	if err := EnsureSeeded(); err != nil {
		return Theme{}, err
	}
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return Theme{}, fmt.Errorf("read source: %w", err)
	}
	name := strings.TrimSuffix(filepath.Base(srcPath), filepath.Ext(srcPath))
	if name == "" {
		return Theme{}, fmt.Errorf("source path has no usable basename: %s", srcPath)
	}
	dst := filepath.Join(Dir(), name+".conf")
	if _, err := os.Stat(dst); err == nil {
		return Theme{}, fmt.Errorf("theme %q already installed at %s — remove it first", name, dst)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return Theme{}, fmt.Errorf("write %s: %w", dst, err)
	}
	t := parse(name, "installed", string(data))
	t.Path = dst
	return t, nil
}

// Remove deletes a theme file. Errors if the theme doesn't exist.
func Remove(name string) error {
	dst := filepath.Join(Dir(), name+".conf")
	if _, err := os.Stat(dst); err != nil {
		return fmt.Errorf("theme %q not found at %s", name, dst)
	}
	return os.Remove(dst)
}

func parse(name, source, body string) Theme {
	t := Theme{Name: name, Source: source, Colors: map[string]string{}}
	for _, line := range strings.Split(body, "\n") {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		fields := strings.Fields(s)
		if len(fields) < 2 {
			continue
		}
		k := fields[0]
		v := fields[1]
		if isColorKey(k) {
			t.Colors[k] = v
		}
	}
	return t
}

func isColorKey(k string) bool {
	switch k {
	case "background", "foreground", "cursor", "cursor_text_color",
		"selection_background", "selection_foreground", "url_color":
		return true
	}
	return strings.HasPrefix(k, "color") && len(k) <= 7
}
