// Package installer fetches a theme or profile from a remote source into a
// temporary staging directory so the caller can preview the contents before
// moving them into the final destination. Two transports:
//
//   1. Single-file HTTP(S) fetch — used for raw .conf URLs
//      (e.g. https://raw.githubusercontent.com/.../mytheme.conf).
//   2. git clone — used for any other URL or repo shorthand
//      (https://github.com/user/repo, git@github.com:user/repo,
//      or the shorthand github.com/user/repo).
//
// The split is deliberate: a single .conf is the safest "trust" surface (one
// file of colour directives), while git clone gives us full repo structure
// for profiles but pulls arbitrary content — hence the mandatory review step
// the caller is expected to drive before committing.
package installer

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Kind describes what got staged.
type Kind int

const (
	KindFile      Kind = iota // single file
	KindDirectory             // a directory of files (typical for a git clone)
)

// Staged is the outcome of Stage(). The caller inspects LocalPath, decides
// whether to commit it (via copy / move / further processing), and MUST call
// Cleanup when done — even on success, the staging area lives until cleanup.
type Staged struct {
	Kind      Kind
	LocalPath string // absolute path of the staged file or directory
	Source    string // user-facing description of where it came from
	transport string // "file", "http", "git"
	tmpDir    string // owned tempdir to be removed in Cleanup
}

// Cleanup removes the staging directory. Safe to call multiple times.
func (s *Staged) Cleanup() {
	if s == nil || s.tmpDir == "" {
		return
	}
	_ = os.RemoveAll(s.tmpDir)
	s.tmpDir = ""
}

// Transport reports which path was used to fetch the source. Useful for log
// lines and review headers.
func (s *Staged) Transport() string { return s.transport }

// FindThemeConf locates the .conf file that represents a theme inside a
// staged install. KindFile is used directly; KindDirectory is searched for
// canonical layouts: a single .conf at the root, or any .conf in the common
// subdirs (themes/, colors/, kitty-themes/). Ambiguous matches return an
// error rather than guessing.
func (s *Staged) FindThemeConf() (string, error) {
	if s.Kind == KindFile {
		return s.LocalPath, nil
	}
	if hits := globConf(s.LocalPath); len(hits) == 1 {
		return hits[0], nil
	} else if len(hits) > 1 {
		return "", fmt.Errorf("multiple .conf files at repo root — pick one explicitly (point at the raw .conf URL)\n  found: %s", strings.Join(hits, ", "))
	}
	for _, sub := range []string{"themes", "colors", "kitty-themes"} {
		path := filepath.Join(s.LocalPath, sub)
		if hits := globConf(path); len(hits) >= 1 {
			if len(hits) > 1 {
				return "", fmt.Errorf("multiple .conf files under %s — pick one explicitly (point at the raw .conf URL)", path)
			}
			return hits[0], nil
		}
	}
	return "", fmt.Errorf("no .conf file found in %s", s.LocalPath)
}

// globConf returns the absolute paths of every top-level .conf file in dir.
// Returns nil (not an error) when dir is missing or unreadable so the caller
// can chain fallback search locations.
func globConf(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".conf") {
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	return out
}

// Stage figures out how to fetch source and prepares a staging area under
// $TMPDIR. The caller is responsible for inspecting and then committing it.
func Stage(source string) (*Staged, error) {
	transport, target, err := detectTransport(source)
	if err != nil {
		return nil, err
	}
	switch transport {
	case "file":
		return stageLocal(target)
	case "http":
		return stageHTTPFile(target)
	case "git":
		return stageGit(target)
	default:
		return nil, fmt.Errorf("internal: unknown transport %q", transport)
	}
}

// detectTransport routes a source string to one of {file, http, git} and
// normalises it to a target argument the corresponding stage* function knows
// how to handle. Pulled out for testability — lets unit tests assert routing
// without actually doing a git clone.
//
// Source forms (matched top-to-bottom):
//
//   - Existing local path           → "file", abs(path)
//   - http(s)://... ending in .conf → "http", url
//   - http(s)://...                 → "git",  url
//   - ssh://... | git@host:path     → "git",  url
//   - host/owner/repo (2+ slashes)  → "git",  https://host/owner/repo
//   - owner/repo     (1 slash)      → "git",  https://github.com/owner/repo
//
// Anything else (bare word, no slashes) is an error — we don't silently
// route to git clone for ambiguous input, because git's connection retry
// timeouts can hang the caller for several seconds before failing.
func detectTransport(source string) (transport, target string, err error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", "", errors.New("install source is empty")
	}

	if isExistingPath(source) {
		abs, err := filepath.Abs(expandTilde(source))
		if err != nil {
			return "", "", err
		}
		return "file", abs, nil
	}

	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		u, perr := url.Parse(source)
		if perr == nil && strings.HasSuffix(strings.ToLower(u.Path), ".conf") {
			return "http", source, nil
		}
		return "git", source, nil
	}

	if strings.HasPrefix(source, "ssh://") || strings.HasPrefix(source, "git@") {
		return "git", source, nil
	}
	if strings.HasPrefix(source, "git+") {
		return "git", strings.TrimPrefix(source, "git+"), nil
	}

	if !strings.Contains(source, "://") {
		slashes := strings.Count(source, "/")
		if slashes == 1 {
			return "git", "https://github.com/" + source, nil
		}
		if slashes >= 2 {
			return "git", "https://" + source, nil
		}
		// Bare word — refuse rather than do a 4-second DNS-fail git clone.
		return "", "", fmt.Errorf("don't know how to fetch source %q (expected a path, URL, or owner/repo)", source)
	}

	return "", "", fmt.Errorf("don't know how to fetch source %q", source)
}

func expandTilde(s string) string {
	if !strings.HasPrefix(s, "~") {
		return s
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return s
	}
	return filepath.Join(home, strings.TrimPrefix(s, "~"))
}

func stageLocal(path string) (*Staged, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	kind := KindFile
	if info.IsDir() {
		kind = KindDirectory
	}
	return &Staged{
		Kind:      kind,
		LocalPath: abs,
		Source:    abs,
		transport: "file",
	}, nil
}

func stageHTTPFile(rawURL string) (*Staged, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	name := filepath.Base(u.Path)
	if name == "" || name == "/" || name == "." {
		return nil, fmt.Errorf("can't derive filename from URL %s", rawURL)
	}

	tmp, err := os.MkdirTemp("", "psps-install-*")
	if err != nil {
		return nil, err
	}
	dst := filepath.Join(tmp, name)

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		_ = os.RemoveAll(tmp)
		return nil, err
	}
	req.Header.Set("User-Agent", "psps-installer")

	resp, err := client.Do(req)
	if err != nil {
		_ = os.RemoveAll(tmp)
		return nil, fmt.Errorf("fetch %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_ = os.RemoveAll(tmp)
		return nil, fmt.Errorf("fetch %s: HTTP %d", rawURL, resp.StatusCode)
	}

	f, err := os.Create(dst)
	if err != nil {
		_ = os.RemoveAll(tmp)
		return nil, err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		_ = os.RemoveAll(tmp)
		return nil, fmt.Errorf("write %s: %w", dst, err)
	}
	if err := f.Close(); err != nil {
		_ = os.RemoveAll(tmp)
		return nil, err
	}

	return &Staged{
		Kind:      KindFile,
		LocalPath: dst,
		Source:    rawURL,
		transport: "http",
		tmpDir:    tmp,
	}, nil
}

func stageGit(repoURL string) (*Staged, error) {
	tmp, err := os.MkdirTemp("", "psps-install-*")
	if err != nil {
		return nil, err
	}
	dst := filepath.Join(tmp, "repo")

	cmd := exec.Command("git", "clone", "--depth=1", "--quiet", repoURL, dst)
	out, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.RemoveAll(tmp)
		return nil, fmt.Errorf("git clone %s: %v · %s", repoURL, err, strings.TrimSpace(string(out)))
	}
	return &Staged{
		Kind:      KindDirectory,
		LocalPath: dst,
		Source:    repoURL,
		transport: "git",
		tmpDir:    tmp,
	}, nil
}

func isExistingPath(s string) bool {
	if strings.Contains(s, "://") {
		return false
	}
	if !strings.HasPrefix(s, "/") && !strings.HasPrefix(s, ".") && !strings.HasPrefix(s, "~") {
		return false
	}
	expanded := s
	if strings.HasPrefix(s, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			expanded = filepath.Join(home, strings.TrimPrefix(s, "~"))
		}
	}
	_, err := os.Stat(expanded)
	return err == nil
}
