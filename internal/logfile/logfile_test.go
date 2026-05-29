package logfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resetForTest re-initializes the package-level singleton so each test gets a
// fresh logger pointed at its tmp dir. Tests in this file must run serially.
// Logging is enabled by default for tests that exercise write paths; tests
// covering the disabled state call SetEnabled(false) themselves.
func resetForTest(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	mu.Lock()
	logger = nil
	path = ""
	mu.Unlock()
	SetEnabled(true)
	return filepath.Join(tmp, "psps", "psps.log")
}

func TestPath_PointsAtXDGStateHome(t *testing.T) {
	want := resetForTest(t)
	if got := Path(); got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
}

func TestErrorfAndInfof_PersistToFile(t *testing.T) {
	want := resetForTest(t)
	Errorf("boom at %d", 42)
	Infof("starting %s", "psps")

	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "ERR  boom at 42") {
		t.Errorf("ERR line missing from log:\n%s", body)
	}
	if !strings.Contains(body, "INFO starting psps") {
		t.Errorf("INFO line missing from log:\n%s", body)
	}
}

func TestLog_AppendsAcrossRuns(t *testing.T) {
	want := resetForTest(t)
	Infof("first")
	// Simulate a second process run by re-zeroing the lazy state but keeping
	// the state-home env so we point at the same file.
	mu.Lock()
	logger = nil
	path = ""
	mu.Unlock()
	Infof("second")

	data, _ := os.ReadFile(want)
	body := string(data)
	if !strings.Contains(body, "first") || !strings.Contains(body, "second") {
		t.Errorf("expected both lines preserved (append mode), got:\n%s", body)
	}
}

func TestStateDir_FallsBackToHomeWhenXDGUnset(t *testing.T) {
	// Clear XDG_STATE_HOME; verify we fall back to $HOME/.local/state/psps.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_STATE_HOME", "")
	if got, want := stateDir(), filepath.Join(tmp, ".local", "state", "psps"); got != want {
		t.Errorf("stateDir() = %q, want %q", got, want)
	}
}

func TestDisabled_NoFileCreated(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	mu.Lock()
	logger = nil
	path = ""
	mu.Unlock()
	SetEnabled(false)

	Errorf("should not write")
	Infof("should not write either")

	if p := Path(); p != "" {
		t.Errorf("Path() = %q, want empty when disabled", p)
	}
	// State dir creation is part of the open path — when disabled, we
	// shouldn't have created it.
	if _, err := os.Stat(filepath.Join(tmp, "psps")); err == nil {
		t.Errorf("state dir was created despite logging being disabled")
	}
}
