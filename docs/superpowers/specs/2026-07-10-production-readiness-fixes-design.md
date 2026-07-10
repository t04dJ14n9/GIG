# Production Readiness Fixes Design

## Goal

Make Gig's confirmed interpreter, CLI, and GitHub Actions failures deterministic,
tested, and releasable without weakening readability or hiding failures behind
larger timeouts, skipped tests, broad lint suppressions, or non-failing security
checks.

## Current Evidence

- GitHub Actions run `28846310871` failed on Go 1.25 after the ten-minute test
  timeout.
- The blocked test was
  `TestConcurrentStatefulFixtures/NestedGoroutineSum`.
- Its stack trace ended in `internal/interp/ops.go` at the direct
  `reflect.Value.Recv` call.
- Local inspection found direct blocking channel send, receive, and select
  operations that do not observe the execution context.
- Local regression work already removes shared panic-frame and type-resolution
  state that was unsafe across interpreted goroutines. Those edits, their
  regression test, and their root-cause document are part of this change.
- `cmd/gig/go.mod` depends on Gig v1.7.6 even though v1.7.7 exists for both the
  root and CLI modules.
- The CLI's interactive mode and its plugin loader are redundant product
  surface; their complete removal is specified separately.
- `study_ast` is an untracked experiment that breaks the repository-wide
  default test/vet gate and is not production code.

## Scope

### Interpreter-owned cancellation

Blocking interpreter-owned channel operations must observe the context passed
to `RunWithContext`:

- channel send;
- channel receive, including comma-ok receives;
- blocking select.

The implementation will use small, plainly named helpers around
`reflect.Select`. Each helper will select between the requested channel
operation and `ctx.Done()`, returning `ctx.Err()` when cancellation wins.
Non-blocking select will retain its Go semantics and only add an inexpensive
pre-operation context check.

Arbitrary registered host functions are explicitly outside this guarantee.
Gig cannot safely terminate an arbitrary Go function. Public documentation will
state that host functions must manage their own cancellation, normally through
an explicit `context.Context` argument.

### Existing concurrency corrections

The current edits in these files remain in scope because they address the
remote concurrency timeout and race-sensitive panic/recover behavior:

- `internal/interp/defer_panic.go`;
- `internal/interp/engine.go`;
- `internal/interp/frame.go`;
- `internal/interp/ops.go`;
- `tests/concurrency_race_test.go`;
- `docs/concurrency-race-root-cause.md`.

The implementation must not overwrite or silently discard those existing
changes.

### CLI version and interactive-mode removal

- Update `cmd/gig/go.mod` from Gig v1.7.6 to v1.7.7 and refresh only the module
  metadata required by that version change.
- The later [CLI REPL removal design](2026-07-10-remove-cli-repl-design.md)
  supersedes the plugin-command timeout design: remove the interactive command,
  its implementation, the plugin manager, and terminal dependencies completely.
- Keep the v1.7.7 correction after removal because it fixes the independent CLI
  version mismatch.

### Repository cleanup

Delete the entire `study_ast` directory. Do not include `.workbuddy` or other
unrelated untracked files in commits or the pull request.

## Readability and Lint Policy

Readability is a release requirement, not a secondary concern.

- Fix concrete lint findings in files touched by this work.
- Prefer clear control flow and descriptive helpers over compressed expressions.
- Do not perform repository-wide modernization solely to satisfy newly added
  style checks.
- Do not disable a correctness or security check to make CI green.
- A narrowly scoped lint configuration change is allowed only when the rule is
  purely stylistic, conflicts with the declared Go compatibility range, and the
  rationale is recorded next to the configuration.

## Test-Driven Development

Each behavior change starts with a test that is observed failing for the
expected reason.

### Interpreter regression tests

Add black-box tests through the public Gig API for:

1. a blocked unbuffered send returning `context.DeadlineExceeded`;
2. a blocked receive returning `context.DeadlineExceeded`;
3. a blocking select returning `context.DeadlineExceeded`;
4. ready channel operations continuing to return their normal values.

The existing nested-goroutine and concurrent panic/recover regression tests run
under the race detector and remain part of the release gate.

### CLI removal regression test

Add a subprocess test proving that `gig repl` follows the normal unknown-command
path and no longer appears in usage. Observe it failing against the
interactive-mode implementation before deleting production code.

## GitHub Actions

Update `.github/workflows/go.yml` without weakening any existing gate.

### Root module

Build and run race-enabled tests on:

- Go 1.23.x, the declared compatibility floor;
- Go 1.24.x;
- Go 1.25.x, where the current remote timeout occurs;
- Go 1.26.x, the current stable line.

### CLI module

Add a separate CLI matrix for Go 1.23.x and Go 1.26.x. Each entry will verify
dependencies, build, and run race-enabled tests from `cmd/gig`.

### Lint and security

- Run the repository's pinned GolangCI-Lint v2.4.0 policy for the root module
  and the CLI module.
- Pin Gosec to v2.27.1 rather than a moving `master` branch.
- Remove Gosec's `-no-fail` option so a real finding fails CI.
- Exclude non-production fixtures, benchmarks, examples, and the legacy
  reference tree from the hard gate.
- Exclude G115 centrally because Gig must implement Go's defined narrowing and
  wrapping conversions; changing those conversions would break language
  compatibility. Keep localized, explained `#nosec` annotations for explicit
  CLI file selection and compatibility-only legacy crypto registration.
- Keep generated-code exclusions because generated registration wrappers are
  reviewed through their generator and module build tests.

No automated GitHub Release or deployment workflow is added in this change.

## Delivery

The original fixes were isolated on `codex/production-readiness-fixes`. Final
delivery, including the superseding CLI removal, is isolated on
`codex/remove-cli-repl`.

Before publishing:

1. run focused red-green regression cycles;
2. run all root and CLI tests with the race detector;
3. run the declared minimum and current Go toolchains;
4. run both module linters with the CI-pinned version;
5. run Gosec v2.27.1 and Govulncheck;
6. cross-build Linux and Windows targets;
7. verify module metadata and formatting;
8. review the final diff for readability and accidental unrelated files.

Then commit the intentional files, push the branch to the GitHub remote, open a
draft pull request, and monitor GitHub Actions. Any failing check will be
root-caused and fixed; tests will not be skipped and timeouts will not be
increased to mask the defect.

## Success Criteria

- Blocking interpreter-owned send, receive, and select return the execution
  context error after cancellation.
- Ready channel operations retain Go-compatible behavior.
- Existing concurrent goroutine and panic/recover regressions pass under
  `go test -race`.
- The CLI uses Gig v1.7.7 and contains no interactive command, plugin manager,
  or terminal-line-editing dependency.
- `study_ast` is absent.
- Default root and CLI build/test commands pass.
- Root and CLI lint pass without readability-reducing rewrites.
- Gosec and Govulncheck report no actionable findings.
- Every GitHub Actions job on the draft pull request completes successfully.

## Non-Goals

- forcibly canceling arbitrary registered host functions;
- subprocess isolation for interpreted programs;
- automatic tag publishing or deployment;
- repairing the legacy `reference/onefun` module;
- unrelated repository-wide modernization;
- committing `.workbuddy` or other unrelated local files.
