package userconf

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConf(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write conf: %v", err)
	}
	return p
}

func TestLoad_MissingFileIsZeroSettings(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if s.LogEnabled {
		t.Errorf("LogEnabled = true, want false for missing file")
	}
}

func TestLoad_EmptyPathIsZeroSettings(t *testing.T) {
	s, err := Load("")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if s.LogEnabled {
		t.Errorf("LogEnabled = true, want false for empty path")
	}
}

func TestLoad_ParsesLogOn(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"equals on", "log = on\n", true},
		{"kitty style", "log on\n", true},
		{"yes", "log=yes\n", true},
		{"true", "log = true\n", true},
		{"1", "log=1\n", true},
		{"off explicit", "log = off\n", false},
		{"commented", "# log = on\n", false},
		{"inline comment", "log = on   # turn it on\n", true},
		{"unknown key ignored", "frobnicate = yes\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Load(writeConf(t, tc.body))
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if s.LogEnabled != tc.want {
				t.Errorf("LogEnabled = %v, want %v (body=%q)", s.LogEnabled, tc.want, tc.body)
			}
		})
	}
}

func TestDefault_RespectsXDGConfigHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdgtest")
	if got, want := Default(), "/tmp/xdgtest/psps/config"; got != want {
		t.Errorf("Default() = %q, want %q", got, want)
	}
}

func TestDefault_FallsBackToHome(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", "")
	if got, want := Default(), filepath.Join(tmp, ".config", "psps", "config"); got != want {
		t.Errorf("Default() = %q, want %q", got, want)
	}
}
