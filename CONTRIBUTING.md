# Contributing

Thanks for considering a contribution. psps is a small Go project — most PRs
should be tight and focused.

## Before opening a PR

1. **Open an issue first** for anything larger than a bugfix or a small
   enhancement. Saves both sides time if the direction needs discussion.
2. **One concern per PR.** Refactors, bug fixes, and new features go in
   separate PRs.
3. **Keep the diff small.** If you find yourself touching unrelated code,
   split the cleanup into its own PR.

## Setup

```sh
git clone https://github.com/elythi0n/psps
cd psps
make build
make test
```

Requirements: Go 1.22+ (the module declares whatever's in `go.mod`), `kitty`
installed if you want to run integration paths locally.

## Checks before pushing

```sh
make fmt                                                         # gofmt + goimports
go vet ./...
go test ./...
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
```

All four must pass. CI will run them again on your PR.

If you're using golangci-lint locally, it'll pick up `.golangci.yml`
automatically:

```sh
golangci-lint run
```

## Code style

- Follow standard Go conventions. `gofmt` is non-negotiable.
- Doc comments on exported types, functions, and methods — even when the
  intent feels obvious. `godoc` is the project's reference.
- Errors: lowercase first letter, no trailing period, wrap with `%w` when
  surfacing upstream errors.
- Don't add comments that restate the code. Comments should explain *why*,
  not *what*. The existing codebase uses comments to flag subtle invariants
  and to record "we tried X, it didn't work because Y" context — match that
  style.
- Keep functions short. If a `cmd*` handler in `internal/cli/` is getting
  long, extract logic into the relevant `internal/<feature>/` package.

## Architecture rules of thumb

- **All mutations go through `apply.Save`.** That's the single place that
  handles backup → write → reload. New commands should not write `kitty.conf`
  directly.
- **CLI handlers are thin.** They parse flags, call into `internal/<feature>/`
  libraries, and render the result. Business logic belongs in the feature
  packages.
- **The TUI and CLI share the feature libraries.** If a behavior should be
  available in both, put it in `internal/<feature>/` and call it from both
  sides.
- **`--json` is part of the public contract.** Any new CLI command must
  support `--json`; agent-facing output goes to stdout (errors included), with
  shapes documented in [`AGENTS.md`](AGENTS.md).

## Tests

Where they make sense, add tests under the relevant `internal/<feature>/`
package. The existing test files (`internal/themelib/`, `internal/profile/`,
`internal/sessionlib/`, `internal/installer/`, `internal/logfile/`) are good
references for the testing style — table-driven, hermetic, no global state.

UI behavior in the TUI panes is currently untested. PRs that add coverage
there are welcome.

## Commit messages

Conventional commits aren't enforced, but please:

- Lead with a short imperative summary (≤72 chars).
- A blank line, then a body explaining *why* the change is needed if it
  isn't obvious from the title.
- Reference the issue number if applicable.

## Filing issues

- **Bugs:** include the output of `psps doctor`, your kitty version
  (`kitty --version`), and the smallest reproducer you can manage.
- **Feature requests:** describe the use case before the implementation.
  "I want X" is more useful than "add a flag for Y."
