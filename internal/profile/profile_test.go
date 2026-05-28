package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elythi0n/psps/internal/kconf"
)

func withTempProfilesDir(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	return filepath.Join(tmp, "psps", "profiles")
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInstall_FromStagedRoot(t *testing.T) {
	_ = withTempProfilesDir(t)
	staging := t.TempDir()
	writeFile(t, filepath.Join(staging, "theme.conf"), "background #112233\n")
	writeFile(t, filepath.Join(staging, "fonts.conf"), "font_family JetBrains Mono\nfont_size 14\n")

	p, err := Install(staging, "darkmode")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if p.Name != "darkmode" {
		t.Errorf("name: got %q, want darkmode", p.Name)
	}
	if !p.Has(ComponentTheme) {
		t.Error("theme component missing from installed profile")
	}
	if !p.Has(ComponentFonts) {
		t.Error("fonts component missing")
	}
	if p.Has(ComponentSettings) {
		t.Error("settings should NOT be present (we didn't stage it)")
	}
}

func TestInstall_FromSubdirectory(t *testing.T) {
	// Git repos often wrap the profile in a top-level directory. Install
	// should look one level deep when the staging root has no component
	// files of its own.
	_ = withTempProfilesDir(t)
	staging := t.TempDir()
	writeFile(t, filepath.Join(staging, "README.md"), "")
	writeFile(t, filepath.Join(staging, "darkmode-profile", "theme.conf"), "background #000\n")
	p, err := Install(staging, "darkmode")
	if err != nil {
		t.Fatalf("Install with subdir: %v", err)
	}
	if !p.Has(ComponentTheme) {
		t.Error("theme component not found from subdirectory layout")
	}
}

func TestInstall_RejectsDuplicate(t *testing.T) {
	_ = withTempProfilesDir(t)
	staging := t.TempDir()
	writeFile(t, filepath.Join(staging, "theme.conf"), "background #000\n")
	if _, err := Install(staging, "dup"); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(staging, "dup"); err == nil {
		t.Fatal("duplicate install should error")
	}
}

func TestInstall_NoComponentsError(t *testing.T) {
	_ = withTempProfilesDir(t)
	staging := t.TempDir()
	writeFile(t, filepath.Join(staging, "README.md"), "")
	_, err := Install(staging, "empty")
	if err == nil {
		t.Fatal("expected error when staging dir has no component files")
	}
	if !strings.Contains(err.Error(), "no profile component files") {
		t.Errorf("error should mention missing components, got: %v", err)
	}
}

func TestList_OrderedByName(t *testing.T) {
	_ = withTempProfilesDir(t)
	for _, n := range []string{"zeta", "alpha", "middle"} {
		staging := t.TempDir()
		writeFile(t, filepath.Join(staging, "theme.conf"), "background #000\n")
		if _, err := Install(staging, n); err != nil {
			t.Fatal(err)
		}
	}
	got, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Name != "alpha" || got[2].Name != "zeta" {
		t.Errorf("List order wrong: %+v", got)
	}
}

func TestRemove_DeletesProfileDir(t *testing.T) {
	_ = withTempProfilesDir(t)
	staging := t.TempDir()
	writeFile(t, filepath.Join(staging, "theme.conf"), "background #000\n")
	p, err := Install(staging, "rm-me")
	if err != nil {
		t.Fatal(err)
	}
	if err := Remove("rm-me"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(p.Dir); !os.IsNotExist(err) {
		t.Errorf("profile dir still present after Remove: %v", err)
	}
}

// Apply integration: stand up a fake kitty.conf in a tmp dir, then verify the
// applied profile's directives appear in it and ones not in the profile are
// preserved untouched.
func TestApply_MergesComponentsSelectively(t *testing.T) {
	_ = withTempProfilesDir(t)

	// Build a starter kitty.conf with one pre-existing directive in each
	// component domain.
	confPath := filepath.Join(t.TempDir(), "kitty.conf")
	starter := strings.Join([]string{
		"# starter",
		"background #ffffff",        // theme domain
		"background_opacity 0.5",    // settings domain
		"font_family OldFont",       // fonts domain
		"map ctrl+a select_all",     // keybinds domain
		"# trailing comment",
	}, "\n") + "\n"
	if err := os.WriteFile(confPath, []byte(starter), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := kconf.Load(confPath)
	if err != nil {
		t.Fatal(err)
	}

	// Profile that touches only theme + fonts. settings and keybinds in the
	// starter must survive untouched.
	staging := t.TempDir()
	writeFile(t, filepath.Join(staging, "theme.conf"), "background #000000\nforeground #ffffff\n")
	writeFile(t, filepath.Join(staging, "fonts.conf"), "font_family JetBrains Mono\nfont_size 14\n")
	if _, err := Install(staging, "partial"); err != nil {
		t.Fatal(err)
	}
	p, err := Load("partial")
	if err != nil {
		t.Fatal(err)
	}

	// Apply directly to the config and verify in-memory state without
	// triggering kitty reload by calling applyComponent for each file.
	// (Apply() also saves via apply.Save which would try to backup a path
	// outside our scope.)
	for _, comp := range Components {
		if path, ok := p.Files[comp]; ok {
			if err := applyComponent(c, comp, path); err != nil {
				t.Fatalf("applyComponent %s: %v", comp, err)
			}
		}
	}

	if got := c.Get("background"); got != "#000000" {
		t.Errorf("theme not applied: background = %q, want #000000", got)
	}
	if got := c.Get("font_family"); got != "JetBrains Mono" {
		t.Errorf("font not applied: %q", got)
	}
	// settings domain: profile didn't touch background_opacity; must persist.
	if got := c.Get("background_opacity"); got != "0.5" {
		t.Errorf("settings clobbered: background_opacity = %q, want 0.5", got)
	}
	// keybinds domain: profile didn't touch the existing map; must persist.
	var sawMap bool
	for _, m := range c.Maps() {
		if m.Key == "ctrl+a" && m.Value == "select_all" {
			sawMap = true
		}
	}
	if !sawMap {
		t.Errorf("keybinds clobbered: existing map missing from Maps()")
	}
}

// Apply test for keybinds component: the keybinds.conf must install map
// directives without clobbering directives in other domains.
func TestApply_KeybindsComponent(t *testing.T) {
	_ = withTempProfilesDir(t)
	confPath := filepath.Join(t.TempDir(), "kitty.conf")
	if err := os.WriteFile(confPath, []byte("background #ffffff\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := kconf.Load(confPath)
	if err != nil {
		t.Fatal(err)
	}

	staging := t.TempDir()
	writeFile(t, filepath.Join(staging, "keybinds.conf"),
		"map ctrl+c copy_to_clipboard\nmap ctrl+v paste_from_clipboard\n")
	if _, err := Install(staging, "kb"); err != nil {
		t.Fatal(err)
	}
	p, _ := Load("kb")
	if err := applyComponent(c, ComponentKeybinds, p.Files[ComponentKeybinds]); err != nil {
		t.Fatal(err)
	}

	var copyMap, pasteMap bool
	for _, m := range c.Maps() {
		if m.Key == "ctrl+c" {
			copyMap = true
		}
		if m.Key == "ctrl+v" {
			pasteMap = true
		}
	}
	if !copyMap || !pasteMap {
		t.Errorf("expected ctrl+c and ctrl+v maps installed; got %v", c.Maps())
	}
	// Other directives untouched.
	if got := c.Get("background"); got != "#ffffff" {
		t.Errorf("theme clobbered by keybinds-only profile: %q", got)
	}
}
