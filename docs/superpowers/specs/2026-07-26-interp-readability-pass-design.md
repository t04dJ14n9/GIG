# Interp Readability Pass

## Goal

Make `internal/interp` easier to read without giving up the performance
the compact-value-frame work won. Budget, carried over from
`2026-07-11-readable-interpreter-design.md`: no benchmark may exceed 3x
its pre-change median on the same machine. Behaviour is unchanged with
one deliberate exception, the call-depth fix below.

## What was measured and rejected

The obvious next simplification — deleting the typed plan layer in
`plan.go` so every instruction dispatches through `visitInstr` — was
implemented and benchmarked before being reverted:

| Benchmark     | before | plan layer deleted | ratio |
|---------------|--------|--------------------|-------|
| BubbleSort    | 201µs  | 2985µs             | 14.8x |
| Sieve         | 199µs  | 1825µs             | 9.2x  |
| SliceSum      | 118µs  | 1072µs             | 9.1x  |
| NestedLoops   | 64µs   | 380µs              | 6.0x  |
| ArithmeticSum | 52µs   | 305µs              | 5.9x  |
| FibRecursive  | 786µs  | 1311µs             | 1.7x  |

BubbleSort also went from 11 to ~7400 allocs/op: the fused
IndexAddr+load/store ops carry allocation savings, not just CPU. Five
of six loop benchmarks land far beyond the 3x ceiling, so the plan
layer stays and now carries a header comment documenting this
measurement. The `TestInterp*AllocationsAreBounded` guardrails in
`perf_test.go` are what caught the allocation regression first.

## What shipped instead

1. **Narrow handler signatures.** Of the 41 instruction handlers, only
   `runIf`, `runJump`, and `runReturn` ever affect control flow, and
   only `runReturn` produces values. The other ~36 returned a
   `(continuation, []value.Value, error)` triple whose first two values
   were always `contNext, nil`. They now return plain `error`;
   `visitInstr` supplies the constants. A handler's signature now
   states whether the instruction can affect control flow.

2. **Depth-cap enforcement on every re-entry path.** Interface method
   invocation, host-method fallback, and fired defers re-entered
   `callSSA` with a hardcoded depth of 0, so recursion through any of
   them bypassed `Config.MaxDepth` and killed the host process with an
   unrecoverable stack overflow. Depth is now threaded through
   `invokeMethodOn` / `tryInvokeMethodOn` / `callHostFunc` and the
   defer machinery. Regression tests: `tests/depth_guard_test.go`.
   This also exposed that the `caller` parameter threaded through
   `runFrame` → `runPlannedOp` → `visitInstr` → `runCall` fed only a
   discarded parameter; that plumbing is removed. recover() semantics
   are untouched — they flow through `callSSAInto`'s own caller
   parameter.

3. **File organisation.** `typeResolver` moved from `engine.go` (72% of
   the file) into `type_resolver.go`; `engine.go` now reads as an
   engine. Dead `program.fset` and the `frame.bindValue` alias are
   gone. `readValue` checks frame slots before the Const/Global/
   Function switch, matching the actual hot path.

4. **Comment truth.** Package and file docs described "Phase 1"/"Phase
   6 vertical slice" stubs for code that shipped long ago, and
   explained the design by analogy to gofun, a codebase the reader
   cannot open. Comments now describe what the code does.

## Non-goals

- The removed v1 API symbols (`ErrTimeout`, `Program.Close`,
  `RegisterPackage`, ...) are a compatibility decision, not a
  readability one, and are tracked separately.
- `value/`, `host/`, `importer/` are untouched.
