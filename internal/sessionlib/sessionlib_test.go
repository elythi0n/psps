package sessionlib

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ─── sanitizeJSONControlChars ────────────────────────────────────────────────

func TestSanitizeJSONControlChars_RecoversFromLiteralNewline(t *testing.T) {
	raw := []byte("[{\"tabs\":[{\"title\":\"foo\nbar\",\"layout\":\"tall\",\"windows\":[]}]}]")
	if err := json.Unmarshal(raw, &[]kittyOSWindow{}); err == nil {
		t.Fatal("expected raw input to fail parsing — test premise is wrong")
	}
	cleaned := sanitizeJSONControlChars(raw)
	var out []kittyOSWindow
	if err := json.Unmarshal(cleaned, &out); err != nil {
		t.Fatalf("sanitized output still failed to parse: %v\n%s", err, cleaned)
	}
	if out[0].Tabs[0].Title != "foo\nbar" {
		t.Fatalf("title not preserved across escape/unescape: %q", out[0].Tabs[0].Title)
	}
}

func TestSanitizeJSONControlChars_HandlesMixedControlBytes(t *testing.T) {
	// All escapable control bytes inside a single string value.
	raw := []byte("\"a\nb\rc\td\be\ff\x01g\"")
	cleaned := sanitizeJSONControlChars(raw)
	var s string
	if err := json.Unmarshal(cleaned, &s); err != nil {
		t.Fatalf("parse failed: %v — %s", err, cleaned)
	}
	if s != "a\nb\rc\td\be\ff\x01g" {
		t.Fatalf("roundtrip mismatch: %q", s)
	}
}

func TestSanitizeJSONControlChars_LeavesStructuralWhitespaceAlone(t *testing.T) {
	raw := []byte("[\n  {\n    \"tabs\": []\n  }\n]")
	cleaned := sanitizeJSONControlChars(raw)
	if string(cleaned) != string(raw) {
		t.Fatalf("structural whitespace was modified:\nin : %q\nout: %q", raw, cleaned)
	}
}

func TestSanitizeJSONControlChars_RespectsEscapedQuote(t *testing.T) {
	raw := []byte(`"a\"b\nc"`)
	cleaned := sanitizeJSONControlChars(raw)
	if string(cleaned) != string(raw) {
		t.Fatalf("escaped quote handling changed input: %q -> %q", raw, cleaned)
	}
}

func TestSanitizeJSONControlChars_FixesInvalidBackslashEscape(t *testing.T) {
	// The PS1-style case: kitty's env dump can contain a literal `\u@\h \w$ `
	// shell prompt, which is invalid JSON (`\u` needs 4 hex digits, `\h`/`\w`
	// aren't valid escapes, `\ ` isn't either). We should turn each invalid
	// backslash into a literal `\\` so the parser sees the original raw text.
	raw := []byte(`"\u@\h \w$ "`)
	cleaned := sanitizeJSONControlChars(raw)
	var s string
	if err := json.Unmarshal(cleaned, &s); err != nil {
		t.Fatalf("sanitized output should parse: %v — got %q", err, cleaned)
	}
	if s != `\u@\h \w$ ` {
		t.Fatalf("backslashes not preserved as literal text: %q", s)
	}
}

func TestSanitizeJSONControlChars_PreservesValidUnicodeEscape(t *testing.T) {
	// A real \u escape with 4 hex digits is valid JSON and must be left
	// alone (not doubled into a literal "\u…" string).
	raw := []byte(`"café"`)
	cleaned := sanitizeJSONControlChars(raw)
	var s string
	if err := json.Unmarshal(cleaned, &s); err != nil {
		t.Fatalf("parse failed: %v — got %q", err, cleaned)
	}
	if s != "café" {
		t.Fatalf("valid \\u escape was corrupted: %q", s)
	}
}

func TestSanitizeJSONControlChars_HandlesTrailingBackslashAtEOF(t *testing.T) {
	// Degenerate: a backslash is the very last byte. We should double it
	// rather than crash or leave a dangling escape.
	raw := []byte{'"', 'a', '\\'}
	cleaned := sanitizeJSONControlChars(raw)
	if got, want := string(cleaned), `"a\\`; got != want {
		t.Errorf("trailing backslash not doubled: got %q want %q", got, want)
	}
}

// ─── parseKittyLs ────────────────────────────────────────────────────────────

func TestParseKittyLs_Valid(t *testing.T) {
	// Minimal realistic shape kitty @ ls produces: an array of OS windows,
	// each with tabs, each tab with windows.
	raw := []byte(`[{
		"tabs": [{
			"title": "work",
			"layout": "tall",
			"windows": [
				{"cwd": "/home/me/proj", "cmdline": ["bash"]},
				{"cwd": "/home/me", "cmdline": ["nvim", "."]}
			]
		}, {
			"title": "logs",
			"layout": "vertical",
			"windows": [
				{"cwd": "/var/log", "cmdline": ["tail", "-f", "syslog"]}
			]
		}]
	}]`)
	body, err := parseKittyLs(raw)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	wantLines := []string{
		"new_tab work",
		"layout tall",
		"launch --cwd=/home/me/proj bash",
		"launch --cwd=/home/me nvim .",
		"new_tab logs",
		"layout vertical",
		"launch --cwd=/var/log tail -f syslog",
	}
	for _, w := range wantLines {
		if !strings.Contains(body, w) {
			t.Errorf("expected session body to contain %q\ngot:\n%s", w, body)
		}
	}
}

func TestParseKittyLs_FallsBackToForegroundCWD(t *testing.T) {
	// kitty's foreground_processes is an array — one entry per foreground PID
	// in the window. When the top-level cwd is empty, fall back to the first
	// element's cwd.
	raw := []byte(`[{
		"tabs": [{
			"title": "t",
			"windows": [
				{"cwd": "", "cmdline": ["bash"], "foreground_processes": [{"cwd": "/srv/app", "cmdline": ["bash"]}]}
			]
		}]
	}]`)
	body, err := parseKittyLs(raw)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if !strings.Contains(body, "launch --cwd=/srv/app bash") {
		t.Errorf("did not fall back to foreground_processes[0].cwd:\n%s", body)
	}
}

func TestParseKittyLs_RealKittyShapeWithBadEnv(t *testing.T) {
	// End-to-end: input mirrors the real kitty @ ls shape (foreground_processes
	// as array, plus an env field containing a PS1 with unescaped backslash
	// sequences that previously broke parsing). Must succeed.
	raw := []byte(`[{
		"tabs": [{
			"title": "work",
			"layout": "splits",
			"windows": [{
				"cwd": "/home/me",
				"cmdline": ["/bin/bash"],
				"env": {"PS1": "\u@\h \w$ ", "PROMPT_COMMAND": "history -a"},
				"foreground_processes": [{"cwd": "/home/me", "cmdline": ["/bin/bash"]}]
			}]
		}]
	}]`)
	body, err := parseKittyLs(raw)
	if err != nil {
		t.Fatalf("parse failed against realistic kitty shape: %v", err)
	}
	if !strings.Contains(body, "launch --cwd=/home/me /bin/bash") {
		t.Errorf("launch line missing:\n%s", body)
	}
}

func TestParseKittyLs_NoCWD(t *testing.T) {
	// When neither cwd is set, omit the --cwd flag.
	raw := []byte(`[{
		"tabs": [{
			"title": "t",
			"windows": [{"cmdline": ["bash"]}]
		}]
	}]`)
	body, err := parseKittyLs(raw)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if strings.Contains(body, "--cwd") {
		t.Errorf("expected no --cwd flag when cwd is empty:\n%s", body)
	}
	if !strings.Contains(body, "launch bash") {
		t.Errorf("expected `launch bash` in:\n%s", body)
	}
}

func TestParseKittyLs_PlainTextError(t *testing.T) {
	// The "invalid character 'k'" case: kitty wrote a plain-text error to
	// stdout instead of JSON. We should surface it as-is, not as a json parse
	// error.
	raw := []byte("kitty: cannot connect to socket /tmp/kitty-9999")
	_, err := parseKittyLs(raw)
	if err == nil {
		t.Fatal("expected an error for non-JSON output")
	}
	if !strings.Contains(err.Error(), "non-JSON output") {
		t.Errorf("error should call out non-JSON output, got: %v", err)
	}
	if !strings.Contains(err.Error(), "cannot connect") {
		t.Errorf("error should include kitty's message, got: %v", err)
	}
}

func TestParseKittyLs_EmptyOutput(t *testing.T) {
	_, err := parseKittyLs(nil)
	if err == nil {
		t.Fatal("expected an error for empty output")
	}
	if !strings.Contains(err.Error(), "no output") {
		t.Errorf("unexpected error text: %v", err)
	}
}

func TestParseKittyLs_NoOSWindows(t *testing.T) {
	_, err := parseKittyLs([]byte(`[]`))
	if err == nil {
		t.Fatal("expected an error for empty windows array")
	}
	if !strings.Contains(err.Error(), "no OS windows") {
		t.Errorf("unexpected error text: %v", err)
	}
}

func TestPatchKittyJSON_RepairsTruncatedLastReportedCmd(t *testing.T) {
	// The exact corruption pattern observed in the wild: kitty emits the key
	// prefix `last_reported_cmd` (truncated from `last_reported_cmdline`)
	// padded with spaces, then runs the next key onto the same line.
	in := []byte(`{
		"last_cmd_exit_status": 0,
		"last_reported_cmd          "user_vars": {}
	}`)
	out := patchKittyJSON(in)
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("patched output still doesn't parse: %v\n---\n%s", err, out)
	}
	if _, ok := got["user_vars"]; !ok {
		t.Errorf("expected user_vars preserved, got keys: %v", got)
	}
	if _, ok := got["last_reported_cmdline"]; ok {
		t.Errorf("the bad key shouldn't have leaked through, got: %v", got)
	}
}

func TestPatchKittyJSON_LeavesValidLastReportedCmdline(t *testing.T) {
	// A non-truncated `last_reported_cmdline` field must be left alone — the
	// regex matches only when whitespace, not a colon, follows the key.
	in := []byte(`{"last_reported_cmdline": "claude", "user_vars": {}}`)
	out := patchKittyJSON(in)
	if string(out) != string(in) {
		t.Errorf("valid field was modified:\nin : %s\nout: %s", in, out)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if got["last_reported_cmdline"] != "claude" {
		t.Errorf("expected last_reported_cmdline=claude, got %v", got["last_reported_cmdline"])
	}
}

func TestPatchKittyJSON_RealKittyOutputShapeRoundTrip(t *testing.T) {
	// Minimal slice of the real kitty @ ls JSON that previously failed at
	// byte 3708 in the logs. End-to-end: patch → sanitize → unmarshal → must
	// reach our struct without error and surface the cwd we care about.
	raw := []byte(`[{
		"tabs": [{
			"title": "work",
			"layout": "splits",
			"windows": [{
				"cwd": "/home/me",
				"cmdline": ["/bin/bash"],
				"foreground_processes": [{"cwd": "/home/me", "cmdline": ["/bin/bash"]}],
				"last_cmd_exit_status": 0,
				"last_reported_cmd          "user_vars": {}
			}]
		}]
	}]`)
	body, err := parseKittyLs(raw)
	if err != nil {
		t.Fatalf("parse failed even after patching: %v", err)
	}
	if !strings.Contains(body, "launch --cwd=/home/me /bin/bash") {
		t.Errorf("expected the launch line, got:\n%s", body)
	}
}

func TestContextAroundJSONError_ShowsBytesNearOffset(t *testing.T) {
	// Force a parse failure at a known offset and verify our context helper
	// pulls the right bytes around it.
	raw := []byte(`{"a": 1, "b": broken, "c": 3}`)
	var out map[string]any
	err := json.Unmarshal(raw, &out)
	if err == nil {
		t.Fatal("expected parse to fail")
	}
	got := contextAroundJSONError(raw, err)
	if !strings.Contains(got, "broken") {
		t.Errorf("expected snippet to include the offending token, got: %s", got)
	}
	if !strings.Contains(got, "at byte") {
		t.Errorf("expected snippet to mention byte offset, got: %s", got)
	}
}

func TestParseKittyLs_HandlesEmbeddedControlChars(t *testing.T) {
	// End-to-end: JSON with a literal newline inside a title goes through
	// sanitize → unmarshal → buildSession cleanly. The title is dropped to
	// the auto-generated "tabN" fallback because the title field has a
	// newline in it which kitty session syntax can't represent on one line
	// anyway — but the important thing is we don't error out.
	raw := []byte("[{\"tabs\":[{\"title\":\"line1\nline2\",\"layout\":\"tall\",\"windows\":[{\"cwd\":\"/x\",\"cmdline\":[\"bash\"]}]}]}]")
	body, err := parseKittyLs(raw)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if !strings.Contains(body, "launch --cwd=/x bash") {
		t.Errorf("body missing launch line:\n%s", body)
	}
}

// ─── filesystem round-trip: WriteBody → List → Delete ────────────────────────

// withSessionDir redirects Dir() to a tmp dir for the test by setting HOME.
// The package always derives Dir() from $HOME/.config/kitty/sessions.
func withSessionDir(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	return filepath.Join(tmp, ".config/kitty/sessions")
}

func TestWriteBodyAndList_RoundTrip(t *testing.T) {
	want := withSessionDir(t)

	body := "new_tab work\nlayout tall\nlaunch bash\n"
	sess, err := WriteBody("first", body)
	if err != nil {
		t.Fatalf("WriteBody: %v", err)
	}
	if sess.Name != "first" {
		t.Errorf("name: got %q want %q", sess.Name, "first")
	}
	if sess.Body != body {
		t.Errorf("body roundtrip mismatch")
	}
	if filepath.Dir(sess.Path) != want {
		t.Errorf("path dir: got %q want %q", filepath.Dir(sess.Path), want)
	}

	// Disk should reflect it.
	on, err := os.ReadFile(sess.Path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(on) != body {
		t.Errorf("on-disk body mismatch:\nwant %q\ngot  %q", body, on)
	}
}

func TestList_SortsMostRecentFirst(t *testing.T) {
	_ = withSessionDir(t)

	// Write two sessions with distinct mtimes.
	if _, err := WriteBody("older", "a"); err != nil {
		t.Fatal(err)
	}
	// Force mtime ordering — same-millisecond writes can produce ties on some
	// filesystems, leading to flakiness.
	older := PathFor("older")
	past := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(older, past, past); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteBody("newer", "b"); err != nil {
		t.Fatal(err)
	}

	all, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 sessions, got %d: %+v", len(all), all)
	}
	if all[0].Name != "newer" || all[1].Name != "older" {
		t.Errorf("List order wrong, expected [newer, older], got [%s, %s]", all[0].Name, all[1].Name)
	}
}

func TestList_IgnoresNonConfFiles(t *testing.T) {
	dir := withSessionDir(t)

	if _, err := WriteBody("keeper", "x"); err != nil {
		t.Fatal(err)
	}
	// Litter the dir with files that aren't sessions.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	all, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 || all[0].Name != "keeper" {
		t.Errorf("expected only [keeper], got %+v", all)
	}
}

func TestDelete_RemovesSession(t *testing.T) {
	_ = withSessionDir(t)

	if _, err := WriteBody("to-delete", "x"); err != nil {
		t.Fatal(err)
	}
	if err := Delete("to-delete"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(PathFor("to-delete")); !os.IsNotExist(err) {
		t.Errorf("expected file gone, stat err = %v", err)
	}
	all, _ := List()
	if len(all) != 0 {
		t.Errorf("expected empty list after delete, got %+v", all)
	}
}

func TestPathFor_StableAndSiblingOfKittyConf(t *testing.T) {
	_ = withSessionDir(t)
	p := PathFor("foo")
	if filepath.Base(p) != "foo.conf" {
		t.Errorf("filename: got %q want foo.conf", filepath.Base(p))
	}
	if filepath.Base(filepath.Dir(p)) != "sessions" {
		t.Errorf("parent dir: got %q want sessions", filepath.Base(filepath.Dir(p)))
	}
}

// ─── buildSession formatting ─────────────────────────────────────────────────

func TestBuildSession_AssignsFallbackTabName(t *testing.T) {
	w := kittyOSWindow{
		Tabs: []kittyTab{
			{Title: "", Layout: "tall", Windows: []kittyWindow{{CWD: "/x", Cmdline: []string{"bash"}}}},
			{Title: "", Layout: "tall", Windows: []kittyWindow{{CWD: "/y", Cmdline: []string{"bash"}}}},
		},
	}
	body := buildSession(w)
	if !strings.Contains(body, "new_tab tab1") || !strings.Contains(body, "new_tab tab2") {
		t.Errorf("expected fallback tab names tab1/tab2 in:\n%s", body)
	}
}

func TestBuildSession_SeparatesTabsWithBlankLine(t *testing.T) {
	w := kittyOSWindow{
		Tabs: []kittyTab{
			{Title: "a", Layout: "tall", Windows: []kittyWindow{{CWD: "/x", Cmdline: []string{"bash"}}}},
			{Title: "b", Layout: "tall", Windows: []kittyWindow{{CWD: "/y", Cmdline: []string{"bash"}}}},
		},
	}
	body := buildSession(w)
	// Between the two `new_tab` blocks there should be a blank line.
	if !strings.Contains(body, "\n\nnew_tab b") {
		t.Errorf("missing blank-line separator between tabs:\n%s", body)
	}
}

func TestBuildSession_OmitsLayoutWhenEmpty(t *testing.T) {
	w := kittyOSWindow{
		Tabs: []kittyTab{
			{Title: "a", Layout: "", Windows: []kittyWindow{{CWD: "/x", Cmdline: []string{"bash"}}}},
		},
	}
	body := buildSession(w)
	if strings.Contains(body, "layout") {
		t.Errorf("did not expect a layout line:\n%s", body)
	}
}
