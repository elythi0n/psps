# psps for agents

`psps` manages a user's kitty terminal config. It owns `~/.config/kitty/kitty.conf`,
keeps timestamped backups in `~/.config/kitty/backups/`, and signals running
kitty processes (SIGUSR1) on every mutation so the change takes effect
immediately.

Without arguments, `psps` launches a TUI — **do not invoke it that way from a
tool call**, it will take over the terminal. Always pass a subcommand.

## Always do this first

- Pass `--json` to every command. Output becomes a single JSON object on stdout;
  errors come back as `{"error":"...","exit":N}` on stdout as well, so you only
  need to read one stream. Exit codes are unchanged.
- For any mutation (`set`, `theme apply`, `font`, `zoom`, `profile apply`),
  preview first with `--dry-run --json`, show the diff to the user, then re-run
  without `--dry-run` only after confirmation.
- Pass `--yes` (or `-y`) to `theme install` and `profile install`. Without it
  those commands prompt interactively and will hang an agent invocation.
- Never edit `~/.config/kitty/kitty.conf` directly. Always go through
  `psps set <key> <value>` so backup + reload happen.

## Output shapes

```jsonc
// psps theme list --json
{"themes":[
  {"name":"gruvbox-dark","source":"bundled","path":"/home/.../themes/gruvbox-dark.conf"}
]}

// psps profile list --json
{"profiles":[
  {"name":"work","dir":"/home/.../profiles/work","components":["theme","settings"]}
]}

// psps session list --json
{"sessions":[
  {"name":"auto","path":"/home/.../sessions/auto.conf","modified":"2026-05-28T13:25:00Z"}
]}

// psps backups --json
{"backups":[
  {"name":"kitty.conf.20260528T132500","path":"/home/.../backups/...","when":"2026-05-28T13:25:00Z"}
]}

// psps get <key> --json
{"key":"font_size","value":"13.0","set":true}

// psps zoom get --json
{"font_size":13.0,"default":false}

// psps set <key> <val> --dry-run --json
// psps theme apply <name> --dry-run --json
// psps font ... --dry-run --json
{"changes":[
  {"key":"font_size","kind":"modified","before":"13.0","after":"14.0"}
],"summary":"1 changes (0+, 1~, 0-)"}

// psps theme apply <name> --json
{"applied":"gruvbox-dark","reloaded":2}

// psps set <key> <val> --json
{"key":"font_size","value":"14.0","reloaded":2}

// psps reload --json
{"reloaded":2}

// psps undo --json
{"restored":"kitty.conf.20260528T130000","reloaded":2}

// psps session save <name> --json
{"saved":"my-layout","path":"/home/.../sessions/my-layout.conf"}

// errors
{"error":"theme not found: gruvbocks-dark","exit":1}
```

## Common workflows

### Change the theme
```
psps theme list --json
psps theme apply <name> --dry-run --json   # preview
psps theme apply <name> --json
```

### Install a theme from a URL or file
```
psps theme install <url-or-path> --yes --json
```
Accepts raw URLs to a `.conf` file, git repo URLs (looks for a single `.conf`
at the root or under `themes/` / `colors/`), or local paths.

### Bump font size persistently
```
psps zoom get --json          # current size
psps zoom inc 2 --json        # +2pt
psps zoom dec 2 --json        # -2pt
psps zoom set 14 --json       # absolute
```
Unlike kitty's built-in `change_font_size`, this writes `font_size` into
kitty.conf so the change survives reloads and theme switches.

### Set any kitty.conf directive
```
psps get font_family --json
psps set font_family "JetBrains Mono" --dry-run --json
psps set font_family "JetBrains Mono" --json
```

### Snapshot / restore the kitty layout
```
psps session save my-layout --json
psps session list --json
psps session restore my-layout --json
psps session delete my-layout --json
```
Requires `allow_remote_control yes` and `listen_on unix:/tmp/kitty-{kitty_pid}`
in kitty.conf, plus `KITTY_LISTEN_ON` set in the env. If `session save` errors,
suggest the user enable remote control rather than retrying.

### Profiles (theme + settings + keybinds + fonts bundle)
```
psps profile list --json
psps profile install <url-or-path> --yes --json
psps profile apply <name> --json
psps profile remove <name> --json
```

### Undo the last change
```
psps backups --json           # see what's available
psps undo --json              # restore the most recent
```

## Things that will break

- `psps autosave run` is a foreground daemon. Never invoke it from a tool call
  without backgrounding (`nohup psps autosave run >/dev/null 2>&1 &`) and never
  wait on it — it never exits on its own.
- `psps session save` and `psps autosave enable` need kitty's remote control.
  If the user doesn't have `allow_remote_control yes` in kitty.conf, surface
  that root cause; retrying won't fix it.
- The bare `psps` command (no subcommand) launches the TUI. Never call it.
- `psps theme install` / `psps profile install` fetch from the network when
  given a URL. Treat them like any other network operation.

## Discoverability

`psps agent-guide` prints this document to stdout, so an agent that can run
shell commands can discover everything without reading any file:

```
psps agent-guide
```
