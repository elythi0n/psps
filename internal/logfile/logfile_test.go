package logfile

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// resetForTest re-initializes the package-level singleton so each test gets a
// fresh logger pointed at its tmp dir. Tests in this file must run serially.
func resetForTest(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	once = sync.Once{}
	logger = nil
	path = ""
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
	// Simulate a second process run by re-resetting `once` but keeping HOME.
	once = sync.Once{}
	logger = nil
	path = ""
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
