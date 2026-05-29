// Package logfile is a tiny append-only logger for psps. Errors and shell-out
// diagnostics are written to $XDG_STATE_HOME/psps/psps.log so the user can
// see exactly what failed after the TUI's transient status messages clear.
//
// Logging is OFF by default — call SetEnabled(true) before any Errorf/Infof
// to opt in. When disabled, every API call is a no-op and no file is created.
// If the state dir can't be written (read-only home, permission denied) the
// logger silently downgrades to a no-op rather than crashing the TUI.
package logfile

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

var (
	enabled atomic.Bool

	mu     sync.Mutex
	logger *log.Logger
	path   string
)

// SetEnabled flips logging on or off. Safe to call from any goroutine; takes
// effect on the next Errorf/Infof/Path call. Disabling does NOT close an
// already-opened file (no point — log.Logger holds the fd internally and
// won't be used again until SetEnabled(true) returns).
func SetEnabled(v bool) { enabled.Store(v) }

// IsEnabled reports the current gate. Useful for tests and conditional
// "logged to <path>" hints in user-facing output.
func IsEnabled() bool { return enabled.Load() }

// ensureOpen returns true if the logger is ready to receive writes. Lazily
// opens the file on first call after enabling. Returns false (without
// touching the filesystem) when disabled.
func ensureOpen() bool {
	if !enabled.Load() {
		return false
	}
	mu.Lock()
	defer mu.Unlock()
	if logger != nil {
		return true
	}
	dir := stateDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false
	}
	p := filepath.Join(dir, "psps.log")
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return false
	}
	logger = log.New(f, "", log.LstdFlags|log.Lmicroseconds)
	path = p
	return true
}

// stateDir returns the directory where the log file lives. Respects
// $XDG_STATE_HOME if set; otherwise falls back to ~/.local/state/psps.
func stateDir() string {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "psps")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "psps")
	}
	return filepath.Join(home, ".local", "state", "psps")
}

// Path returns the on-disk log path. Empty string when logging is disabled
// or the file couldn't be opened — callers should treat "" as "no log to
// point the user at".
func Path() string {
	if !ensureOpen() {
		return ""
	}
	return path
}

// Errorf appends a line tagged ERR. No-op when logging is disabled.
func Errorf(format string, args ...any) {
	if !ensureOpen() {
		return
	}
	_ = logger.Output(2, "ERR  "+fmt.Sprintf(format, args...))
}

// Infof appends a line tagged INFO. No-op when logging is disabled.
func Infof(format string, args ...any) {
	if !ensureOpen() {
		return
	}
	_ = logger.Output(2, "INFO "+fmt.Sprintf(format, args...))
}
