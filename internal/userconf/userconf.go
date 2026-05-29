// Package userconf reads psps's own settings file — distinct from kitty.conf,
// which is the config psps manages on behalf of kitty. Lives at
// $XDG_CONFIG_HOME/psps/config (or ~/.config/psps/config) and uses a tiny
// `key = value` line format with `#` comments. Unrecognized keys are
// silently ignored so we can add settings without breaking older configs.
package userconf

import (
	"bufio"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type Settings struct {
	// LogEnabled gates the internal logfile (psps.log). Off by default —
	// only flip on when diagnosing an issue.
	LogEnabled bool
}

// Default returns the canonical config path. Empty string if the home dir
// isn't resolvable; callers should treat that as "no config".
func Default() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "psps", "config")
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".config", "psps", "config")
	}
	return ""
}

// Load parses path. A missing file yields zero Settings and a nil error —
// no config is a valid state. Parse errors on individual lines are tolerated
// (line skipped); only I/O errors surface.
func Load(path string) (Settings, error) {
	var s Settings
	if path == "" {
		return s, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return s, nil
		}
		return s, err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.Index(line, "#"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		k, v, ok := splitKV(line)
		if !ok {
			continue
		}
		switch k {
		case "log":
			s.LogEnabled = IsTruthy(v)
		}
	}
	return s, sc.Err()
}

// splitKV accepts either `key = value` or `key value` (kitty.conf style).
func splitKV(line string) (key, value string, ok bool) {
	if i := strings.Index(line, "="); i >= 0 {
		return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:]), true
	}
	if i := strings.IndexAny(line, " \t"); i >= 0 {
		return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:]), true
	}
	return "", "", false
}

// IsTruthy recognizes the common spellings for "on". Exported so the CLI and
// main can apply the same parsing to env-var overrides.
func IsTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "on", "true", "yes", "enable", "enabled":
		return true
	}
	return false
}
