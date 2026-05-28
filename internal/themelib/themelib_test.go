package themelib

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elythi0n/psps/internal/kconf"
)

// withTempDataDir redirects Dir() to a fresh tmp via XDG_DATA_HOME so tests
// don't touch the real ~/.local/share/psps/themes/ directory.
func withTempDataDir(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	return filepath.Join(tmp, "psps", "themes")
}

func TestDir_FollowsXDGDataHome(t *testing.T) {
	want := withTempDataDir(t)
	if got := Dir(); got != want {
		t.Errorf("Dir() = %q, want %q", got, want)
	}
}

func TestEnsureSeeded_CreatesDirAndCopiesBundled(t *testing.T) {
	dir := withTempDataDir(t)
	if err := EnsureSeeded(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	for _, want := range []string{
		"Catppuccin-Mocha.conf", "Gruvbox-Dark.conf", "Nord.conf",
		"Rose-Pine.conf", "Tokyo-Night.conf", "Dracula.conf",
	} {
		if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
			t.Errorf("missing seeded theme %s: %v", want, err)
		}
	}
}

func TestEnsureSeeded_DoesNotOverwriteExistingFiles(t *testing.T) {
	dir := withTempDataDir(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-create a user-modified Nord.
	custom := "# my custom Nord override\nbackground #000000\nforeground #ffffff\n"
	if err := os.WriteFile(filepath.Join(dir, "Nord.conf"), []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSeeded(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "Nord.conf"))
	if string(got) != custom {
		t.Errorf("user-modified Nord was overwritten:\n%s", got)
	}
	// Other defaults still seeded.
	if _, err := os.Stat(filepath.Join(dir, "Dracula.conf")); err != nil {
		t.Errorf("Dracula should still be seeded: %v", err)
	}
}

func TestLoadAll_TagsBundledVsInstalled(t *testing.T) {
	_ = withTempDataDir(t)
	if err := EnsureSeeded(); err != nil {
		t.Fatal(err)
	}
	// Add a user-installed theme.
	custom := filepath.Join(Dir(), "MyCustom.conf")
	if err := os.WriteFile(custom, []byte("background #112233\nforeground #aabbcc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	themes, err := LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	var sawBundled, sawInstalled bool
	for _, th := range themes {
		switch th.Name {
		case "Dracula":
			if th.Source != "bundled" {
				t.Errorf("Dracula source = %q, want bundled", th.Source)
			}
			sawBundled = true
		case "MyCustom":
			if th.Source != "installed" {
				t.Errorf("MyCustom source = %q, want installed", th.Source)
			}
			sawInstalled = true
		}
	}
	if !sawBundled {
		t.Error("expected to find bundled Dracula in LoadAll")
	}
	if !sawInstalled {
		t.Error("expected to find installed MyCustom in LoadAll")
	}
}

func TestLoadAll_ParsesColorDirectives(t *testing.T) {
	_ = withTempDataDir(t)
	if err := EnsureSeeded(); err != nil {
		t.Fatal(err)
	}
	themes, _ := LoadAll()
	for _, th := range themes {
		if th.Name != "Dracula" {
			continue
		}
		if th.Colors["background"] != "#282A36" {
			t.Errorf("Dracula background = %q, want #282A36", th.Colors["background"])
		}
		if th.Colors["color0"] != "#21222C" {
			t.Errorf("Dracula color0 = %q, want #21222C", th.Colors["color0"])
		}
		return
	}
	t.Fatal("Dracula not found")
}

func TestInstall_CopiesFileAndTagsInstalled(t *testing.T) {
	_ = withTempDataDir(t)
	src := filepath.Join(t.TempDir(), "Solarized-Dark.conf")
	if err := os.WriteFile(src, []byte("background #002b36\nforeground #839496\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	th, err := Install(src)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if th.Name != "Solarized-Dark" {
		t.Errorf("name: got %q, want Solarized-Dark", th.Name)
	}
	if th.Source != "installed" {
		t.Errorf("source: got %q, want installed", th.Source)
	}
	if th.Colors["background"] != "#002b36" {
		t.Errorf("colors not parsed correctly: %v", th.Colors)
	}
	if _, err := os.Stat(th.Path); err != nil {
		t.Errorf("file not present at %s: %v", th.Path, err)
	}
}

func TestInstall_RejectsDuplicate(t *testing.T) {
	_ = withTempDataDir(t)
	src := filepath.Join(t.TempDir(), "X.conf")
	if err := os.WriteFile(src, []byte("background #111111\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(src); err != nil {
		t.Fatal(err)
	}
	_, err := Install(src)
	if err == nil {
		t.Fatal("second install of same name should error")
	}
	if !strings.Contains(err.Error(), "already installed") {
		t.Errorf("expected duplicate-install message, got: %v", err)
	}
}

func TestRemove_DeletesFile(t *testing.T) {
	_ = withTempDataDir(t)
	if err := EnsureSeeded(); err != nil {
		t.Fatal(err)
	}
	if err := Remove("Dracula"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(Dir(), "Dracula.conf")); !os.IsNotExist(err) {
		t.Errorf("Dracula.conf still present after Remove (err = %v)", err)
	}
}

func TestRemove_UnknownNameErrors(t *testing.T) {
	_ = withTempDataDir(t)
	if err := Remove("NotAThing"); err == nil {
		t.Fatal("expected error for unknown theme")
	}
}

// Pins the contract that applying a theme leaves non-color directives —
// in particular font_size — untouched. This is what makes `psps zoom set N`
// survive a subsequent `psps theme apply`: only color* / background /
// foreground / cursor* / selection_* / url_color keys get rewritten.
func TestApply_DoesNotTouchFontSizeOrUserDirectives(t *testing.T) {
	_ = withTempDataDir(t)
	if err := EnsureSeeded(); err != nil {
		t.Fatal(err)
	}

	// Stand up a kitty.conf with a zoom and an unrelated setting; load it
	// into the in-memory model the way the real app does.
	confPath := filepath.Join(t.TempDir(), "kitty.conf")
	starter := strings.Join([]string{
		"# user config",
		"font_family JetBrains Mono",
		"font_size 18.5",
		"background_opacity 0.85",
		"background #ffffff", // theme will overwrite this
		"map ctrl+a select_all",
	}, "\n") + "\n"
	if err := os.WriteFile(confPath, []byte(starter), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := kconf.Load(confPath)
	if err != nil {
		t.Fatal(err)
	}

	dracula, err := Find("Dracula")
	if err != nil {
		t.Fatal(err)
	}

	// Apply the theme directly to the in-memory config — we don't want to
	// invoke the full save pipeline (which would try to reload kitty).
	SetColors(c, dracula)

	if got := c.Get("font_size"); got != "18.5" {
		t.Errorf("font_size lost: got %q, want 18.5", got)
	}
	if got := c.Get("font_family"); got != "JetBrains Mono" {
		t.Errorf("font_family lost: got %q", got)
	}
	if got := c.Get("background_opacity"); got != "0.85" {
		t.Errorf("background_opacity lost: got %q", got)
	}
	if got := c.Get("background"); got != "#282A36" {
		t.Errorf("background not updated by theme: got %q, want #282A36", got)
	}
	// keybinds preserved
	var sawMap bool
	for _, m := range c.Maps() {
		if m.Key == "ctrl+a" && m.Value == "select_all" {
			sawMap = true
		}
	}
	if !sawMap {
		t.Error("existing keybind clobbered by theme apply")
	}
}
