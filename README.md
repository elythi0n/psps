# psps

A config manager for the [kitty](https://sw.kovidgoyal.net/kitty/) terminal.
Drives `kitty.conf` so you can switch themes, manage profiles, snapshot
sessions, and persist font-size changes — from a TUI or a scriptable CLI.

Every mutation goes through the same pipeline: snapshot the existing file,
write the new one, then signal running kitty processes (`SIGUSR1`) to reread.
You get backups and live reload for free.

## Features

- **TUI** — themes, settings, sessions, keybinds, fonts. Live theme preview on
  arrow-key hover (uses `kitty @ set-colors`; reverts on `esc` or moving away).
- **Themes** — bundled defaults (Catppuccin, Gruvbox, Nord, Rose-Pine,
  Tokyo-Night, Dracula). Install more from a URL, a git repo, or a local
  `.conf` file.
- **Profiles** — bundle theme + settings + keybinds + fonts into a named
  directory; apply or share as a unit.
- **Persistent zoom** — `psps zoom inc/dec/set` writes `font_size` into
  kitty.conf so the change survives reloads and new windows. Bind it in
  kitty.conf to replace the runtime-only `ctrl++` / `ctrl+-` defaults.
- **Sessions** — capture the current kitty layout, restore it later, or
  autosave on a background loop so the next launch picks up where you left
  off.
- **Generic key/value** — `psps set <key> <value>` for any kitty.conf
  directive, with `--dry-run` to preview the diff first.
- **Undo + backups** — every write is snapshotted; `psps undo` restores the
  last one.
- **Agent-friendly** — `--json` on every command emits a single JSON object on
  stdout (errors too — agents only parse one stream). See
  [`AGENTS.md`](AGENTS.md) for the full agent guide; `psps agent-guide`
  prints it to stdout.
- **`psps doctor`** — diagnoses common setup issues (missing
  `allow_remote_control`, unreachable `listen_on` socket, `psps` not on PATH).

## Install

### Pre-built binaries

Grab a release from
[GitHub Releases](https://github.com/elythi0n/psps/releases/latest) —
archives for Linux, macOS, and Windows on `amd64` and `arm64`:

```sh
# linux/amd64 example
curl -L https://github.com/elythi0n/psps/releases/latest/download/psps_0.1.0_linux_amd64.tar.gz | tar xz
sudo install -m 0755 psps /usr/local/bin/psps
```

### Debian / Ubuntu

```sh
curl -LO https://github.com/elythi0n/psps/releases/latest/download/psps_0.1.0_linux_amd64.deb
sudo dpkg -i psps_0.1.0_linux_amd64.deb
```

### Fedora / RHEL

```sh
sudo rpm -i https://github.com/elythi0n/psps/releases/latest/download/psps_0.1.0_linux_amd64.rpm
```

### Alpine

```sh
sudo apk add --allow-untrusted psps_0.1.0_linux_amd64.apk
```

### Arch Linux

A `PKGBUILD` is included. From a clone:

```sh
makepkg -si
```

### Go install

```sh
go install github.com/elythi0n/psps@latest
```

### From source

```sh
git clone https://github.com/elythi0n/psps
cd psps
make install-user    # builds and installs to ~/.local/bin
# or:
make install         # installs to /usr/local/bin (needs sudo)
```

## Usage

Launch the TUI:

```sh
psps
```

Quick CLI examples — see `psps help` for the full list.

```sh
# Themes
psps theme list
psps theme apply gruvbox-dark
psps theme install https://github.com/example/my-theme

# Font size that survives reloads
psps zoom inc 2
psps zoom set 14

# Generic kitty.conf edits, with a preview
psps set cursor_blink_interval 0.5 --dry-run
psps set cursor_blink_interval 0.5

# Sessions
psps session save my-layout
psps session restore my-layout

# Diagnose your setup
psps doctor
```

### Persistent zoom keybindings

Add to `~/.config/kitty/kitty.conf` to replace kitty's runtime-only zoom
defaults with the persistent version:

```kitty
map ctrl+equal        launch --type=background psps zoom inc 2
map ctrl+minus        launch --type=background psps zoom dec 2
map ctrl+0            launch --type=background psps zoom set 11
```

### For AI agents

`psps` is designed to work cleanly from inside agentic tools (Claude Code,
Cursor, etc.). Every command supports `--json`, install commands respect
`--yes` to skip interactive prompts, and the agent guide ships with the
binary.

```sh
psps agent-guide                              # prints AGENTS.md
psps theme list --json | jq '.themes[].name'
psps set font_size 14 --dry-run --json        # preview as JSON
```

## Development

```sh
make build            # → ./build/psps
make test             # go test ./...
make fmt              # gofmt + goimports
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
```

The codebase is a single Go module:

- `main.go` — the bubbletea root model + TUI shell
- `internal/cli/` — non-TUI entry point (the `psps theme apply X` etc. commands)
- `internal/panes/` — TUI panels: themes, settings, sessions, keybinds, fonts
- `internal/kconf/` — kitty.conf parser/writer (line-aware, comment-preserving)
- `internal/apply/`, `backups/`, `diff/` — the write pipeline
- `internal/themelib/`, `profile/`, `sessionlib/` — feature libraries
- `internal/installer/` — `theme/profile install` URL+git+file staging
- `internal/kitty/` — talks to running kitty (SIGUSR1, `@ set-colors`)

See [CONTRIBUTING.md](CONTRIBUTING.md) before opening a PR.

## License

[MIT](LICENSE)
