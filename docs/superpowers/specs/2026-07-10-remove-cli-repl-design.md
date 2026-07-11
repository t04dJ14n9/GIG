# Remove Gig CLI REPL Design

## Goal

Remove the interactive REPL from the Gig CLI completely, including the
REPL-only plugin loader and terminal-line-editing dependencies, while preserving
the production-readiness interpreter fixes and the useful `init`, `gen`, and
`dump` commands.

## Decision

Use a hard deletion. The CLI will not retain a compatibility shim, deprecation
message, hidden flag, or separate plugin-loading command. After this change,
`gig repl` behaves exactly like any other unknown command: it prints the normal
unknown-command error and exits unsuccessfully.

This supersedes the plugin-manager timeout work in the production-readiness
design. The CLI must continue to depend on Gig v1.7.7, which fixes the separate
CLI version mismatch; only dependencies made unnecessary by REPL removal are
removed.

## Removal Boundary

### Command surface

- Remove `RunREPL` and the `repl` entry from the CLI command registry.
- Remove REPL descriptions, examples, and headings from general CLI usage.
- Keep the existing command-dispatch implementation straightforward and keep
  `init`, `gen`, and `dump` unchanged.

### Source packages

Delete the complete REPL and plugin-manager implementation:

- `cmd/gig/commands/repl.go`;
- every file under `cmd/gig/repl/`;
- every file under `cmd/gig/pluginmgr/`.

No plugin-manager package is retained because it has no consumer outside the
REPL. In particular, its Unix and Windows loaders, package cache, command
runners, wrapper generator, registry, context timeout, and tests all disappear
with the feature they support.

### Module dependencies

Remove `github.com/peterh/liner`, then run `go mod tidy` in `cmd/gig`.
`github.com/mattn/go-runewidth` should disappear transitively. Any other module
removed by `go mod tidy` must be confirmed unused rather than deleted manually.
The direct `github.com/t04dJ14n9/gig v1.7.7` requirement remains.

### Documentation

The active user and contributor documentation must describe only the remaining
CLI:

- rewrite the English and Chinese CLI guides around `init`, `gen`, and `dump`;
- update English and Chinese architecture summaries;
- update `CLAUDE.md`'s command inventory;
- mark the earlier plugin-manager plan and design as superseded so they cannot
  be mistaken for current product behavior.

Historical design documents may say that the REPL was removed, but no active
documentation may instruct users to run it or describe plugins as a supported
CLI capability.

## Test-Driven Removal

Before deleting production code, add a black-box test in `cmd/gig/main_test.go`.
The test runs the real `main` function in a subprocess and requires:

1. `gig repl` exits with status 1;
2. stderr contains `Unknown command: repl`;
3. the printed usage does not contain `gig repl`.

The test must first be observed failing against the current REPL-enabled CLI.
After the hard deletion, it must pass without adding production-only test hooks
or complicating command dispatch.

## Verification and Delivery

The complete production-readiness branch remains subject to all existing gates:

- root and CLI tests, including race-enabled tests;
- Go 1.23.1 compatibility and the current Go toolchain;
- root and CLI GolangCI-Lint v2.4.0;
- Gosec v2.27.1, Govulncheck, Actionlint, formatting, module tidiness, and
  cross-platform builds;
- a literal repository audit showing no active CLI registration, import,
  dependency, or user instruction remains;
- a focused final-diff review for readability and unrelated files.

Work is delivered from `codex/remove-cli-repl`. The branch is pushed to GitHub,
a pull request is opened, and every required GitHub Actions check must finish
successfully. CI failures are root-caused and fixed; checks are not skipped and
timeouts are not increased to conceal defects.

## Success Criteria

- `gig repl` is rejected as an unknown command and is absent from usage.
- `cmd/gig/commands/repl.go`, `cmd/gig/repl/`, and `cmd/gig/pluginmgr/` do not
  exist.
- The CLI module contains no `liner` or `go-runewidth` dependency.
- Active English and Chinese documentation describes only `init`, `gen`, and
  `dump`.
- The root interpreter and cancellation fixes remain unchanged and passing.
- `.workbuddy` and unrelated user files are absent from every commit and the PR.
- All local release gates and GitHub Actions checks pass.

## Non-Goals

- replacing the REPL with another interactive shell;
- extracting the REPL or plugin loader into another module;
- keeping a deprecation-only `repl` command;
- changing the Gig interpreter API or interpreter-owned cancellation boundary;
- adding release publishing or unrelated CLI features.
