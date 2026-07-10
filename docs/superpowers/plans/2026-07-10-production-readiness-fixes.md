# Production Readiness Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Superseded CLI task:** Task 3 records work completed before the interactive
> CLI was removed. Do not execute or restore that task. Final CLI scope and
> delivery are defined by
> [Remove Gig CLI REPL Implementation Plan](2026-07-10-remove-cli-repl.md).

**Goal:** Fix Gig's confirmed concurrency, cancellation, CLI, and GitHub Actions failures and prove the resulting pull request is green.

**Architecture:** Keep interpreter activation state per call, and make only interpreter-owned blocking channel operations context-aware through small `reflect.Select` helpers. Keep arbitrary host-function cancellation out of scope, remove the superseded interactive CLI surface through the linked follow-up plan, and extend GitHub Actions so both Go modules and the supported Go lines are enforced.

**Tech Stack:** Go 1.23.1+, `golang.org/x/tools/go/ssa`, `reflect.Select`, `context.Context`, GitHub Actions, GolangCI-Lint v2.4.0, Gosec v2.27.1, Govulncheck.

## Global Constraints

- Work on `codex/production-readiness-fixes`, never directly on `main`.
- Preserve the existing concurrency edits and their untracked regression test/document.
- Do not commit `.workbuddy` or unrelated local files.
- Do not forcibly cancel arbitrary registered host functions.
- Do not skip tests, increase timeouts to hide deadlocks, or use `-no-fail` on security checks.
- Do not reduce readability to satisfy lint: prefer named helpers and ordinary control flow; avoid mass modernization and broad lint suppression.
- Keep Go 1.23.1 as the compatibility floor and test Go 1.23.x through 1.26.x in CI.
- Do not add automated release publishing or modify `reference/onefun`.

---

### Task 1: Preserve and validate the existing concurrency root-cause fix

**Files:**
- Modify: `internal/interp/defer_panic.go`
- Modify: `internal/interp/engine.go`
- Modify: `internal/interp/frame.go`
- Modify: `internal/interp/ops.go`
- Create: `tests/concurrency_race_test.go`
- Create: `docs/concurrency-race-root-cause.md`

**Interfaces:**
- Consumes: existing `program.callSSA`, `typeResolver.resolveType`, deferred interpreted functions, and builtin `recover` handling.
- Produces: per-resolution recursion tracking and per-call panic-frame traversal with no shared mutable panic or in-flight resolution state.

- [ ] **Step 1: Review the current diff without rewriting it**

```bash
git diff -- internal/interp/defer_panic.go internal/interp/engine.go internal/interp/frame.go internal/interp/ops.go
sed -n '1,220p' tests/concurrency_race_test.go
sed -n '1,240p' docs/concurrency-race-root-cause.md
```

Expected: shared `program.panicFrame` and `typeResolver.inFlight` are gone; tests exercise nested goroutines and concurrent panic/recover.

- [ ] **Step 2: Run the focused race regressions**

```bash
go test -race -count=1 -run '^(TestConcurrentNestedGoroutines|TestConcurrentPanicRecover)$' ./tests
```

Expected: PASS with no race report or timeout.

- [ ] **Step 3: Commit only the existing concurrency fix**

```bash
git add internal/interp/defer_panic.go internal/interp/engine.go internal/interp/frame.go internal/interp/ops.go tests/concurrency_race_test.go docs/concurrency-race-root-cause.md
git commit -m "fix: isolate concurrent interpreter state"
```

### Task 2: Make interpreter-owned channel operations cancellable

**Files:**
- Create: `cancellation_test.go`
- Modify: `internal/interp/goroutine.go`
- Modify: `internal/interp/ops.go`
- Modify: `gig.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: `frame.ctx`, channel `reflect.Value` objects, and `Program.RunWithContext`.
- Produces: `sendWithContext(context.Context, reflect.Value, reflect.Value) error` and `recvWithContext(context.Context, reflect.Value) (reflect.Value, bool, error)`.

- [ ] **Step 1: Write public regression tests before production code**

Create `cancellation_test.go` in package `gig`. Build this interpreted source once per test:

```go
const blockingChannelSource = `
func BlockSend() { ch := make(chan int); ch <- 1 }
func BlockReceive() { ch := make(chan int); <-ch }
func BlockSelect() { ch := make(chan int); select { case <-ch: } }
func ReadyReceive() int { ch := make(chan int, 1); ch <- 7; return <-ch }
`
```

Add separate tests named `TestRunWithContextCancelsBlockingSend`, `TestRunWithContextCancelsBlockingReceive`, `TestRunWithContextCancelsBlockingSelect`, and `TestRunWithContextPreservesReadyChannelOperation`. Cancellation tests use a 25 ms context deadline and call `RunWithContext` in a goroutine guarded by a 500 ms `select`; they require `errors.Is(err, context.DeadlineExceeded)`. The ready test requires value `7` and no error.

- [ ] **Step 2: Verify RED**

```bash
go test -count=1 -run '^TestRunWithContextCancelsBlocking' .
```

Expected: FAIL within 500 ms because at least one operation remains blocked.

- [ ] **Step 3: Add readable context-aware helpers**

Add `context` to `internal/interp/goroutine.go` and implement:

```go
func sendWithContext(ctx context.Context, channel, send reflect.Value) error {
	if ctx == nil || ctx.Done() == nil {
		channel.Send(send)
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	chosen, _, _ := reflect.Select([]reflect.SelectCase{
		{Dir: reflect.SelectSend, Chan: channel, Send: send},
		{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ctx.Done())},
	})
	if chosen == 1 {
		return ctx.Err()
	}
	return nil
}

func recvWithContext(ctx context.Context, channel reflect.Value) (reflect.Value, bool, error) {
	if ctx == nil || ctx.Done() == nil {
		value, ok := channel.Recv()
		return value, ok, nil
	}
	if err := ctx.Err(); err != nil {
		return reflect.Value{}, false, err
	}
	chosen, value, ok := reflect.Select([]reflect.SelectCase{
		{Dir: reflect.SelectRecv, Chan: channel},
		{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ctx.Done())},
	})
	if chosen == 1 {
		return reflect.Value{}, false, ctx.Err()
	}
	return value, ok, nil
}
```

Change `runSend` to call `sendWithContext(fr.ctx, rv, rx)`. Change channel receive in `runUnOp` to call `recvWithContext(fr.ctx, rv)` and propagate its error. For `runSelect`, return a pre-existing context error before selecting; for blocking select, append a receive case for a non-nil `ctx.Done()` and return `ctx.Err()` when selected. Preserve the existing non-blocking default index adjustment.

- [ ] **Step 4: Document the host boundary**

Update the `RunWithContext` comment in `gig.go` and the context-cancellation feature in `README.md`: interpreter-owned execution observes cancellation; arbitrary registered host functions must implement cancellation themselves, usually through an explicit context argument.

- [ ] **Step 5: Verify GREEN and race safety**

```bash
go test -count=1 -run '^(TestRunWithContextCancelsBlocking|TestRunWithContextPreservesReadyChannelOperation)' .
go test -race -count=1 -run '^(TestRunWithContextCancelsBlocking|TestRunWithContextPreservesReadyChannelOperation)' .
go test -race -count=1 ./internal/interp ./tests
```

Expected: all commands PASS with no race report.

- [ ] **Step 6: Commit**

```bash
git add cancellation_test.go internal/interp/goroutine.go internal/interp/ops.go gig.go README.md
git commit -m "fix: cancel blocking interpreter channel operations"
```

### Task 3 (superseded): Bound plugin subprocesses and align the CLI module

This task is retained only as implementation history. Its version alignment
remains valid, but the plugin-manager code and tests are deleted by the
superseding plan.

**Files:**
- Create: `cmd/gig/pluginmgr/manager_test.go`
- Modify: `cmd/gig/pluginmgr/manager.go`
- Modify: `cmd/gig/go.mod`
- Modify: `cmd/gig/go.sum`

**Interfaces:**
- Consumes: `Manager.LoadPackage(string) error` and the plugin build pipeline.
- Produces: `Manager.LoadPackageContext(context.Context, string) error`; context-taking internal pipeline methods.

- [ ] **Step 1: Write failing plugin context tests**

In `manager_test.go`, construct a manager with `t.TempDir()` and initialized maps. Add:

```go
func TestLoadPackageContextReturnsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := newTestManager(t).LoadPackageContext(ctx, "example.invalid/canceled")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("LoadPackageContext error = %v, want context.Canceled", err)
	}
}

func TestNewManagerUsesFiveMinuteCommandTimeout(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := NewManager().commandTimeout; got != 5*time.Minute {
		t.Fatalf("commandTimeout = %v, want 5m", got)
	}
}

func TestDownloadPackagePreservesPackageError(t *testing.T) {
	err := newTestManager(t).downloadPackage(context.Background(), "\x00")
	if !errors.Is(err, ErrPackageNotFound) || !strings.Contains(err.Error(), "\x00") {
		t.Fatalf("downloadPackage error = %q, want ErrPackageNotFound with path", err)
	}
}
```

- [ ] **Step 2: Verify RED**

```bash
cd cmd/gig && go test -count=1 ./pluginmgr
```

Expected: FAIL because the context API, timeout field, and method signatures are absent.

- [ ] **Step 3: Implement the bounded API and propagate context**

Add `const defaultPluginCommandTimeout = 5 * time.Minute`, add `commandTimeout time.Duration` to `Manager`, and initialize it in `NewManager`. `LoadPackage` creates a timeout using the configured value (falling back to the default when non-positive), then delegates to `LoadPackageContext`. `LoadPackageContext` normalizes nil context, returns `ctx.Err()` before work, and retains the current registration/platform/lock/cache/load flow.

Use these exact internal signatures:

```go
func (pm *Manager) buildAndLoad(ctx context.Context, pkgPath string) error
func (pm *Manager) downloadPackage(ctx context.Context, pkgPath string) error
func (pm *Manager) generateWrapper(ctx context.Context, pkgPath string) (string, error)
func (pm *Manager) generatePluginCode(ctx context.Context, pkgPath string) ([]byte, error)
func (pm *Manager) getExportedSymbols(ctx context.Context, pkgPath string) (*ExportedSymbols, error)
func (pm *Manager) buildPlugin(ctx context.Context, pkgPath, wrapperPath string) (string, error)
func findGigRoot(ctx context.Context) (string, error)
```

Return `ctx.Err()` before each subprocess and whenever `CommandContext` failed due to cancellation. Do not fall back to reflection after canceled symbol discovery. For non-cancellation failures, preserve `ErrPackageNotFound`, `ErrPluginBuildFailed`, and the current descriptive wrappers.

- [ ] **Step 4: Align Gig and fix the three readable lint findings**

Set `github.com/t04dJ14n9/gig v1.7.7` in `cmd/gig/go.mod`. Replace the three reported `WriteString(fmt.Sprintf(...))` expressions with `fmt.Fprintf(&sb, ...)` while preserving generated bytes. Run `cd cmd/gig && go mod tidy`.

- [ ] **Step 5: Verify GREEN**

```bash
cd cmd/gig && go test -count=1 ./pluginmgr
cd cmd/gig && go test -race -count=1 ./...
cd cmd/gig && golangci-lint run --timeout=5m ./...
```

Expected: all commands PASS with no lint finding.

- [ ] **Step 6: Commit**

```bash
git add cmd/gig/pluginmgr/manager.go cmd/gig/pluginmgr/manager_test.go cmd/gig/go.mod cmd/gig/go.sum
git commit -m "fix: bound plugin commands and align cli version"
```

### Task 4: Remove study code and harden GitHub Actions

**Files:**
- Delete: `study_ast/main.go`
- Modify: `.github/workflows/go.yml`

**Interfaces:**
- Consumes: root and CLI Go modules and the GitHub `Go` workflow.
- Produces: mandatory root, CLI, lint, and pinned security jobs.

- [ ] **Step 1: Delete `study_ast`**

Delete `study_ast/main.go` and remove the empty directory.

- [ ] **Step 2: Expand root CI and add CLI CI**

Set root `strategy.fail-fast: false` and matrix Go versions `1.23.x`, `1.24.x`, `1.25.x`, and `1.26.x`. Keep dependency verification, build, race test, and atomic coverage steps.

Add a CLI job with Go `1.23.x` and `1.26.x`, `defaults.run.working-directory: cmd/gig`, `cache-dependency-path: cmd/gig/go.sum`, then run `go mod download`, `go mod verify`, `go build -v ./...`, and `go test -v -race ./...`.

- [ ] **Step 3: Lint both modules readably**

Keep GolangCI-Lint v2.4.0. Add a second `golangci/golangci-lint-action@v9` invocation with `working-directory: cmd/gig` and `args: --timeout=5m`. Do not add broad disables or rewrite clear code into compact forms.

- [ ] **Step 4: Pin and enforce Gosec**

Use `securego/gosec@v2.27.1` and remove `-no-fail`, preserving generated-code and directory exclusions.

- [ ] **Step 5: Run local workflow equivalents**

```bash
go test -count=1 ./...
go test -race -count=1 ./...
GOTOOLCHAIN=go1.23.1 go test -race -count=1 ./...
(cd cmd/gig && go test -race -count=1 ./...)
(cd cmd/gig && GOTOOLCHAIN=go1.23.1 go test -race -count=1 ./...)
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.4.0 run --timeout=5m ./...
(cd cmd/gig && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.4.0 run --timeout=5m ./...)
go run github.com/securego/gosec/v2/cmd/gosec@v2.27.1 -exclude=G115 -exclude-generated -exclude-dir=stdlib/packages -exclude-dir=examples -exclude-dir=benchmarks -exclude-dir=tests -exclude-dir=reference -exclude-dir=gentool -exclude-dir=cmd/gig ./...
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

Expected: every command exits 0 using the same GolangCI-Lint version as CI.

- [ ] **Step 6: Commit**

```bash
git add .github/workflows/go.yml
git commit -m "ci: enforce production readiness gates"
```

### Task 5: Full verification and GitHub delivery

**Files:**
- Review: every file changed from `main`
- Exclude: `.workbuddy/**`

**Interfaces:**
- Consumes: completed commits and authenticated GitHub CLI.
- Produces: a draft PR with every required GitHub Actions job successful.

- [ ] **Step 1: Verify formatting, modules, and cross-builds**

```bash
changed_go=$(git diff --name-only --diff-filter=ACMR main...HEAD -- '*.go')
test -z "$changed_go" || test -z "$(gofmt -l $changed_go)"
go mod verify
go mod tidy -diff
(cd cmd/gig && go mod verify)
(cd cmd/gig && go mod tidy -diff)
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...
(cd cmd/gig && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...)
(cd cmd/gig && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...)
git diff --check main...HEAD
```

Expected: all commands exit 0.

- [ ] **Step 2: Run stress and fuzz checks**

```bash
(cd benchmarks && go test -count=1 ./...)
for fuzz in FuzzIntNarrowing FuzzUintNarrowing FuzzFloatNarrowing FuzzBitwise FuzzStringOps FuzzMapOps FuzzChannelOps FuzzSliceOps FuzzStructAccess FuzzArithmeticEdgeCases FuzzFloatSpecialValues; do
  go test ./tests -run '^$' -fuzz "^${fuzz}$" -fuzztime=3s -parallel=4 || exit 1
done
```

Expected: stress tests and every fuzz campaign PASS.

- [ ] **Step 3: Review readability and scope**

```bash
git diff --stat main...HEAD
git diff main...HEAD -- internal/interp cmd/gig .github/workflows/go.yml README.md gig.go
git status --short
```

Expected: control flow is clear; no unrelated file is committed; only pre-existing unrelated `.workbuddy` may remain untracked.

- [ ] **Step 4: Push and open a draft PR**

Push `codex/remove-cli-repl` to the GitHub remote and create a draft PR targeting `main` with the root cause, fix summary, and exact verification evidence.

- [ ] **Step 5: Watch and repair GitHub Actions**

```bash
gh pr checks --watch --fail-fast=false
```

For any failure, inspect its run/job logs, reproduce locally, add a regression test for behavior changes, fix the root cause, push, and watch again. Completion requires every required GitHub Actions job on the PR to report success.
