// Package kitty drives the running kitty instance(s) — used to apply config
// changes live by sending SIGUSR1 (kitty's "reload config" signal).
package kitty

import (
	"context"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Reload signals every running kitty process to reread its config.
//
// Best-effort: errors finding processes or signalling them are returned, but
// callers usually want to surface them as a warning rather than abort the
// save flow that triggered this call.
func Reload() error {
	pids, err := kittyPids()
	if err != nil {
		return err
	}
	self := os.Getpid()
	for _, pid := range pids {
		if pid == self {
			continue
		}
		if proc, err := os.FindProcess(pid); err == nil {
			_ = proc.Signal(syscall.SIGUSR1)
		}
	}
	return nil
}

// PidCount returns how many running kitty processes we'd signal. Useful for
// status messages: "reloaded N kitty windows".
func PidCount() int {
	pids, _ := kittyPids()
	n := 0
	self := os.Getpid()
	for _, p := range pids {
		if p != self {
			n++
		}
	}
	return n
}

// PreviewColors applies a set of color directives to every running kitty
// window using `kitty @ set-colors --all`. Unlike Reload(), this does NOT
// touch kitty.conf on disk — so the change is purely live and reverts on the
// next config reread. Used by the TUI's theme-hover preview.
//
// Returns silently if there's no reachable kitty instance — preview is a
// nice-to-have; we never want to break the TUI because remote-control isn't
// configured.
func PreviewColors(colors map[string]string) error {
	if len(colors) == 0 {
		return nil
	}
	args := []string{"@"}
	if listen := os.Getenv("KITTY_LISTEN_ON"); listen != "" {
		args = append(args, "--to", listen)
	}
	args = append(args, "set-colors", "--all")
	// Deterministic order so a sequence of identical maps produces identical
	// argv (helps if tools dedupe / cache invocations).
	keys := make([]string, 0, len(colors))
	for k := range colors {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, k+"="+colors[k])
	}
	return runKittyCmd(args)
}

// ResetPreview undoes any preview state — kitty rereads its config defaults
// for all colors. Cheap; safe to call even if no preview was active.
func ResetPreview() error {
	args := []string{"@"}
	if listen := os.Getenv("KITTY_LISTEN_ON"); listen != "" {
		args = append(args, "--to", listen)
	}
	args = append(args, "set-colors", "--reset")
	return runKittyCmd(args)
}

// runKittyCmd shells out to the `kitty` binary with a short timeout. The
// preview path is on the user's keystroke critical path — never block the TUI
// waiting for a slow socket.
func runKittyCmd(args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kitty", args...)
	return cmd.Run()
}

func kittyPids() ([]int, error) {
	out, err := exec.Command("pgrep", "-x", "kitty").Output()
	if err != nil {
		// pgrep exits 1 when nothing matched — not an error for us.
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return nil, nil
		}
		return nil, err
	}
	var pids []int
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		if pid, err := strconv.Atoi(line); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}
