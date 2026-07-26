# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**Gig** embeds a Go interpreter in a Go application. Source is parsed, type-checked, and lowered to SSA by `go/ssa`, then executed by a direct SSA tree-walking interpreter. There is **no bytecode, no opcode table, and no VM** — if you find docs describing those, they describe the pre-rewrite design (see `docs/PLAN.md`).

Runtime dependency: `golang.org/x/tools` (SSA IR) only.

## Modules

The repo is **five separate Go modules**. `go build ./...` at the root does not cover the others:

| Module | Replace directive | Note |
|---|---|---|
| `.` (root) | — | library |
| `cmd/gig` | **none** — pins published `gig v1.7.7` | CLI; see gotcha below |
| `benchmarks` | `=> ../` | cross-interpreter benchmarks |
| `examples/simple`, `examples/custom` | `=> ../..` | |

**Gotcha:** `cmd/gig` builds against the *released* library, not your working tree. Breaking changes to the root package's exported API compile clean in CI and only break `cmd/gig` after the next tag. Verify manually with a temporary `replace` before changing root-package exports.

## Commands

```bash
# Build / test (root module)
go build ./...
go test -race ./...
go test -race -run TestName ./tests/          # single test
go test -race -run 'TestCorrectness/subtest' ./tests/

# Other modules must be run from their own directory
cd cmd/gig && go test -race ./...
cd benchmarks && go test -bench=. -benchmem -count=5 -run='^$' ./...

# Benchmarks (Gig vs native Go, paired Benchmark{Gig,Native}_* )
go test -bench='BenchmarkGig' -benchmem -count=6 -run='^$' ./tests/

# Lint (CI pins golangci-lint v2.4.0; runs on root AND cmd/gig)
golangci-lint run --timeout=5m

# Security scan (G115 excluded: narrowing conversions are semantically required)
gosec -exclude=G115 -exclude-generated -exclude-dir=stdlib/packages ./...

# Interactive SSA study lab (loopback only; analysis-only, never executes input)
go run ./cmd/ssa-study        # http://127.0.0.1:8080

# Generate stdlib/package registration code
# MUST be go1.23.1 — gen reflects over the running toolchain's stdlib,
# so a newer toolchain emits wrappers for symbols absent from go1.23
# (e.g. bytes.FieldsSeq, net/http.CrossOriginProtection).
go1.23.1 run ./cmd/gig gen <dir>
go1.23.1 run ./cmd/gig init -package <name>
```

CI matrix builds the root module on Go 1.23–1.26, so avoid post-1.23 stdlib APIs in library code.

## Architecture

```
Go source
  → internal/frontend   parse → go/types → policy validation → go/ssa
  → internal/interp     direct SSA interpreter
        ↕ host          the external-Go boundary (interface, not globals)
```

`gig.go` is the entire public API surface: `Build`, `Program.Run`, `Program.RunWithContext`, `WithRegistry`, `WithAllowPanic`.

### internal/frontend

Owns parse, type-check, banned-import and panic-policy enforcement, implicit-import insertion, and SSA construction. Produces an `frontend.Unit`. Policy lives here, not in the interpreter.

### internal/interp — the execution core

Read these in order: `interp.go` (interfaces) → `frame.go` (activation record + dispatch loop) → `plan.go` (per-block precompiled plan) → `ops.go` (per-instruction visitors). Type resolution (`types.Type` → `reflect.Type`, memoised) lives in `type_resolver.go`.

`plan.go` is deliberately a second way to execute the same SSA and it is load-bearing: deleting it in favour of pure `visitInstr` dispatch was measured at 5.9×–14.8× slower on int loops (see the file header and `docs/superpowers/specs/2026-07-26-interp-readability-pass-design.md`). Planned ops must stay semantically identical to the generic handlers they shortcut.

- **`frameLayout`** — computed once per `*ssa.Function`, cached in `p.layouts` (a `sync.Map`). Holds `values []ssa.Value`, `index map[ssa.Value]int`, and one `blockPlan` per basic block.
- **`frame.values []value.Value`** — the *single* mutable store for a call. There is no separate cell map, slot-kind array, or free-var array; earlier revisions had all three. Frames with ≤8 values are allocated inline (`newFrameWithLayout`) to avoid a second allocation.
- **`blockPlan`** — `phis` resolved at block entry, then `ops`. Each `plannedOp` has a `planKind`: `planGeneric` defers to `visitInstr`, while `planIntBinOp` / `planIf` / `planJump` / `planIntIndexLoad` / `planIntIndexStore` are precompiled fast paths that read operands through `intRef` (a frame index *or* an inlined constant) and never touch the map.
- **`continuation`** — the dispatch signal: `contNext` / `contJump` / `contReturn`. Only `runIf`, `runJump`, and `runReturn` return the full `(continuation, []value.Value, error)` triple; every other handler returns plain `error` and `visitInstr` supplies `contNext`. A handler's signature therefore tells you whether its instruction can affect control flow.
- Context cancellation is polled every `cancelCheckInterval` (1024) instructions.

**Hot-path cost to know:** `planGeneric` ops go through `readValue`, which ends in `fr.value(v)` → `fr.layout.index[v]`, a **map lookup per operand read**. Only the planned kinds above avoid it. Widening the plan to pre-resolve operand indices for all instructions is the main remaining optimization.

### Addressability model

Mutability lives in `frame.values` and global storage — never inside a `value.Value`, which is immutable once constructed. Addressable SSA values (`Alloc`, `Locals`) hold a **reflected pointer** in their slot; `runAlloc` and the local pre-binding in `callSSAInto` both use `makeAddressable` + `Addr()`. Loads/stores go through reflect on that pointer.

### host — the external boundary

`host.Environment` is an interface composing `types.Importer` plus `LookupFunc/Var/Const/Type/Method/ReflectType/InterfaceProxy`. The frontend type-checks against it and the interpreter dispatches through it, so there is no global registry reachable from the engine. `importer/` bridges registered packages (`importer.RegisterPackage`, `stdlib/packages/`, 69 generated wrappers) into that interface via `host.FromRegistry`.

Host calls prefer `host.DirectFunction` / `host.DirectMethod` (generated, no reflect) and fall back to `reflect.Call` — see `callResolvedHostFunc` / `callResolvedHostMethod` in `host_call.go`. `tryInvokeMethodOn` returns a `methodDispatchResult` whose `found` flag distinguishes "no such method" from "the call itself failed"; keep that distinction — collapsing it back into an error-means-miss check silently swallows real host errors.

### value

`value.Value` is a tagged union; scalars (bool/int/uint/float/nil) live inline, composites in `obj`. The package is leaf-level — it may import only `go/types` and the stdlib, never `host`, `frontend`, or `interp`.

### Security model

Compile-time: `unsafe` and `reflect` imports are rejected; `panic()` is rejected unless `WithAllowPanic()`. Runtime: context cancellation, and a call-depth cap (`Config.MaxDepth`, default 1024) checked in `callSSAInto`.

The depth cap is enforced on **every** path that re-enters interpreted code — direct calls, interface-method invocation, host-method fallback, and fired defers. `tests/depth_guard_test.go` pins this; a reset-to-zero on any of those paths lets recursion kill the host process with an unrecoverable stack overflow, so keep `depth` threaded when adding a new dispatch path.

## Testing

- `tests/` — the bulk. `correctness_test.go` (feature matrix), `strange_syntax_test.go` (obscure Go), `concurrency_*`, `stress_leak_test.go`, `fuzz_test.go`, `known_issue_test.go`, `sandbox_test.go`. Fixtures in `tests/testdata/` (49 dirs) are embedded as `<name>Src` strings and paired with a `<name>Tests` map. `TestCorrectnessCaseCoverageAudit` (`testcase_coverage_test.go`) parses each fixture source and fails if an exported function has no test case — so adding a function to a fixture obliges you to register a case. Its list of audited sets is maintained by hand; a brand-new fixture set must be added there explicitly or it is silently unaudited.
- `internal/interp/` — unit tests for the execution model: `frame_value_test.go` (layout/slot invariants), `interp_behaviour_test.go` (index-store write-through, phi/branch loops), `host_call_test.go`, `call_storage_test.go`, and `perf_test.go`, whose `TestInterp*AllocationsAreBounded` ceilings are the guardrail that catches structural perf regressions — do not raise them to make a refactor pass.
- Root — `gig_test.go` (public API), `cancellation_test.go`, `race_{enabled,disabled}_test.go` (build-tag split).

Benchmarks in `tests/benchmark_test.go` are paired `BenchmarkGig_X` / `BenchmarkNative_X`, so a run reports interpreter overhead against native Go directly.

## Docs

`docs/` holds the design record. `docs/PLAN.md` is the rewrite plan and explains why the bytecode VM was removed. `docs/ARCHITECTURE.md` (+ `_CN`) is the current walkthrough; `docs/SSA_PIPELINE.md` and `docs/AST_SSA_REFERENCE.md` cover the frontend in depth. `docs/superpowers/{plans,specs}/` are dated per-change design docs — check for one matching the area you are changing before redesigning it.
