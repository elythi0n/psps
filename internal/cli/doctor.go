package cli

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/elythi0n/psps/internal/kconf"
	"github.com/elythi0n/psps/internal/kitty"
	"github.com/elythi0n/psps/internal/sessionlib"
	"github.com/elythi0n/psps/internal/themelib"
	"github.com/elythi0n/psps/internal/ui"
)

// doctorCheck is one diagnostic result. status is one of "ok", "warn", "err",
// "info" — only "err" influences the process exit code.
type doctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
	Hint   string `json:"hint,omitempty"`
}

func (c *cli) cmdDoctor() int {
	checks := runDoctor()

	exit := 0
	counts := map[string]int{}
	for _, ch := range checks {
		counts[ch.Status]++
		if ch.Status == "err" {
			exit = 1
		}
	}

	if c.json {
		c.emit(map[string]any{
			"checks":  checks,
			"summary": counts,
			"exit":    exit,
		})
		return exit
	}

	color := useColor()
	for _, ch := range checks {
		fmt.Println(formatCheck(ch, color))
	}
	fmt.Println()
	fmt.Printf("%d ok · %d warn · %d err · %d info\n",
		counts["ok"], counts["warn"], counts["err"], counts["info"])
	return exit
}

func runDoctor() []doctorCheck {
	var out []doctorCheck

	// 1. kitty binary
	if path, err := exec.LookPath("kitty"); err == nil {
		out = append(out, doctorCheck{Name: "kitty binary", Status: "ok", Detail: path})
	} else {
		out = append(out, doctorCheck{
			Name:   "kitty binary",
			Status: "err",
			Detail: "not found on PATH",
			Hint:   "install kitty (https://sw.kovidgoyal.net/kitty/binary/)",
		})
	}

	// 2. psps binary on PATH (some keybindings call it as `psps`, not via $0)
	if path, err := exec.LookPath("psps"); err == nil {
		out = append(out, doctorCheck{Name: "psps on PATH", Status: "ok", Detail: path})
	} else {
		out = append(out, doctorCheck{
			Name:   "psps on PATH",
			Status: "warn",
			Detail: "not on PATH — keybindings that call `psps zoom inc 2` from kitty.conf will fail",
			Hint:   "ensure the install location is in PATH (or use an absolute path in `map ...`)",
		})
	}

	// 3. kitty.conf readable
	confPath := kconf.Default()
	if _, err := os.Stat(confPath); err == nil {
		out = append(out, doctorCheck{Name: "kitty.conf", Status: "ok", Detail: confPath})
	} else {
		out = append(out, doctorCheck{
			Name:   "kitty.conf",
			Status: "err",
			Detail: confPath + ": " + err.Error(),
			Hint:   "create the file or set $KITTY_CONFIG_DIRECTORY",
		})
		// Skip checks that depend on the file we just failed to find.
		return out
	}

	conf, err := kconf.Load(confPath)
	if err != nil {
		out = append(out, doctorCheck{
			Name:   "kitty.conf parse",
			Status: "err",
			Detail: err.Error(),
		})
		return out
	}

	// 4. allow_remote_control — needed for session save / autosave
	switch conf.Get("allow_remote_control") {
	case "yes":
		out = append(out, doctorCheck{Name: "allow_remote_control", Status: "ok", Detail: "yes"})
	case "":
		out = append(out, doctorCheck{
			Name:   "allow_remote_control",
			Status: "warn",
			Detail: "not set — session save/restore will fail",
			Hint:   "add `allow_remote_control yes` to kitty.conf",
		})
	default:
		out = append(out, doctorCheck{
			Name:   "allow_remote_control",
			Status: "info",
			Detail: conf.Get("allow_remote_control"),
		})
	}

	// 5. listen_on — also needed for remote-control reachability
	if v := conf.Get("listen_on"); v == "" {
		out = append(out, doctorCheck{
			Name:   "listen_on",
			Status: "warn",
			Detail: "not set — psps can't reach kitty over a known socket",
			Hint:   "add `listen_on unix:/tmp/kitty-{kitty_pid}` to kitty.conf",
		})
	} else {
		out = append(out, doctorCheck{Name: "listen_on", Status: "ok", Detail: v})
	}

	// 6. KITTY_LISTEN_ON env var: present, reachable
	listen := os.Getenv("KITTY_LISTEN_ON")
	switch {
	case listen == "" && kitty.PidCount() == 0:
		out = append(out, doctorCheck{
			Name:   "KITTY_LISTEN_ON",
			Status: "info",
			Detail: "unset (no running kitty either — fine if you're not in a kitty terminal)",
		})
	case listen == "":
		out = append(out, doctorCheck{
			Name:   "KITTY_LISTEN_ON",
			Status: "warn",
			Detail: "unset in this shell — session save in this terminal will fall back to the global socket",
			Hint:   "kitty exports this automatically when listen_on is set; check your shell init for unsets",
		})
	default:
		reachable := probeListenSocket(listen)
		if reachable == "" {
			out = append(out, doctorCheck{Name: "KITTY_LISTEN_ON", Status: "ok", Detail: listen})
		} else {
			out = append(out, doctorCheck{
				Name:   "KITTY_LISTEN_ON",
				Status: "err",
				Detail: listen,
				Hint:   reachable,
			})
		}
	}

	// 7. running kitty processes
	if n := kitty.PidCount(); n > 0 {
		out = append(out, doctorCheck{
			Name: "running kitty", Status: "ok",
			Detail: fmt.Sprintf("%d process(es) — `psps reload` and theme apply will signal them", n),
		})
	} else {
		out = append(out, doctorCheck{
			Name:   "running kitty",
			Status: "info",
			Detail: "no running kitty processes (reloads will be no-ops)",
		})
	}

	// 8. themes directory seeded
	if entries, err := os.ReadDir(themelib.Dir()); err != nil {
		out = append(out, doctorCheck{
			Name:   "themes dir",
			Status: "err",
			Detail: themelib.Dir() + ": " + err.Error(),
			Hint:   "run any `psps theme` command to seed the default themes",
		})
	} else {
		n := 0
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".conf") {
				n++
			}
		}
		out = append(out, doctorCheck{
			Name:   "themes dir",
			Status: "ok",
			Detail: fmt.Sprintf("%d themes in %s", n, themelib.Dir()),
		})
	}

	// 9. sessions directory writeable
	if err := probeWritable(sessionlib.Dir()); err != nil {
		out = append(out, doctorCheck{
			Name:   "sessions dir",
			Status: "warn",
			Detail: sessionlib.Dir() + ": " + err.Error(),
		})
	} else {
		out = append(out, doctorCheck{
			Name:   "sessions dir",
			Status: "ok",
			Detail: sessionlib.Dir(),
		})
	}

	// 10. backups directory (sibling of kitty.conf)
	backupsDir := filepath.Join(filepath.Dir(confPath), "backups")
	if err := probeWritable(backupsDir); err != nil {
		out = append(out, doctorCheck{
			Name:   "backups dir",
			Status: "warn",
			Detail: backupsDir + ": " + err.Error(),
		})
	} else {
		out = append(out, doctorCheck{Name: "backups dir", Status: "ok", Detail: backupsDir})
	}

	// 11. host info — useful when users paste doctor output in bug reports
	out = append(out, doctorCheck{
		Name:   "host",
		Status: "info",
		Detail: fmt.Sprintf("%s/%s · psps %s", runtime.GOOS, runtime.GOARCH, versionString()),
	})

	return out
}

// probeListenSocket dials a kitty listen_on address to check it's actually
// accepting connections. Returns "" if reachable, or a human hint otherwise.
// Handles unix:/path and tcp:host:port forms.
func probeListenSocket(addr string) string {
	if strings.HasPrefix(addr, "unix:") || strings.HasPrefix(addr, "unix-abstract:") {
		path := strings.TrimPrefix(addr, "unix:")
		path = strings.TrimPrefix(path, "unix-abstract:")
		if path == "" {
			return "socket path is empty"
		}
		c, err := net.DialTimeout("unix", path, 500*time.Millisecond)
		if err != nil {
			return "can't connect to " + path + ": " + err.Error() + " (kitty may have exited)"
		}
		_ = c.Close()
		return ""
	}
	if strings.HasPrefix(addr, "tcp:") {
		hostPort := strings.TrimPrefix(addr, "tcp:")
		c, err := net.DialTimeout("tcp", hostPort, 500*time.Millisecond)
		if err != nil {
			return "can't connect to " + hostPort + ": " + err.Error()
		}
		_ = c.Close()
		return ""
	}
	// Unknown scheme — don't fail the check, just note we can't probe it.
	return ""
}

// probeWritable creates the dir if missing and tries a transient write to
// confirm we have permission. Cleans up after itself.
func probeWritable(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".psps-doctor-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}

// formatCheck renders a single check row for text output. Colors come from
// the same palette used elsewhere in the TUI so `psps doctor` looks at home
// next to the rest of the tool.
func formatCheck(c doctorCheck, color bool) string {
	marker, sty := doctorMarker(c.Status, color)
	head := marker + "  " + padRight(c.Name, 22) + sty.Render(c.Detail)
	if c.Hint == "" {
		return head
	}
	hint := lipgloss.NewStyle().Foreground(lipgloss.Color(ui.Muted)).Render("   ↳ " + c.Hint)
	if !color {
		hint = "   ↳ " + c.Hint
	}
	return head + "\n" + hint
}

func doctorMarker(status string, color bool) (string, lipgloss.Style) {
	plain := lipgloss.NewStyle()
	if !color {
		switch status {
		case "ok":
			return "[ok]  ", plain
		case "warn":
			return "[!]   ", plain
		case "err":
			return "[ERR] ", plain
		case "info":
			return "[i]   ", plain
		}
		return "[?]   ", plain
	}
	switch status {
	case "ok":
		return ui.OkStyle.Render("✓") + "  ", lipgloss.NewStyle()
	case "warn":
		return ui.WarnStyle.Render("!") + "  ", ui.WarnStyle
	case "err":
		return ui.ErrStyle.Render("✗") + "  ", ui.ErrStyle
	case "info":
		return lipgloss.NewStyle().Foreground(lipgloss.Color(ui.Muted)).Render("·") + "  ",
			lipgloss.NewStyle().Foreground(lipgloss.Color(ui.Muted))
	}
	return "?  ", plain
}

func padRight(s string, n int) string {
	if len(s) >= n {
		return s + " "
	}
	return s + strings.Repeat(" ", n-len(s))
}
