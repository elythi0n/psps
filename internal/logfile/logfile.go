// Package logfile is a tiny append-only logger for psps. Errors and shell-out
// diagnostics are written to $XDG_STATE_HOME/psps/psps.log so the user can
// see exactly what failed after the TUI's transient status messages clear.
//
// The logger initialises lazily on first call; if the state dir can't be
// created (read-only home, permission denied) it silently downgrades to a
// no-op rather than crashing the TUI.
package logfile

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
)

var (
	once   sync.Once
	logger *log.Logger
	path   string
)

func ensureOpen() {
	once.Do(func() {
		dir := stateDir()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			logger = log.New(io.Discard, "", 0)
			return
		}
		path = filepath.Join(dir, "psps.log")
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			logger = log.New(io.Discard, "", 0)
			path = ""
			return
		}
		logger = log.New(f, "", log.LstdFlags|log.Lmicroseconds)
	})
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

// Path returns the on-disk log path, or "" if the logger couldn't open a
// file. Useful for tacking onto user-facing error messages.
func Path() string {
	ensureOpen()
	return path
}

// Errorf appends a line tagged ERR.
func Errorf(format string, args ...any) {
	ensureOpen()
	_ = logger.Output(2, "ERR  "+fmt.Sprintf(format, args...))
}

// Infof appends a line tagged INFO.
func Infof(format string, args ...any) {
	ensureOpen()
	_ = logger.Output(2, "INFO "+fmt.Sprintf(format, args...))
}
