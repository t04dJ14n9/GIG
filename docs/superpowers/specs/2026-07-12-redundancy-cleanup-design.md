# Redundancy Cleanup Design

## Goal

Remove the remaining hand-written duplicate execution paths, dead scaffolding,
and compatibility-only public APIs while preserving interpreter behavior and
the current performance envelope.

## Chosen approach

Use targeted consolidation rather than a generic call-target abstraction. A
single helper will execute an already-resolved static SSA function, choosing
the host bridge for body-less functions and `callSSA` for interpreted bodies.
Builtin execution will similarly have one implementation that accepts resolved
arguments. Normal direct interpreted calls retain their specialized one-argument
and one-result path.

This is preferred over either leaving the duplicate paths in place (which
already permits `go pkg.Func()` to silently skip a host call) or routing every
call through one large tagged dispatcher (which would obscure the hot path and
create unnecessary performance risk).

## Interpreter structure

- `runGo` and `runDeferRec` use one `callStaticFunction` helper for static
  targets.
- `callBuiltin` resolves SSA operands and delegates to `executeBuiltin`.
  Deferred and goroutine builtins call `executeBuiltin` directly; the partial
  `callBuiltinDirect` implementation is deleted.
- `reflectArgs` and `valuesFromReflect` centralize conversion in reflective
  interpreter fallbacks. Host-package reflection remains local to `host`
  because it has separate variadic-call semantics.
- A regression test imports a registered host function, starts it with a Go
  statement, and observes a channel notification. It must time out before the
  fix and pass afterward.

## Test and dead-code cleanup

- Both correctness-suite iteration modes use one `runCorrectnessCase` helper.
- Allocation tests share only build/warm/measure plumbing; sources, expected
  values, run counts, and ceilings remain visible in each test.
- Remove unused diagnostic and embedded-parity helpers, the unused defer
  position, the unused select local type, the unused frame-construction
  parameter, benchmark import suppressions, and the unreachable duplicate
  `Sort` category.

## Breaking API cleanup

Backward compatibility is not required. Delete the no-op `Program.Close`, the
unused `ErrTimeout` alias, the unused `Program.allowPanic` field, and the
top-level `gig` registry passthroughs. Callers use `importer.NewRegistry` or the
canonical `importer` global-registry functions directly. No deprecated aliases
or migration shims are retained.

`host.FromRegistry` remains: it is the concrete adapter that implements the
`host.Environment` contract, not a duplicate public path. Its comments are
updated to describe it as the canonical registry-backed environment rather
than a legacy stop-gap.

## Performance and verification

The normal direct interpreted-call fast path is unchanged. New helpers are
limited to defer/go or reflection-dominated paths, so the expected runtime
change is within noise and must remain comfortably inside the user's 3x
ceiling. Verification includes focused red/green coverage, allocation guards,
root and nested-module tests, race tests, formatting, static analysis, duplicate
detection, and `git diff --check`.

## Repository constraints

Preserve all existing user changes. Do not touch `.workbuddy/`, stage files,
commit, push, or open a pull request. Generated stdlib wrappers and large test
data tables are not cleanup targets.
