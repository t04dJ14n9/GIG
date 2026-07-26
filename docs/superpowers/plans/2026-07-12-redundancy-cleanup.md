# Redundancy Cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace duplicated interpreter execution paths with canonical helpers and remove redundant tests, dead code, and compatibility-only APIs.

**Architecture:** Keep the optimized normal-call path specialized. Consolidate only already-resolved static calls, builtin execution, and reflection conversion; then update tests and public callers to one canonical API per behavior.

**Tech Stack:** Go 1.23.1+, `golang.org/x/tools/go/ssa`, standard `testing`, golangci-lint.

## Global Constraints

- Preserve every pre-existing working-tree change and do not touch `.workbuddy/`.
- Backward compatibility is not required; do not retain deprecated aliases or no-op shims.
- Keep normal direct interpreted calls on the existing one-argument/one-result fast path.
- The performance result must remain within the existing allocation ceilings and comfortably inside the 3.0x benchmark ceiling.
- Use test-first red/green evidence for the host-goroutine correctness bug.
- Do not stage, commit, push, or create a pull request.

---

### Task 1: Canonicalize callable execution and reflection conversion

**Files:**
- Create: `internal/interp/goroutine_test.go`
- Create: `internal/interp/call_helpers.go`
- Modify: `internal/interp/goroutine.go`
- Modify: `internal/interp/defer_panic.go`
- Modify: `internal/interp/ops.go`
- Modify: `internal/interp/host_call.go`

**Interfaces:**
- Produces: `callStaticFunction(context.Context, *frame, *ssa.Function, []value.Value, int) ([]value.Value, error)`.
- Produces: `executeBuiltin(*frame, *ssa.Builtin, []value.Value) (value.Value, error)`.
- Produces: `reflectArgs(reflect.Type, []value.Value) ([]reflect.Value, error)` and `valuesFromReflect([]reflect.Value) ([]value.Value, error)`.

- [ ] **Step 1: Add the failing direct-host-goroutine test**

Register `example/async.Notify` on a fresh `importer.Registry`; the function
sends to a buffered channel. Build `func Spawn() { go async.Notify() }` with
the registry-backed host environment, call `Spawn`, and select between the
notification and a one-second timer.

- [ ] **Step 2: Verify red**

Run:

```bash
go test ./internal/interp -run TestGoHostFunctionExecutes -count=1 -v
```

Expected before implementation: FAIL because the notification times out;
`runGo` sends the body-less function to `callSSA`, which returns "has no body".

- [ ] **Step 3: Add the canonical helpers**

Implement the static dispatcher with this behavior:

```go
func (p *program) callStaticFunction(ctx context.Context, caller *frame, fn *ssa.Function, args []value.Value, depth int) ([]value.Value, error) {
	if len(fn.Blocks) == 0 {
		return p.callHostFunc(ctx, fn, args)
	}
	return p.callSSA(ctx, caller, fn, args, nil, depth)
}
```

Move the existing builtin switch unchanged into `executeBuiltin`, making its
`recover` case nil-frame safe. Make `callBuiltin` resolve operands through
`readValuesInto` and delegate to it. Delete `callBuiltinDirect`.

Implement reflection helpers using `p.converter`; preserve the existing rule
that arguments beyond `NumIn` use a nil target type, and wrap conversion errors
at call sites where method/argument context already exists.

- [ ] **Step 4: Route call sites through the helpers**

Use `readValuesInto` in `runGo` and `runDefer`. Use `callStaticFunction` in
`runGo` and `runDeferRec`; leave `runDirectInterpretedCall` unchanged. Use
`executeBuiltin` for normal, deferred, and goroutine builtins. Use the
reflection helpers in indirect calls, deferred function values, goroutine
function values, and reflected host methods.

- [ ] **Step 5: Verify green and surrounding semantics**

Run:

```bash
go test ./internal/interp -run 'Test(GoHostFunctionExecutes|CallResolvedHost|CallHostFunc|Interp.*Allocations|Recover|Panic)' -count=1 -v
go test ./tests -run 'Test(PanicRecover|Concurrency|External)' -count=1
```

Expected: all selected tests pass with pristine output.

---

### Task 2: Consolidate test harnesses and remove test-only duplication

**Files:**
- Modify: `tests/correctness_test.go`
- Modify: `tests/orphan_fixture_test.go`
- Modify: `internal/interp/perf_test.go`
- Modify: `internal/interp/diag_test.go`
- Modify: `tests/embedded_parity_test.go`
- Modify: `tests/benchmark_test.go`

**Interfaces:**
- Produces: one package-level `runCorrectnessCase` used by ordered and unordered correctness runners.
- Produces: a narrow allocation-measurement helper that accepts source, function name, run count, and a result checker.

- [ ] **Step 1: Establish characterization coverage**

Run:

```bash
go test ./tests -run 'Test(Correctness|Orphan|EmbeddedParity)' -count=1
go test ./internal/interp -run 'TestInterp.*AllocationsAreBounded' -count=1 -v
```

Expected: pass before refactoring.

- [ ] **Step 2: Consolidate correctness execution**

Move `runCorrectnessCase` beside `runTestSet` in `correctness_test.go` and make
`runTestSet` call it. Delete the duplicate body from `runTestSet` and the
definition from `orphan_fixture_test.go`.

- [ ] **Step 3: Consolidate allocation-test plumbing**

Add a helper with this responsibility:

```go
func measureProgramAllocs(t *testing.T, src, function string, runs int, check func(*testing.T, []value.Value)) float64
```

It builds once, warms once, checks the warm result, and returns
`testing.AllocsPerRun`. Each test retains its own source, expected-result
closure, run count, and ceiling.

- [ ] **Step 4: Remove test-only residue**

Delete `runDiagProgram`, `embeddedHostImportPath`,
`newBoundaryRejectionRegistry`, and `hostAcceptAny`. Remove the unused
`strconv`, `sort`, and `time` imports and their suppression assignments from
`benchmark_test.go`. Remove the second unreachable `Sort` condition from the
Third-party category.

- [ ] **Step 5: Verify**

Run the commands from Step 1 and:

```bash
go test ./tests -run '^TestBenchmarkSummary$' -count=1
```

Expected: all pass and allocation ceilings remain unchanged.

---

### Task 3: Remove compatibility-only APIs and production dead scaffolding

**Files:**
- Modify: `gig.go`
- Modify: `host/host.go`
- Modify: `host/registry_bridge.go`
- Modify: `internal/interp/frame.go`
- Modify: `internal/interp/defer_panic.go`
- Modify: `internal/interp/goroutine.go`
- Modify: `internal/interp/fuse_test.go`
- Modify: `internal/interp/frame_value_test.go`
- Modify: `internal/interp/call_storage_test.go`
- Modify: `tests/external_signature_test.go`
- Modify: `tests/githublibs_registration_test.go`
- Modify: `tests/sandbox_test.go`
- Modify: `tests/stress_leak_test.go`
- Modify: `tests/value_type_method_test.go`
- Modify: `tests/embedded_parity_test.go`
- Modify: `benchmarks/extreme_stress_test.go`
- Modify: `benchmarks/stress_test.go`
- Modify: `docs/ARCHITECTURE.md`
- Modify: `docs/ARCHITECTURE_CN.md`
- Modify: `docs/PLAN.md`

**Interfaces:**
- Removes: `ErrTimeout`, `(*Program).Close`, `NewSandboxRegistry`, and the four top-level `gig` registry passthroughs.
- Retains: `WithRegistry`, `importer.NewRegistry`, importer global registration helpers, and `host.FromRegistry`.

- [ ] **Step 1: Remove the compatibility API surface**

Delete the unused `ErrTimeout` alias, the no-op `Program.Close`, the unused
`Program.allowPanic` field, `NewSandboxRegistry`, `RegisterPackage`,
`GetPackageByPath`, `GetPackageByName`, and `GetAllPackages` from `gig.go`.
Construct `Program` with only its interpreter program.

- [ ] **Step 2: Update callers to canonical APIs**

Use `importer.RegisterPackage` in registration tests and
`importer.NewRegistry` in sandbox tests. Remove only calls that close a
`*gig.Program`; retain real `Close` calls for files, compressors, Lua states,
and other resources. Update architecture documentation to show the canonical
importer APIs and remove claims about compatibility shims.

- [ ] **Step 3: Remove internal dead scaffolding**

Change frame construction to:

```go
func (p *program) newFrame(fn *ssa.Function) *frame
func (p *program) newFrameWithLayout(fn *ssa.Function, layout *frameLayout) *frame
```

Continue binding closure free variables in `callSSAInto`; update all internal
tests/callers. Delete `deferRecord.pos` and the unused local `recvSlot` type.

- [ ] **Step 4: Make host-environment terminology current**

Describe `host.Environment` and `FromRegistry` as the current explicit host
boundary. Remove only stale "legacy", "stop-gap", and future-phase language;
do not redesign or delete the working registry-backed environment.

- [ ] **Step 5: Verify both modules compile and test**

Run:

```bash
go test ./...
(cd benchmarks && go test ./...)
```

Expected: all packages compile and pass without compatibility shims.

---

### Task 4: Format, scan, benchmark, and review the complete cleanup

**Files:**
- Verify every changed file; modify only to correct findings.

**Interfaces:**
- Consumes: Tasks 1-3.
- Produces: fresh correctness, cleanliness, and performance evidence.

- [ ] **Step 1: Format and inspect the diff**

Run `gofmt` on every changed Go file, `go mod tidy -diff` in the root and
nested modules, and `git diff --check`.

- [ ] **Step 2: Run complete tests and race tests**

Run:

```bash
go test -count=1 ./...
go test -count=1 -race ./...
(cd cmd/gig && go test -count=1 -race ./...)
(cd benchmarks && go test -count=1 ./...)
```

- [ ] **Step 3: Run static redundancy checks**

Run golangci-lint with `unused,unparam,ineffassign,wastedassign,gocritic`, then
run `dupl` once with production files only and once with tests. Expected: no
actionable unused production code, no unreachable duplicate branch, no large
production clones, and no remaining correctness/perf harness clone.

- [ ] **Step 4: Run performance guards**

Run all `TestInterp*AllocationsAreBounded` tests and the repository's existing
3.0x benchmark gate. Compare against the recorded pre-cleanup baseline; any
regression outside normal variance must be investigated before completion.

- [ ] **Step 5: Request a fresh whole-diff review**

Provide the design, this plan, the full working-tree diff relative to
`65bb78e`, and all verification evidence to a reviewer. Fix every Critical or
Important finding and re-run its covering tests.
