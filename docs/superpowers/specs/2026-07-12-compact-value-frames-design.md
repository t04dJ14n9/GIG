# Compact Value Frames Design

## Goal

Complete the next readability and performance pass for Gig's SSA interpreter.
The interpreter will keep one compact runtime value array per invocation, use
the cached function layout as the only `ssa.Value` index, and remove the
remaining `Cell` wrapper and per-frame pointer map. The same pass will remove
the common one-argument and one-result heap allocations from direct interpreted
calls without adding a pool, a second dispatcher, or type-specific runtime
state.

All public APIs and interpreted Go behavior remain unchanged.

This specification supersedes the per-invocation `Cell` representation in
`2026-07-11-readable-interpreter-design.md`. It preserves that design's core
invariant—one canonical runtime value for each SSA value—but stores the value
directly instead of wrapping it in copied metadata.

## Evidence

The current frame has two lookup structures for the same values:

- `frameLayout.index map[ssa.Value]int`, built once per function; and
- `frame.cells map[ssa.Value]*Cell`, rebuilt for every invocation.

Each frame also copies immutable `Cell.Name` and `Cell.Type` metadata even
though runtime execution only mutates `Cell.Value`. `Cell.Name` is not read,
`Cell.Type` is used only by the dead `reflectFromCellValue` helper, and
`frame.freeVars` is assigned but never read.

A fresh five-sample `BenchmarkGig_Fib25` baseline on Apple M3 Pro with Go
1.26.3 has these medians:

| Metric | Median |
| --- | ---: |
| `ns/op` | 150,189,599 |
| `B/op` | 264,152,537 |
| `allocs/op` | 1,213,943 |

The allocation profile attributes:

- 40.03% of allocated objects and 23.28% of allocated bytes directly to
  `indexCellStore`;
- 60.39% of allocated objects cumulatively to frame construction;
- 19.73% of allocated objects to the one-result slice in `runReturn`; and
- 19.63% of allocated objects to the one-argument slice in `runCall`.

This is allocation and ownership overhead, not an instruction-dispatch
bottleneck. No new `planKind` is justified by the current profile.

## Considered approaches

### Keep `[]Cell`, remove only the per-frame map

Frames could retain `[]Cell` and resolve values through `frameLayout.index`.
This is a small change and removes the map allocations, but each recursive
frame would still copy unused names and types and carry a 64-byte `Cell` for
each 32-byte `value.Value`. It leaves the central storage model more elaborate
than the runtime semantics require.

### Pool frames and call slices

A pool could reuse frames, maps, argument slices, and result slices. It might
reduce allocations, but it introduces reset protocols, ownership questions,
retention risk, and special eligibility rules. Those are precisely the kinds
of parallel lifecycle state this readability work is intended to remove.

### Compact value frames

The selected design stores only mutable `value.Value` entries per invocation.
Immutable identity and type information stays on the SSA graph and cached
layout. Globals and closure bindings also store values directly. Direct
interpreted calls use caller-owned one-element scratch storage for their common
argument and result shape.

This approach removes the most code and the most measured allocation sources
while keeping one execution model.

## Design

### Immutable function layout

`frameLayout` remains shared and immutable. It owns:

```go
type frameLayout struct {
    values []ssa.Value
    index  map[ssa.Value]int
    blocks []blockPlan
}
```

`values` provides deterministic indexes and diagnostics. `index` is the only
runtime mapping from SSA identity to an array offset. `cellTemplate` is removed
because immutable metadata is no longer copied into each invocation.

The layout builder must include every parameter, free variable, local, and SSA
instruction that implements `ssa.Value`. A characterization test will assert
that every value which can be read or written has an index.

### Compact invocation state

`frame` owns one mutable value array and a pointer to its layout:

```go
type frame struct {
    fn            *ssa.Function
    layout        *frameLayout
    values        []value.Value
    resultScratch []value.Value
    // control-flow, iterator, defer, panic, and cancellation state
}
```

Small functions retain the inline-eight optimization, but the inline array is
`[8]value.Value`, not `[8]Cell`. Larger functions allocate one value slice.
`newCellStore`, `indexCellStore`, the per-frame `cells` map, and metadata-copy
logic are deleted.

The frame exposes two operations:

```go
func (fr *frame) value(v ssa.Value) (value.Value, bool)
func (fr *frame) setValue(v ssa.Value, val value.Value)
```

Reads use `layout.index` and return a descriptive interpreter error when an
index is absent. Writes use the same index. A missing write index is an
internal layout invariant violation and fails loudly with the function and SSA
value in the message; it never creates hidden fallback storage.

Optimized planned operations continue to access `fr.values[index]` directly.
Generic operations use `value` and `setValue`, so both paths observe the same
canonical array.

### Remove `Cell` from runtime storage

`Cell` is exported only from Go's import-restricted `internal/interp` package
and has no consumers elsewhere in the repository. It is not part of Gig's
public API and is removed rather than retained as a one-field wrapper.

Package globals become:

```go
map[*ssa.Global]value.Value
```

Global reads and writes load or replace the map value. Composite and pointer
values continue to carry addressability through their existing
`reflect.Value` representation.

Closure bindings become `[]value.Value`. `runMakeClosure` snapshots each
binding value exactly as it does today. Scalar captures remain snapshots;
captured address values still point to shared addressable storage. This
preserves loop-allocation freshness and mutation semantics without allocating
one `Cell` per binding.

The unused `frame.freeVars` field and dead `reflectFromCellValue` helper are
deleted. Comments in `value` and `interp` are updated to describe mutability as
belonging to frame/global storage rather than `Cell`.

### Readable call classification

`runCall` currently repeats argument reading, invocation, result packing, and
storage across interface, host, direct interpreted, and indirect calls. It is
split into helpers by call kind while retaining one selection point:

- interface invocation;
- builtin invocation;
- direct SSA or host function;
- interpreted function value; and
- reflect function fallback.

Shared helpers read arguments and store packed results. Invocation selection,
DirectCall handling, method fallback, and `packResults` semantics do not
change.

This decomposition is both a readability change and an escape-analysis
boundary: the direct interpreted helper never passes its scratch storage to a
host interface or reflect call.

### One-argument scratch storage

The direct interpreted helper supplies one caller-owned element to the common
argument reader:

```go
var oneArg [1]value.Value
args, err := p.readValuesInto(fr, common.Args, oneArg[:0])
```

Zero arguments use `nil`. One argument uses the local array. Multiple
arguments allocate an exactly sized slice. Other call kinds keep their current
allocation behavior unless the compiler can independently prove their scratch
storage does not escape.

### One-result caller storage

`callSSA` remains the ordinary slice-returning entry point. An internal
`callSSAInto` variant accepts optional result scratch storage. The callee frame
owns that slice header for the duration of the call, and `runReturn` fills it:

```go
func (p *program) callSSAInto(..., resultScratch []value.Value) ([]value.Value, error)
```

The direct interpreted helper passes a local one-element result array when the
callee signature has exactly one result. Zero results require no storage;
multiple results retain the existing allocation path. Top-level calls, host
calls, reflection, and the public API continue returning ordinary slices.

The result scratch lives only until the direct helper packs or stores the
returned value. It is never retained by the frame, closure, program, or host
environment after the helper returns.

The panic/recover zero-result path uses the same frame result preparation
helper so recovered calls preserve result arity without reintroducing a
separate return model.

### Error and fallback behavior

- Unknown reads remain ordinary interpreter errors.
- Missing write indexes are internal invariant failures with precise context.
- Multi-argument and multi-result calls fall back to allocated slices.
- Host, interface, interpreted-method, reflect-call, defer, panic, recover,
  cancellation, and maximum-depth errors retain their current behavior.
- Direct host and method handled/declined/error selection is unchanged.
- No public signature in `gig`, `host`, `value`, or the public interpreter
  interfaces changes.

## Tests

Tests must characterize and preserve:

1. layout completeness for parameters, free variables, locals, and every
   result-producing instruction;
2. shared immutable layouts with isolated value arrays across recursive and
   concurrent frames;
3. direct reads and writes through one layout index;
4. fresh repeated `ssa.Alloc` values and loop-closure capture semantics;
5. global scalar, pointer, and composite reads/writes;
6. closure scalar snapshots and shared address captures;
7. direct calls with zero, one, and multiple arguments/results;
8. one-result recursion, including error, panic, recover, and depth-limit
   propagation;
9. interface, host, interpreted-method, function-value, reflect, variadic, and
   multi-return call behavior; and
10. the existing full correctness and race suites.

Focused allocation tests may assert the absence of the per-frame map and
per-binding `Cell` allocations structurally. The benchmark gate, rather than a
fragile unit-level `AllocsPerRun` constant, owns the end-to-end allocation
target.

## Performance gates

On the same Apple M3 Pro and Go 1.26.3 environment, using median values from at
least five sequential samples:

- `BenchmarkGig_Fib25` must be at most 500,000 allocs/op;
- `BenchmarkGig_Fib25` must be at most 180,000,000 B/op;
- its median time must improve by at least 10% from the fresh 150,189,599 ns/op
  baseline, so it must be at most 135,170,639 ns/op; and
- all 17 representative workloads must remain below their original exact
  3.0x ceilings recorded in
  `docs/PERFORMANCE_OPTIMIZATION_2026-06_CN.md`.

If a gate fails, profile the failing workload. A new `planKind` is allowed only
when the new profile identifies a repeated generic SSA operation as the
bottleneck. The implementation must not restore a per-frame map, `Cell`
metadata, pooling, typed dirty state, parallel operation arrays, or a second
execution loop.

## Documentation

Update:

- `docs/ARCHITECTURE.md`;
- `docs/ARCHITECTURE_CN.md`;
- `docs/GIG_RULE_ENGINE_OVERVIEW_CN.md`; and
- `docs/PERFORMANCE_OPTIMIZATION_2026-06_CN.md`.

The architecture documents will describe one shared immutable layout, one
compact value array per invocation, value-based globals and closures, and
caller-provided one-result storage. The performance document will retain the
prior measurements and add this pass's baseline, final medians, allocation
deltas, and profile interpretation.

## Scope boundaries

This pass does not redesign type resolution, panic/recover semantics, host
registration, or the ordered block plan. It does not introduce a frame pool,
an arena, bytecode, a second IR, or unsafe pointer arithmetic. Those areas may
receive only mechanical signature/comment changes required by the compact
storage and caller-result interfaces.

## Readability acceptance criteria

The completed implementation must:

- have one SSA identity map per cached function and none per invocation;
- store exactly one mutable `value.Value` per indexed frame value;
- contain no `Cell` type, `cellStorage`, `cells`, `cellTemplate`,
  `newCellStore`, or `indexCellStore` residue;
- represent globals and closure bindings directly as values;
- keep one call-classification point and one result-packing implementation;
- isolate the one-argument/one-result optimization inside the direct
  interpreted-call helper;
- preserve generic slice fallbacks for larger call shapes; and
- reduce conceptual branches and lifecycle state while meeting every
  correctness and performance gate above.
