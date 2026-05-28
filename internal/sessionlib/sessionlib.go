// Package sessionlib stores and replays kitty session layouts.
//
// Sessions are plain kitty session files (`new_tab`, `layout`, `launch` …) so
// they're consumable by `kitty --session <path>` directly. The package is
// UI-free; the TUI pane and the CLI both call into it.
//
// Capturing the current layout requires kitty's remote control:
//
//   allow_remote_control yes
//   listen_on unix:/tmp/kitty-{kitty_pid}
//
// in kitty.conf. Without that, Save() returns a clear error.
package sessionlib

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/elythi0n/psps/internal/logfile"
)

// kittyLsTimeout caps how long we wait for `kitty @ ls`. Past experience: an
// unreachable remote-control socket leaves the kitty CLI hanging for ~10s
// before its own internal timeout fires, which both frustrates the user and
// lets a flurry of rapid `s` presses queue up multiple stale goroutines.
const kittyLsTimeout = 4 * time.Second

// Default session name used by the autosave loop.
const AutoName = "auto"

type Session struct {
	Name string
	Path string
	Mod  time.Time
	Body string
}

// Dir is where session .conf files live (sibling of kitty.conf).
func Dir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config/kitty/sessions")
}

// PathFor returns the on-disk path for a named session.
func PathFor(name string) string {
	return filepath.Join(Dir(), name+".conf")
}

// Save snapshots the current kitty layout (via `kitty @ ls`) and writes it
// under name. Existing files are overwritten — callers handle uniqueness.
func Save(name string) (Session, error) {
	body, err := dumpCurrent()
	if err != nil {
		return Session{}, err
	}
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return Session{}, err
	}
	path := PathFor(name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return Session{}, err
	}
	info, _ := os.Stat(path)
	mod := time.Now()
	if info != nil {
		mod = info.ModTime()
	}
	return Session{Name: name, Path: path, Mod: mod, Body: body}, nil
}

// WriteBody saves an already-built session body under name (no kitty @ ls).
// Used by the TUI's "review and rename" flow that pre-dumped the layout.
func WriteBody(name, body string) (Session, error) {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return Session{}, err
	}
	path := PathFor(name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return Session{}, err
	}
	info, _ := os.Stat(path)
	return Session{
		Name: name,
		Path: path,
		Mod:  info.ModTime(),
		Body: body,
	}, nil
}

// Dump returns the current kitty layout as a session file body without
// writing anything. Useful for "preview and name" UX.
func Dump() (string, error) {
	return dumpCurrent()
}

// ParseKittyLs is the exported entry point to the kitty @ ls output parser.
// Callers that run `kitty @ ls` themselves (e.g. via bubbletea's tea.ExecProcess
// to suspend the UI while talking to the TTY) can feed the captured stdout
// straight in. Applies the same patches and sanitisation as the in-process
// Dump() path.
func ParseKittyLs(raw []byte) (string, error) {
	return parseKittyLs(raw)
}

// MakeKittyLsCmd builds the *exec.Cmd we'd run to enumerate kitty's state,
// using --to $KITTY_LISTEN_ON when the env var is set. Returned cmd has no
// Stdin/Stdout/Stderr wired up — the caller decides whether to capture or
// inherit them (the suspend path inherits stdin/stderr from the real TTY).
func MakeKittyLsCmd() *exec.Cmd {
	args := []string{"@", "ls"}
	if listen := os.Getenv("KITTY_LISTEN_ON"); listen != "" {
		args = []string{"@", "--to", listen, "ls"}
	}
	return exec.Command("kitty", args...)
}

// List returns saved sessions, most recent first.
func List() ([]Session, error) {
	dir := Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Session
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".conf") {
			continue
		}
		full := filepath.Join(dir, e.Name())
		info, _ := e.Info()
		body, _ := os.ReadFile(full)
		out = append(out, Session{
			Name: strings.TrimSuffix(e.Name(), ".conf"),
			Path: full,
			Mod:  info.ModTime(),
			Body: string(body),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Mod.After(out[j].Mod) })
	return out, nil
}

// Restore launches a detached kitty pre-loaded with the named session.
func Restore(name string) error {
	path := PathFor(name)
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("session %q not found at %s", name, path)
	}
	cmd := exec.Command("kitty", "--detach", "--session", path)
	return cmd.Start()
}

// Delete removes a session file.
func Delete(name string) error {
	return os.Remove(PathFor(name))
}

// ─── internal: shell out to `kitty @ ls` ──────────────────────────────────

type kittyOSWindow struct {
	Tabs []kittyTab `json:"tabs"`
}
type kittyTab struct {
	Title   string        `json:"title"`
	Layout  string        `json:"layout"`
	Windows []kittyWindow `json:"windows"`
}
type kittyWindow struct {
	CWD     string   `json:"cwd"`
	Cmdline []string `json:"cmdline"`
	Title   string   `json:"title"`
	// foreground_processes is an ARRAY in kitty's output (one entry per
	// active foreground PID in the window). We previously declared it as a
	// single object, which silently broke once kitty's parse hit it.
	ForegroundProcs []struct {
		Cmdline []string `json:"cmdline"`
		CWD     string   `json:"cwd"`
	} `json:"foreground_processes"`
}

func dumpCurrent() (string, error) {
	// Build the argv. Inside a kitty window, `kitty @ ls` normally talks to
	// the parent kitty over the controlling TTY's stdin/stdout. But psps runs
	// bubbletea in raw mode, which monopolises the TTY — the CLI then sits
	// waiting on TTY IPC until it times out (~10s default). Bypass that path
	// by talking to the unix socket directly via `--to $KITTY_LISTEN_ON`.
	// kitty exports this env var to its children when `listen_on` is set in
	// kitty.conf. If it's unset, fall back to plain `@ ls` and surface a
	// targeted error if it fails — there's no socket-based path available.
	listen := os.Getenv("KITTY_LISTEN_ON")
	args := []string{"@", "ls"}
	if listen != "" {
		args = []string{"@", "--to", listen, "ls"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), kittyLsTimeout)
	defer cancel()

	// Capture stdout/stderr separately. CombinedOutput would mix kitty's
	// stderr warnings into the JSON stream, producing confusing parse errors
	// like `invalid character ':' in string` from stdlib json.
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "kitty", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(start)

	// Log the full diagnostic on every invocation. The user can tail this to
	// see exactly what kitty returned across the wire — the transient TUI
	// status flash isn't enough to debug edge cases.
	logKittyInvocation(args, runErr, elapsed, stdout.Bytes(), stderr.Bytes())

	if runErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			if listen == "" {
				return "", withLogHint(fmt.Errorf("kitty @ ls timed out after %s — $KITTY_LISTEN_ON is empty, so add `listen_on unix:/tmp/kitty-{kitty_pid}` to kitty.conf and restart kitty (raw-mode TUIs can't use kitty's default TTY-based remote control)", kittyLsTimeout))
			}
			return "", withLogHint(fmt.Errorf("kitty @ ls timed out after %s talking to %s — is that socket actually live?", kittyLsTimeout, listen))
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = runErr.Error()
		}
		return "", withLogHint(fmt.Errorf("kitty @ ls: %s (need `allow_remote_control yes` + `listen_on unix:/tmp/kitty-{kitty_pid}` in kitty.conf)", msg))
	}

	body, err := parseKittyLs(stdout.Bytes())
	if err != nil {
		return "", withLogHint(err)
	}
	return body, nil
}

// logKittyInvocation writes a structured block describing one kitty @ ls call
// to the log file. Truncates very large stdout/stderr to keep the log readable
// while preserving the head, where parse errors usually surface.
func logKittyInvocation(args []string, runErr error, elapsed time.Duration, stdout, stderr []byte) {
	const cap = 32 * 1024
	clip := func(b []byte) string {
		if len(b) <= cap {
			return string(b)
		}
		return string(b[:cap]) + fmt.Sprintf("\n…(truncated %d more bytes)", len(b)-cap)
	}
	status := "ok"
	if runErr != nil {
		status = runErr.Error()
	}
	logfile.Infof(
		"kitty %s — elapsed=%s status=%s\n  stdout(%dB): %s\n  stderr(%dB): %s",
		strings.Join(args, " "),
		elapsed.Round(time.Millisecond),
		status,
		len(stdout), clip(stdout),
		len(stderr), clip(stderr),
	)
}

// withLogHint appends a pointer to the log file so the user knows where to
// look for the full diagnostic.
func withLogHint(err error) error {
	logfile.Errorf("%v", err)
	if p := logfile.Path(); p != "" {
		return fmt.Errorf("%w · full output: %s", err, p)
	}
	return err
}

// parseKittyLs turns the raw stdout of `kitty @ ls` into a kitty session file
// body. Split out from dumpCurrent so it can be exercised in unit tests
// without shelling out to kitty.
func parseKittyLs(raw []byte) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "", fmt.Errorf("kitty @ ls returned no output")
	}
	// If stdout doesn't start with a JSON token, kitty wrote a plain-text
	// error message there (e.g. "kitty: unknown socket protocol: …" or
	// "kitty: connection refused"). Surface it directly instead of letting
	// json.Unmarshal fail with the unhelpful `invalid character 'k'`.
	if trimmed[0] != '[' && trimmed[0] != '{' {
		snippet := string(trimmed)
		if len(snippet) > 200 {
			snippet = snippet[:200] + "…"
		}
		return "", fmt.Errorf("kitty @ ls returned non-JSON output: %s", snippet)
	}

	// Patch known kitty serialiser bugs that produce structurally invalid
	// JSON — currently the truncated `last_reported_cmdline` field where
	// kitty collapses the value to padding spaces and runs the next key
	// onto the same line. Then run the control-char / backslash sanitiser
	// to fix in-string issues that stdlib json rejects.
	data := patchKittyJSON(trimmed)
	data = sanitizeJSONControlChars(data)

	var windows []kittyOSWindow
	if err := json.Unmarshal(data, &windows); err != nil {
		return "", fmt.Errorf("parse kitty @ ls output: %w%s", err, contextAroundJSONError(data, err))
	}
	if len(windows) == 0 {
		return "", fmt.Errorf("no OS windows reported by kitty @ ls")
	}
	return buildSession(windows[0]), nil
}

// brokenLastReportedCmd matches a kitty serialiser glitch where the
// `last_reported_cmdline` field gets emitted as just the prefix `last_reported_cmd`
// padded with spaces, immediately followed by the opening quote of the next
// key on the same line. Example raw output:
//
//	"last_reported_cmd          "user_vars": {}
//
// The match goes from the opening `"` of the broken key up to (and including)
// the opening `"` of the next key. patchKittyJSON replaces it with just `"`
// to leave the next key's opening quote in place.
var brokenLastReportedCmd = regexp.MustCompile(`"last_reported_cmd[a-z_]*\s+"`)

// patchKittyJSON repairs structural malformations that originate inside
// kitty's own JSON serialiser before we hand the bytes to stdlib json.
// We don't need any of the fields it strips, so dropping them is harmless.
func patchKittyJSON(in []byte) []byte {
	return brokenLastReportedCmd.ReplaceAll(in, []byte(`"`))
}

// sanitizeJSONControlChars walks a JSON byte stream and repairs two classes of
// kitty-specific malformation that stdlib json rejects:
//
//  1. Raw control bytes (< 0x20) appearing inside a string literal. Replaced
//     with their JSON escape (\n, \t, \uXXXX, …).
//  2. Backslashes inside a string that don't introduce a valid JSON escape
//     sequence. kitty's `env` field can contain shell-prompt values like
//     `PS1="\u@\h \w$ "` verbatim — the `\u`/`\h`/`\w` aren't valid JSON
//     escapes (and `\u` would even require 4 hex digits), so the parse fails
//     with `invalid character 'w' in string escape code` or similar. We treat
//     any non-escape backslash as a literal and double it so the parser sees
//     a single literal backslash.
//
// Structural whitespace outside strings is left intact.
func sanitizeJSONControlChars(in []byte) []byte {
	var out bytes.Buffer
	out.Grow(len(in))
	inString := false
	i := 0
	for i < len(in) {
		c := in[i]
		if !inString {
			if c == '"' {
				inString = true
			}
			out.WriteByte(c)
			i++
			continue
		}
		// Inside a string from here on.
		if c == '"' {
			inString = false
			out.WriteByte(c)
			i++
			continue
		}
		if c == '\\' {
			// Decide based on the next byte whether this is a valid JSON
			// escape sequence. If not, treat the backslash as a literal.
			if i+1 < len(in) {
				switch next := in[i+1]; next {
				case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
					out.WriteByte(c)
					out.WriteByte(next)
					i += 2
					continue
				case 'u':
					// \uXXXX — need 4 hex digits.
					if i+5 < len(in) && allHex(in[i+2:i+6]) {
						out.Write(in[i : i+6])
						i += 6
						continue
					}
				}
			}
			// Invalid escape or trailing backslash — double it.
			out.WriteString(`\\`)
			i++
			continue
		}
		if c < 0x20 {
			switch c {
			case '\n':
				out.WriteString(`\n`)
			case '\r':
				out.WriteString(`\r`)
			case '\t':
				out.WriteString(`\t`)
			case '\b':
				out.WriteString(`\b`)
			case '\f':
				out.WriteString(`\f`)
			default:
				fmt.Fprintf(&out, `\u%04x`, c)
			}
			i++
			continue
		}
		out.WriteByte(c)
		i++
	}
	return out.Bytes()
}

// contextAroundJSONError returns a human-readable snippet of `data` centred on
// the byte offset reported by stdlib json's *SyntaxError or *UnmarshalTypeError,
// so log readers can see exactly which key/value tripped the parser instead of
// just the first 160 bytes of a 17 KB document.
func contextAroundJSONError(data []byte, err error) string {
	var offset int64 = -1
	switch e := err.(type) {
	case *json.SyntaxError:
		offset = e.Offset
	case *json.UnmarshalTypeError:
		offset = e.Offset
	}
	if offset < 0 || offset > int64(len(data)) {
		// Fall back: show head.
		head := data
		if len(head) > 160 {
			head = head[:160]
		}
		return fmt.Sprintf(" — head: %q", head)
	}
	const window = 80
	start := offset - window
	if start < 0 {
		start = 0
	}
	end := offset + window
	if end > int64(len(data)) {
		end = int64(len(data))
	}
	return fmt.Sprintf(" — at byte %d: %q (caret at offset %d in this slice)",
		offset, data[start:end], offset-start)
}

func allHex(b []byte) bool {
	for _, c := range b {
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

func buildSession(w kittyOSWindow) string {
	var b strings.Builder
	for i, tab := range w.Tabs {
		if i > 0 {
			b.WriteString("\n")
		}
		title := tab.Title
		if title == "" {
			title = fmt.Sprintf("tab%d", i+1)
		}
		fmt.Fprintf(&b, "new_tab %s\n", title)
		if tab.Layout != "" {
			fmt.Fprintf(&b, "layout %s\n", tab.Layout)
		}
		for _, win := range tab.Windows {
			cwd := win.CWD
			if cwd == "" && len(win.ForegroundProcs) > 0 {
				cwd = win.ForegroundProcs[0].CWD
			}
			cmd := strings.Join(win.Cmdline, " ")
			if cwd != "" {
				fmt.Fprintf(&b, "launch --cwd=%s %s\n", cwd, cmd)
			} else {
				fmt.Fprintf(&b, "launch %s\n", cmd)
			}
		}
	}
	return b.String()
}
