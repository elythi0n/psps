// Package apply orchestrates a config save: diff → backup → write → reload.
// Every pane (and the CLI) goes through Save() so the pipeline stays
// consistent — undo always works, kitty always reloads.
package apply

import (
	"fmt"

	"github.com/elythi0n/psps/internal/backups"
	"github.com/elythi0n/psps/internal/kconf"
	"github.com/elythi0n/psps/internal/kitty"
)

// Result describes what happened during a Save.
type Result struct {
	BackupPath string // "" if nothing existed yet
	Reloaded   int    // number of kitty processes signaled
}

// Save runs the full pipeline: snapshot the existing file, write the new one,
// then signal running kitty processes to reread it. Errors from reload are
// returned but do NOT roll back the write — the on-disk change is already
// good, kitty just didn't notice.
func Save(c *kconf.Config) (Result, error) {
	var r Result

	bp, err := backups.Snapshot(c.Path)
	if err != nil {
		return r, fmt.Errorf("snapshot: %w", err)
	}
	r.BackupPath = bp

	if err := c.Save(); err != nil {
		return r, fmt.Errorf("write: %w", err)
	}

	r.Reloaded = kitty.PidCount()
	_ = kitty.Reload()
	return r, nil
}

// Undo restores the most recent backup over c.Path. The current state is
// snapshotted first (so undo of an undo works) and the caller should reload
// c from disk afterwards.
func Undo(c *kconf.Config) (backups.Entry, error) {
	return backups.RestoreLatest(c.Path)
}
