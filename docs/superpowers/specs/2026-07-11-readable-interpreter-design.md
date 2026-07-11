# Readable Interpreter Design

## Goal

Simplify Gig's SSA execution and external-object resolution without changing
public behavior. Representative interpreter benchmarks may slow down, but no
individual workload may exceed three times its pre-change median runtime on the
same machine. The redesign should remove overlapping state and execution paths,
not merely move them into different files.

## Current readability problems

### Frame execution

The current frame keeps two logical value stores:

- `slots`, addressed through `slotIndex`, for most SSA values;
- `cells`, a fallback map for values excluded from the slot layout.

`Cell` also carries a second representation for integers and booleans through
`fastInt`, `fastBool`, and `fastDirty`. Generic reads must call
`materializeSlot` to reconcile the typed cache with `Cell.Value`. Understanding
one value therefore requires knowing where it is stored, which representation
is current, and which execution path last wrote it.

`runFrame` has four overlapping optimization structures:

- `fastPhis`;
- `fastBlockOps`;
- `fastInstrs`;
- `fastIndexAddrs`, plus a separate fused-consumer table and `addrRefs` side
  channel.

The structures are individually conservative, but their precedence and
fallback behavior make the dispatcher difficult to follow.

### External objects and calls

The registry bridge first resolves a function through
`LookupExternalFunc`, then reopens the package object map to find its
`DirectCall` wrapper. The interpreter has separate direct and generic entry
points for functions and methods. Each path repeats package-path discovery,
method fallback, argument handling, and result packing.

This splits one semantic action -- call a resolved host symbol -- into two
control flows that must remain synchronized.

## Design

### One logical frame store

Each frame owns one `map[ssa.Value]*Cell`. Stable cells are allocated from a
single backing `[]Cell` when the frame is created, and the map points into that
backing storage. This avoids one allocation per cell while keeping one obvious
lookup model.

Values that must be created dynamically, including repeated `ssa.Alloc`
instructions, still overwrite a stable cell with a fresh runtime value on each
execution. Closure capture continues to snapshot the current `value.Value`, so
loop allocations do not become shared accidentally.

The frame no longer contains `slots`, `slotIndex`, or `slotKinds`. `Cell`
contains only its diagnostic metadata and canonical `value.Value`; the typed
cache and materialization protocol are removed.

### One block plan

A cached function layout describes stable SSA values and one ordered plan per
basic block. Plans store immutable cell indexes into each frame's backing
storage. Hot execution follows those indexes directly, avoiding map lookups
without embedding frame-specific `*Cell` pointers in a cached plan or creating
another value store.

Each block plan contains:

1. an optional generic Phi group;
2. one ordered list of operations.

An operation is either a small optimized operation or the original SSA
instruction. The executor walks exactly one list. A generic operation delegates
to `visitInstr`; optimized operations cover only shapes that have clear,
measured value:

- plain `int` arithmetic and comparisons;
- plain `bool` branches;
- jumps;
- safe adjacent `[]int` `IndexAddr -> load/store` pairs.

There is no separate full-block executor. A block whose operations are all
optimized naturally stays in the same loop without invoking `visitInstr`.

### Phi semantics

Phi handling remains a block-entry operation because SSA requires every Phi in
a block to observe predecessor values before any Phi destination is updated.
The plan selects the predecessor once, reads every source into a temporary
buffer, and then commits every destination.

The Phi implementation is type-agnostic. There is no int-only `fastPhi`, typed
slot cache, or materialization step. Immutable cell indexes and constant
operands retain the useful part of the old optimization while leaving one
semantic implementation.

### Indexed access

The planner recognizes only adjacent, non-escaping `IndexAddr` consumers that
the existing referrer checks prove safe. It emits one planned load or store
operation. The operation has a native `[]int` implementation and can fall back
to the ordinary two-instruction path if the runtime value does not match.

This replaces `fusedIndexAddr`, `fusedIndexAddrConsumers`, `fastIndexAddrs`, and
the per-frame `addrRefs` side channel. Address-taking patterns that are not
proved safe continue through the generic reflect-based handlers.

### External object resolution

The registry bridge resolves an `ExternalObject` once from the registered
package and constructs the appropriate host adapter from that object. Function
value, signature metadata, and optional `DirectCall` therefore come from one
lookup. Variable, constant, and type adapters use the same object-resolution
helper while preserving the existing `PackageRegistry` interface for custom
registries.

Host invocation has one entry point per semantic action:

- `callHostFunc` resolves the cached `host.Function`, tries
  `host.DirectFunction` when available, and otherwise calls `Function.Call`;
- `invokeMethodOn` resolves the receiver once, tries a cached
  `host.DirectMethod`, then the ordinary host method, interpreted method, and
  final reflect fallback in that order.

The separate `callHostFuncDirect`, `invokeMethodOnDirect`, and
`invokeMethodOnDirectResult` flows are removed. `runCall` reads arguments and
packs results once regardless of which host implementation runs.

Direct wrappers remain supported and retain their performance benefit; they
become an internal choice inside the single call pipeline.

## Compatibility and error behavior

The following remain unchanged:

- the public `gig`, `host`, and `importer` APIs;
- Go SSA Phi semantics;
- closure capture and addressability behavior;
- context cancellation and panic/recover behavior;
- generated DirectCall wrappers and reflect fallback;
- host function, variable, constant, type, and method registration;
- error messages where tests or callers rely on them.

The implementation will preserve the current dirty worktree changes in panic,
recover, host-call, generator, tests, CI, and documentation files. Overlapping
files will be edited against their current on-disk state rather than reset.

## Tests

Characterization tests will cover:

- simultaneous multi-Phi assignment;
- constant and cell Phi operands;
- loop allocations captured by closures;
- optimized and fallback integer operations;
- optimized and escaping `IndexAddr` behavior;
- host functions with and without DirectCall;
- host methods through DirectMethod, ordinary host adapters, interpreted
  methods, and reflect fallback;
- missing host symbols and receiver-shape fallback;
- external variable, constant, and type resolution.

The existing root, interpreter, host, importer, generator, native-parity,
concurrency, and race suites remain authoritative for broader compatibility.

## Performance gate

Before implementation, collect repeated `-benchmem` baselines on the current
worktree. After implementation, compare medians on the same machine and Go
toolchain. At minimum, measure:

- arithmetic sum;
- iterative and recursive Fibonacci;
- nested loops;
- slice sum;
- bubble sort;
- sieve;
- closure calls;
- external DirectCall, reflect, method, and mixed calls.

No individual benchmark may exceed `3.0x` its baseline median. Allocation
changes are reported separately. If an operation exceeds the gate, optimize
that operation inside the unified block plan; do not restore a second store,
typed-dirty state, or parallel dispatcher.

## Readability acceptance criteria

The completed design must:

- expose one canonical runtime value per SSA value;
- have one block execution loop and one Phi implementation;
- have one host-function path and one host-method path;
- eliminate lazy typed-value materialization;
- eliminate parallel fast-plan arrays and the address side channel;
- update architecture and performance documentation to describe the new model;
- reduce conceptual branches even if raw line count is not minimized.
