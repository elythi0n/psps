// Package backups keeps a rolling history of kitty.conf snapshots so any save
// can be undone. Snapshots live next to the config in a hidden subdirectory.
package backups

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const MaxKeep = 50

// Snapshot copies path to ~/.config/kitty/.backups/kitty.conf.YYYYMMDD-HHMMSS
// and prunes oldest entries beyond MaxKeep. Returns the backup path.
//
// If the source doesn't exist yet (first-ever save), returns ("", nil) — no
// backup needed.
func Snapshot(path string) (string, error) {
	src, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	defer func() { _ = src.Close() }()

	dir := backupDir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	base := filepath.Base(path)
	dest := filepath.Join(dir, fmt.Sprintf("%s.%s", base, time.Now().Format("20060102-150405")))

	dst, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		_ = os.Remove(dest)
		return "", err
	}
	if err := dst.Close(); err != nil {
		return "", err
	}

	prune(dir, base)
	return dest, nil
}

type Entry struct {
	Path string
	Name string
	When time.Time
}

// List returns snapshots for path in newest-first order.
func List(path string) ([]Entry, error) {
	dir := backupDir(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	base := filepath.Base(path)
	var out []Entry
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), base+".") {
			continue
		}
		info, _ := e.Info()
		out = append(out, Entry{
			Path: filepath.Join(dir, e.Name()),
			Name: e.Name(),
			When: info.ModTime(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].When.After(out[j].When) })
	return out, nil
}

// RestoreLatest copies the most recent snapshot back over path. Returns the
// snapshot that was restored, or an error if nothing is available.
func RestoreLatest(path string) (Entry, error) {
	entries, err := List(path)
	if err != nil {
		return Entry{}, err
	}
	if len(entries) == 0 {
		return Entry{}, fmt.Errorf("no backups for %s", path)
	}
	return entries[0], Restore(path, entries[0].Path)
}

// Restore copies backupPath onto target atomically (write to tmp, rename).
// A fresh snapshot of the current target is taken first so an undo is itself
// undoable.
func Restore(target, backupPath string) error {
	if _, err := Snapshot(target); err != nil {
		return fmt.Errorf("pre-restore snapshot: %w", err)
	}
	src, err := os.Open(backupPath)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	tmp := target + ".tmp"
	dst, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, target)
}

func backupDir(path string) string {
	return filepath.Join(filepath.Dir(path), ".backups")
}

func prune(dir, base string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var keep []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), base+".") {
			keep = append(keep, e)
		}
	}
	if len(keep) <= MaxKeep {
		return
	}
	sort.Slice(keep, func(i, j int) bool {
		ai, _ := keep[i].Info()
		aj, _ := keep[j].Info()
		return ai.ModTime().Before(aj.ModTime())
	})
	for _, e := range keep[:len(keep)-MaxKeep] {
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
}
