// Package profile manages "profiles" — bundles of kitty configuration that
// can include any subset of {theme, settings, keybinds, fonts}.
//
// A profile lives at $XDG_DATA_HOME/psps/profiles/<name>/ and may contain:
//
//	theme.conf      kitty colour directives (background, foreground, color0…15, …)
//	settings.conf   kitty settings (background_opacity, scrollback_lines, …)
//	keybinds.conf   kitty map directives (map ctrl+c paste_from_clipboard …)
//	fonts.conf      font directives (font_family, font_size, …)
//
// Each component file is plain kitty-config syntax — readable directly via
// `kitty --config`. None of the four files is required; Apply silently skips
// any that aren't present, which is what makes profiles composable: install
// one profile for a theme, another for keybinds, and they don't clobber
// each other's domain.
package profile

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/elythi0n/psps/internal/apply"
	"github.com/elythi0n/psps/internal/kconf"
)

// Component identifies one of the four file types a profile can include.
type Component string

const (
	ComponentTheme    Component = "theme"
	ComponentSettings Component = "settings"
	ComponentKeybinds Component = "keybinds"
	ComponentFonts    Component = "fonts"
)

// Components is the ordered set of all four component types.
var Components = []Component{ComponentTheme, ComponentSettings, ComponentKeybinds, ComponentFonts}

// Filename returns the on-disk filename used for this component.
func (c Component) Filename() string { return string(c) + ".conf" }

// Profile describes one installed profile.
type Profile struct {
	Name string
	Dir  string
	// Files maps a component to its absolute path on disk. Only components
	// that exist in the profile are present here.
	Files map[Component]string
}

// Has reports whether this profile includes the given component.
func (p Profile) Has(c Component) bool {
	_, ok := p.Files[c]
	return ok
}

// Dir returns the absolute root path where profiles live.
func Dir() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "psps", "profiles")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "psps", "profiles")
	}
	return filepath.Join(home, ".local", "share", "psps", "profiles")
}

// List returns all installed profiles sorted by name.
func List() ([]Profile, error) {
	root := Dir()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []Profile
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p, err := Load(e.Name())
		if err != nil {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Load resolves a profile by name. Errors if the directory doesn't exist;
// returns a profile with an empty Files map if it exists but contains nothing
// recognisable.
func Load(name string) (Profile, error) {
	dir := filepath.Join(Dir(), name)
	info, err := os.Stat(dir)
	if err != nil {
		return Profile{}, fmt.Errorf("profile %q not found at %s", name, dir)
	}
	if !info.IsDir() {
		return Profile{}, fmt.Errorf("%s is not a directory", dir)
	}
	p := Profile{Name: name, Dir: dir, Files: map[Component]string{}}
	for _, c := range Components {
		path := filepath.Join(dir, c.Filename())
		if _, err := os.Stat(path); err == nil {
			p.Files[c] = path
		}
	}
	return p, nil
}

// Apply reads each component the profile contains and merges it into the
// user's kitty.conf via the existing save pipeline. Components missing from
// the profile are not touched — that's the selective-override property.
//
// The returned apply.Result is from the single save that writes the merged
// result; the kitty-reload count comes from kitty.Reload() inside apply.Save.
func Apply(p Profile, c *kconf.Config) (apply.Result, error) {
	if len(p.Files) == 0 {
		return apply.Result{}, fmt.Errorf("profile %q is empty — nothing to apply", p.Name)
	}
	for _, comp := range Components {
		path, ok := p.Files[comp]
		if !ok {
			continue
		}
		if err := applyComponent(c, comp, path); err != nil {
			return apply.Result{}, fmt.Errorf("apply %s: %w", comp, err)
		}
	}
	return apply.Save(c)
}

// applyComponent reads one component file and merges its directives into c.
// Theme/Settings/Fonts → regular directives (Set). Keybinds → map directives
// (SetMap).
func applyComponent(c *kconf.Config, comp Component, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// keybinds.conf is allowed to also mix in `map ...` lines; theme/
		// settings/fonts technically aren't, but if a directive named "map"
		// appears we still treat it correctly.
		if fields[0] == "map" && len(fields) >= 3 {
			c.SetMap(fields[1], strings.Join(fields[2:], " "))
			continue
		}
		c.Set(fields[0], strings.Join(fields[1:], " "))
	}
	return sc.Err()
}

// Install copies a staged directory into the profiles folder under the given
// name. The staging directory is expected to contain any subset of the four
// component files at its root. If the source directory contains nested
// subdirectories (e.g., a git repo with a top-level profile/ folder), we look
// one level deep for a directory that has at least one recognisable
// component file and use that.
//
// Errors if a profile with the same name already exists — caller decides on
// overwrite by removing first.
func Install(stagedDir, name string) (Profile, error) {
	if name == "" {
		return Profile{}, errors.New("profile name is required")
	}
	src, err := locateProfileRoot(stagedDir)
	if err != nil {
		return Profile{}, err
	}
	dst := filepath.Join(Dir(), name)
	if _, err := os.Stat(dst); err == nil {
		return Profile{}, fmt.Errorf("profile %q already installed at %s — remove it first", name, dst)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return Profile{}, err
	}
	copied := 0
	for _, c := range Components {
		srcFile := filepath.Join(src, c.Filename())
		if _, err := os.Stat(srcFile); err != nil {
			continue
		}
		if err := copyFile(srcFile, filepath.Join(dst, c.Filename())); err != nil {
			_ = os.RemoveAll(dst)
			return Profile{}, fmt.Errorf("copy %s: %w", c.Filename(), err)
		}
		copied++
	}
	if copied == 0 {
		_ = os.RemoveAll(dst)
		return Profile{}, fmt.Errorf("no recognisable component files in %s (expected one of: theme.conf, settings.conf, keybinds.conf, fonts.conf)", stagedDir)
	}
	return Load(name)
}

// locateProfileRoot finds the directory that actually holds the profile
// component files. Accepts: stagedDir itself (if any component file is at the
// root), or any first-level subdirectory that holds at least one.
func locateProfileRoot(stagedDir string) (string, error) {
	if hasAnyComponent(stagedDir) {
		return stagedDir, nil
	}
	entries, err := os.ReadDir(stagedDir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub := filepath.Join(stagedDir, e.Name())
		if hasAnyComponent(sub) {
			return sub, nil
		}
	}
	return "", fmt.Errorf("no profile component files under %s", stagedDir)
}

func hasAnyComponent(dir string) bool {
	for _, c := range Components {
		if _, err := os.Stat(filepath.Join(dir, c.Filename())); err == nil {
			return true
		}
	}
	return false
}

// Remove deletes a profile directory.
func Remove(name string) error {
	dst := filepath.Join(Dir(), name)
	if _, err := os.Stat(dst); err != nil {
		return fmt.Errorf("profile %q not found at %s", name, dst)
	}
	return os.RemoveAll(dst)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
