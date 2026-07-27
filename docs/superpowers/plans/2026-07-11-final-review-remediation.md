# Final Review Remediation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the deferred-method `recover` regressions and restore full Gosec coverage of the production CLI generator.

**Architecture:** Store the active deferred recovery target on each interpreter frame. Seed it only when entering a directly deferred callable, propagate it only through SSA's synthetic bound and thunk method adapters, and pass caller context through interface dispatch. Replace the CLI-wide generator scan exclusion with localized, explained G306 suppressions on the two intentional generated-source writes.

**Tech Stack:** Go 1.23.1+, `golang.org/x/tools/go/ssa`, the native-parity correctness harness, Gosec v2.27.1, GitHub Actions.

## Global Constraints

- Every interpreter behavior fix must first have a native-vs-interpreted parity case that fails on the current branch and passes after implementation.
- `recover()` may reach only the directly deferred callable; ordinary helper calls must continue to receive nil.
- Recovery state must remain frame-local and goroutine-local; do not reintroduce shared program panic state.
- Keep Go 1.23.1 compatibility.
- Do not commit or modify the user's untracked `.workbuddy/` files.

---

### Task 1: Preserve deferred recovery through method dispatch adapters

**Files:**
- Modify: `tests/testdata/panic_recover/main.go`
- Modify: `tests/correctness_test.go`
- Modify: `internal/interp/frame.go`
- Modify: `internal/interp/host_call.go`
- Modify: `internal/interp/ops.go`
- Modify: `internal/interp/defer_panic.go`

**Interfaces:**
- Consumes: `callSSA`, `runDeferRec`, `invokeMethodOn`, and SSA functions whose `Synthetic` description starts with `bound method wrapper for `.
- Produces: frame-local `recoverTarget *frame` propagation across only direct defer dispatch and SSA bound/thunk method adapters.

- [ ] **Step 1: Add native-parity cases**

Add exported testdata functions for a direct interface-method defer, concrete and interface bound method-value defers, a method-expression defer, a promoted-method parity control, and this negative control:

```go
func recoverThroughHelper() bool { return recover() != nil }

func RecoverHelperCannotRecover() (result int) {
	defer func() {
		if recover() != nil {
			result = 2
		}
	}()
	defer func() {
		if recoverThroughHelper() {
			result = 1
		}
	}()
	panic("boom")
}
```

Register each function in `panicRecoverTests` with its native function value.

- [ ] **Step 2: Verify the parity cases fail for the expected reason**

Run `go test ./tests -run 'TestPanicRecover/(RecoverWithDirectInterfaceMethod|RecoverWithConcreteMethodValue|RecoverWithInterfaceMethodValue|RecoverWithMethodExpression|RecoverWithPromotedMethod|RecoverHelperCannotRecover)' -count=1 -v`.

Expected on the original branch: the direct interface and bound-method cases fail because interpreted execution returns `interpreter panic: boom`, while the helper control passes. Against the first `$bound`-aware fix, the method-expression case must fail for the same reason while the promoted-method control passes.

- [ ] **Step 3: Add frame-local recovery targeting**

Add `recoverTarget *frame` to `frame`. When `callSSA` creates a frame, derive the target from its caller: a panicking caller is the direct deferred target; a caller whose SSA function is a synthetic bound or thunk adapter forwards its existing target; every other caller produces nil.

Recognize only x/tools adapters whose `Function.Synthetic` value begins with `bound method wrapper for ` or `thunk for `, and require the corresponding `$bound` or `$thunk` name suffix. Do not make generic `wrapper for ` functions transparent without a native-parity failure.

- [ ] **Step 4: Thread interface dispatch and consume the target**

Change `invokeMethodOn` to accept a caller frame and pass it into interpreted `callSSA`. Use the current frame for ordinary interface calls and the panicking frame for a directly deferred interface method. In `callBuiltin("recover")`, prefer the current panicking frame, then `fr.recoverTarget`; do not propagate that target through ordinary calls.

- [ ] **Step 5: Verify green and guard directness**

Run the command from Step 2, then `go test -race ./tests -run '^(TestPanicRecover|TestConcurrentPanicRecover|TestRecoverCannotCrossGoroutineBoundary)$' -count=1`.

Expected: all cases pass, including `RecoverHelperCannotRecover == 2`.

---

### Task 2: Scan the complete production CLI module with Gosec

**Files:**
- Modify: `cmd/gig/gentool/generator.go`
- Modify: `.github/workflows/go.yml`

**Interfaces:**
- Consumes: Gosec v2.27.1 rule G306.
- Produces: a CLI security command that scans `cmd/gig/gentool` and exits successfully with zero issues.

- [ ] **Step 1: Record the failing full scan**

From `cmd/gig`, run `go run github.com/securego/gosec/v2/cmd/gosec@v2.27.1 -exclude=G115 -exclude-generated ./...`.

Expected: two G306 findings at `gentool/generator.go` for generated Go source written with mode `0o666`.

- [ ] **Step 2: Localize the intentional suppressions**

Add an explained `#nosec G306` annotation to each generated-source `os.WriteFile(..., 0o666)` call. The explanation must state that generated Go source follows normal compiler-readable permissions and remains restricted by the caller's umask.

- [ ] **Step 3: Remove the broad exclusion**

Delete `-exclude-dir=gentool` from the CLI Gosec command in `.github/workflows/go.yml`.

- [ ] **Step 4: Verify the full scan**

Re-run the Step 1 command. Expected: exit status 0 and `Issues : 0`, with all six CLI files scanned.

---

### Task 3: Integrated verification

**Files:**
- Verify all files changed by Tasks 1 and 2.

**Interfaces:**
- Consumes: both completed fixes.
- Produces: merge-ready verification evidence without committing.

- [ ] **Step 1: Format and check module metadata**

Run `gofmt` on changed Go files, `go mod tidy -diff` in both modules, and `git diff --check`.

- [ ] **Step 2: Run complete race suites**

Run `go test -count=1 -race ./...` in the root and CLI modules.

- [ ] **Step 3: Cross-build Windows and review the final diff**

Run root and CLI `GOOS=windows GOARCH=amd64 go build ./...`, confirm only intended files changed, and request a final code review.
