package installer

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStage_LocalFile(t *testing.T) {
	src := filepath.Join(t.TempDir(), "Mytheme.conf")
	body := "background #112233\nforeground #aabbcc\n"
	if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Stage(src)
	if err != nil {
		t.Fatalf("Stage local file: %v", err)
	}
	defer s.Cleanup()
	if s.Kind != KindFile {
		t.Errorf("Kind = %v, want KindFile", s.Kind)
	}
	if s.LocalPath != src {
		t.Errorf("LocalPath = %q, want %q", s.LocalPath, src)
	}
	if s.Transport() != "file" {
		t.Errorf("Transport = %q, want file", s.Transport())
	}
}

func TestStage_LocalDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "theme.conf"), []byte("background #000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Stage(dir)
	if err != nil {
		t.Fatalf("Stage local dir: %v", err)
	}
	defer s.Cleanup()
	if s.Kind != KindDirectory {
		t.Errorf("Kind = %v, want KindDirectory", s.Kind)
	}
}

func TestStage_HTTPFile(t *testing.T) {
	body := "# remote theme\nbackground #abcdef\nforeground #123456\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	url := srv.URL + "/themes/Remote.conf"
	s, err := Stage(url)
	if err != nil {
		t.Fatalf("Stage HTTP: %v", err)
	}
	defer s.Cleanup()
	if s.Kind != KindFile {
		t.Errorf("Kind = %v, want KindFile", s.Kind)
	}
	if s.Transport() != "http" {
		t.Errorf("Transport = %q, want http", s.Transport())
	}
	got, _ := os.ReadFile(s.LocalPath)
	if string(got) != body {
		t.Errorf("body mismatch:\nwant: %q\ngot:  %q", body, got)
	}
}

func TestStage_HTTPFileFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	_, err := Stage(srv.URL + "/missing.conf")
	if err == nil {
		t.Fatal("expected an error for HTTP 404")
	}
	if !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("error should mention HTTP 404, got: %v", err)
	}
}

func TestStage_RejectsEmptySource(t *testing.T) {
	if _, err := Stage(""); err == nil {
		t.Fatal("expected error for empty source")
	}
}

func TestStage_UnknownScheme(t *testing.T) {
	// A scheme we don't recognise should fall through to "don't know how to fetch".
	// Use a single-word, no-slashes string that also isn't an existing path.
	_, err := Stage("notavalidsource")
	if err == nil {
		t.Fatal("expected error for unrecognised source")
	}
}

func TestDetectTransport_Routes(t *testing.T) {
	// Route resolution without actually doing a clone/fetch. Each case is the
	// source string + the transport we expect to dispatch to + the normalised
	// target the corresponding stage* would receive.
	cases := []struct {
		source, transport, target string
	}{
		{"https://example.com/foo/bar.conf", "http", "https://example.com/foo/bar.conf"},
		{"https://example.com/foo/bar", "git", "https://example.com/foo/bar"},
		{"http://example.com/x.CONF", "http", "http://example.com/x.CONF"}, // case-insensitive suffix
		{"ssh://git@github.com/user/repo.git", "git", "ssh://git@github.com/user/repo.git"},
		{"git@github.com:user/repo.git", "git", "git@github.com:user/repo.git"},
		{"git+https://example.com/x.git", "git", "https://example.com/x.git"},
		{"owner/repo", "git", "https://github.com/owner/repo"},
		{"github.com/owner/repo", "git", "https://github.com/owner/repo"},
		{"gitlab.com/group/sub/repo", "git", "https://gitlab.com/group/sub/repo"},
	}
	for _, c := range cases {
		gotT, gotTarget, err := detectTransport(c.source)
		if err != nil {
			t.Errorf("detectTransport(%q) error: %v", c.source, err)
			continue
		}
		if gotT != c.transport || gotTarget != c.target {
			t.Errorf("detectTransport(%q) = (%q, %q); want (%q, %q)", c.source, gotT, gotTarget, c.transport, c.target)
		}
	}
}

func TestDetectTransport_BareWordErrors(t *testing.T) {
	// A bare word with no slashes shouldn't be silently routed to git — the
	// retry/timeout cost when the user typos a command name is too high.
	_, _, err := detectTransport("nothelpful")
	if err == nil {
		t.Fatal("expected error for ambiguous bare-word source")
	}
}

func TestStaged_CleanupRemovesTmp(t *testing.T) {
	body := "background #000\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, body)
	}))
	defer srv.Close()
	s, err := Stage(srv.URL + "/x.conf")
	if err != nil {
		t.Fatal(err)
	}
	tmp := s.tmpDir
	if tmp == "" {
		t.Fatal("expected a tmpDir for HTTP staging")
	}
	s.Cleanup()
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Errorf("tmpDir %s still present after Cleanup", tmp)
	}
}
