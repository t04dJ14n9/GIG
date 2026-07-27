# Adversarial Native-Parity Test Design

## Goal

Find interpreter defects that are not exercised by Gig's current correctness
suite by comparing the result of each test program under native Go execution
and Gig interpretation. Start with the panic, defer, and recover machinery
changed by the current patch, then broaden the same differential method to
unrelated interpreter semantics.

## Oracle and Failure Definition

Native Go execution is the behavioral oracle. Every case must be registered in
the existing correctness harness with both its interpreted source entry point
and its native function value.

A case fails only when the value produced by Gig differs from the value
produced by native Go. Cases that exercise a panic must catch and encode that
panic inside the test program so both executions still return a deterministic,
directly comparable value. This avoids treating two unrelated errors or two
escaped panics as a successful parity result.

The test corpus must compile and run on the repository's Go 1.23.1
compatibility floor and the current toolchain. Tests must not depend on timing,
scheduler ordering, implementation-specific panic text, or unstable reflection
formatting.

## Approach

Use hand-written semantic-boundary cases rather than generated pairwise cases
or fuzzing for this pass. Each function should isolate one Go rule and return a
small integer or string that distinguishes all relevant outcomes. This gives a
failing parity result a clear cause and leaves readable regression coverage.

Generated combinations could cover more syntax, but failures would be harder
to diagnose and minimize. Differential fuzzing is a useful later project, but
it needs separate infrastructure for source generation, compilation, panic
normalization, shrinking, and reproducibility.

## Phase One: Panic, Defer, and Recover

Add focused cases in `tests/testdata/panic_recover/main.go` and register them in
`tests/correctness_test.go`. Before adding a case, check the existing fixture to
confirm that its semantic boundary is not already covered.

The first matrix will cover:

1. `defer recover()` does not recover, while an enclosing deferred closure can
   observe the still-active panic.
2. A nested anonymous function called by a deferred closure cannot recover the
   caller's panic, extending the existing named-helper negative control.
3. A promoted method value can recover through every compiler-generated
   adapter between the deferred call and the declared method.
4. Promoted pointer-receiver method values and pointer/value method expressions
   preserve the same direct-recovery behavior.
5. A nil pointer receiver can execute a directly deferred recovery method when
   the method body does not dereference the receiver, including concrete and
   interface dispatch forms.
6. Two calls to `recover()` in one deferred callable return the panic value once
   and `nil` thereafter.
7. A typed-nil panic value preserves both its dynamic type and nil pointer value
   when recovered.
8. A deferred nil function value panics when invoked and that panic can be
   recovered by an earlier defer.
9. A deferred call through a nil interface produces a recoverable invocation
   panic at the native execution point.
10. A nested defer inside a deferred callable cannot steal an outer frame's
    panic merely because it executes during unwinding.
11. A new panic raised inside a deferred callable can be recovered locally
    without losing an older panic that must continue unwinding.
12. Deferred arguments and method receivers retain the values captured when the
    defer statement executed, even when later defers mutate the original
    variables before recovery.

Syntactic variants will only be retained when they exercise a distinct SSA
call shape, such as an interface invoke, `$bound`, `$thunk`, or promotion
wrapper. Redundant variants that compile to the same relevant call path will be
removed.

## Phase-Two Broadening

After phase one passes and any discovered defects are fixed, add a second
native-parity batch across unrelated interpreter boundaries:

- left-to-right evaluation and multi-assignment ordering;
- slice backing-array aliasing, overlapping `copy`, append reallocation, and
  subslice capacity;
- map mutation during iteration only where Go specifies a deterministic
  result, plus nil-map reads, deletes, and writes caught as return codes;
- pointer, interface, and typed-nil identity across conversions and assertions;
- embedded method sets, pointer/value receiver adaptation, generic functions,
  methods on generic receiver types supported at the compatibility floor, and
  closures that capture method values;
- closure capture across loop iterations and nested returns;
- runtime panic boundaries such as nil function calls, nil dereferences,
  out-of-range slicing, and failed assertions, always normalized to stable
  return values;
- deterministic channel and select cases using buffered channels or explicit
  handshakes rather than scheduler races; and
- host/interpreter boundaries for reflect-backed arguments, results, methods,
  and mutation where the current environment registers the host operation.

Inspect the existing `tricky`, `strange_syntax`, concurrency, and correctness
fixtures before selecting the final broad cases. Retain only cases that cover a
new semantic boundary or a distinct interpreter/SSA path.

## Red-Green Workflow for Discovered Defects

Add and run the smallest coherent batch first. If all cases agree, retain them
as passing coverage and continue to the next batch.

If a case disagrees:

1. run that case alone and record the native and interpreted values;
2. reduce it until one Go rule and one interpreter path explain the mismatch;
3. inspect the emitted SSA and trace the interpreter path to the root cause;
4. keep the reduced parity case failing before changing production code;
5. implement the smallest root-cause fix; and
6. rerun the isolated case, the focused batch, and the full suites.

Do not weaken or rewrite the native oracle to accommodate Gig. Do not add a
production change for a case that already matches native Go.

## Verification and Review

For each phase, run the targeted native-parity cases under Go 1.23.1 and the
current toolchain. After all discovered fixes, run the full root and CLI test
suites with the race detector, pinned lint and Gosec commands, tidy checks,
Windows cross-builds, formatting, and `git diff --check`, matching the existing
release gate.

Obtain a fresh whole-diff code review after both phases. The final merge verdict
must distinguish local readiness from remote readiness: the branch is only
mergeable after the changes are committed, pushed, and the updated GitHub
Actions checks pass.

## Repository Hygiene

Preserve all current user changes. In particular, do not touch or stage the
untracked `.workbuddy/` directory. Do not commit, push, or open/update a pull
request without explicit user authorization.
