# Gig Direct SSA Interpreter Plan

This document tracks the current implementation state and near-term roadmap.
It replaces the original v2 phase plan, which is now historical: the project
has already moved to the direct SSA interpreter architecture.

## Current Architecture

Gig has one execution backend:

```text
Go source -> go/parser -> go/types -> go/ssa -> direct SSA interpreter
```

There is no active bytecode compiler, opcode layer, stack VM, or JIT path. The
interpreter may still use internal frame slots, frame pools, cached layouts, and
typed execution plans as implementation details.

## Current Status

| Area | State | Notes |
| --- | --- | --- |
| Public API | Active | `gig.Build`, `Program.Run`, `RunWithContext`, `WithRegistry`, `WithAllowPanic`, and sandbox registries are implemented. |
| Frontend | Active | Source wrapping, parse, banned import checks, panic policy, auto-import, type checking, G_iface_ban, diagnostics, and SSA construction are implemented in `internal/frontend`. |
| Interpreter runtime | Active | `internal/interp` directly walks SSA functions, basic blocks, and instructions with frame-local state. |
| Value system | Active | `value.Value` is the concrete tagged-union runtime value; mutability lives in interpreter `Cell`s. |
| Host bridge | Active | `host.Environment` bridges registered external packages into type checking and runtime dispatch. |
| DirectCall | Active | Generated package-level wrappers and selected method wrappers bypass `reflect.Value.Call` when possible. |
| Safety model | Active | Scripts can only import registered packages; `unsafe` and `reflect` are rejected by default; `panic` is rejected unless enabled. |
| Context cancellation | Active | `RunWithContext` and interpreter frames honor cancellation checks. |
| Test coverage | Active | Native-parity, regression, concurrency, fuzz, stress, benchmark, host bridge, frontend, interpreter, and value tests exist. |

## Implemented Capabilities

- Scalar arithmetic, comparisons, control flow, loops, recursion, and Phi nodes.
- Pointers, load/store, addressable locals, globals, and package initialization.
- Arrays, slices, maps, structs, composite literals, range, and built-ins.
- Functions, multi-return calls, closures, captured mutation, methods, method values, and method expressions.
- Interfaces, type assertions, type switches, typed nil handling, and interface boxes.
- `defer`, `panic`, `recover`, goroutines, channels, send/receive, and `select`.
- External functions, constants, variables, named types, methods, and third-party package registrations.
- Interpreted closures crossing host boundaries through reflect fallback when needed.
- Default timeout through `Run` and caller-provided cancellation through `RunWithContext`.

## Architectural Boundaries

- Gig intentionally reuses Go's parser, type checker, and SSA builder instead
  of maintaining a separate language or IR.
- Interpreted structs are not silently adapted to host non-empty interfaces.
  The frontend rejects that boundary with G_iface_ban. Supporting it would
  require an explicit proxy design.
- Reflection remains the fallback for host boundaries or composite cases that
  do not have a typed fast path.
- The global registry remains for generated standard-library wrappers and
  importer registration helpers. Isolation-sensitive callers should pass an
  `importer.NewRegistry()` instance through `WithRegistry`.
- A `Program` does not own external resources or require teardown.

## Active Roadmap

1. **Internal dispatch performance**
   - Expand typed operation coverage beyond plain `int`, `bool`, and `[]int`.
   - Precompute more callsite and operand descriptors during frame layout.
   - Reduce generic `readValue` and `map[ssa.Value]` fallback traffic on hot paths.

2. **Host bridge generation**
   - Generate more `DirectMethod` wrappers instead of relying on hand-written overlays.
   - Reduce conversion overhead inside generated DirectCall wrappers.
   - Keep reflect fallback available for signatures that cannot be generated safely.

3. **Semantic regression hygiene**
   - Keep `testdata/known_issues` limited to active unresolved interpreter defects.
   - Keep historical fixes in `resolved_issue` or domain-specific native-parity coverage.
   - Document intentional design limits separately from interpreter defects.

4. **Diagnostics and developer tooling**
   - Keep `cmd/gig dump` focused on SSA/interpreter metadata.
   - Improve build diagnostics where type-check, host import, and safety checks overlap.
   - Keep architecture docs synchronized with the implementation.

5. **Quality gates**
   - Continue running full `go test ./...` in CI.
   - Keep benchmark coverage for internal execution and host-call-heavy workloads.
   - Preserve race, stress, and fuzz coverage for concurrency and cancellation paths.

## Verification Commands

Before claiming completion for interpreter or frontend changes, run:

```bash
go test ./...
git diff --check
```

For performance-sensitive changes, also run the relevant benchmark package:

```bash
cd benchmarks
go test -run '^$' -bench '^Benchmark(Gig|Yaegi)_' -benchmem
```

## Historical Note

The original clean-SSA phase plan is complete and superseded. It described a
future cutover from skeleton packages to a direct SSA interpreter; this branch
now already uses that architecture. Future planning should be written as
current-state roadmap items, not as unfinished historical phase entries.
