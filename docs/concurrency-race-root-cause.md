# Concurrency Race Root-Cause: Nested Goroutine Hang

**Status:** Fixed
**Symptom:** CI (`go test -race`, Go 1.25.x) hung on
`TestConcurrentStatefulFixtures/NestedGoroutineSum` until the 10-minute test
timeout, then failed. Passed locally in isolation.
**Scope:** Three concurrency defects in the SSA interpreter
(`internal/interp`), all rooted in per-goroutine execution state being shared
across goroutine boundaries.

---

## 1. The observed failure

CI produced a `panic: test timed out after 10m0s` with the primary interpreter
goroutine parked on a channel receive:

```
goroutine 2101 [chan receive, 9 minutes]:
  ...interp.(*program).runUnOp(...)      internal/interp/ops.go:305
  ...interp.(*program).visitInstr(...)   internal/interp/ops.go:41
  ...interp.(*program).runFrame(...)     internal/interp/frame.go:543
  ...interp.(*program).callSSA(...)      internal/interp/frame.go:393
  ...interp.(*program).Run(...)          gig.go:99
  ...tests.runCorrectnessCase.func1(...) tests/orphan_fixture_test.go:59
```

The interpreted fixture is ordinary, correct Go:

```go
func NestedGoroutineSum() int {
    ch := make(chan int, 50)
    var wg sync.WaitGroup
    wg.Add(5)
    for i := 0; i < 5; i++ {
        go func() {                 // 5 outer goroutines
            defer wg.Done()
            for j := 0; j < 10; j++ {
                go func() { ch <- 1 }() // 50 inner goroutines total
            }
        }()
    }
    wg.Wait()                       // waits only for the 5 outer goroutines
    sum := 0
    for i := 0; i < 50; i++ {       // blocks until all 50 sends land
        sum += <-ch
    }
    return sum
}
```

The receive loop expects 50 sends. The hang means **fewer than 50 inner
goroutines ever sent** — some were lost. Because the send count was short, the
final `<-ch` blocked forever.

### Why it hid locally

The bug is a scheduling race. In isolation the test almost always passes; the
CI failure needed ~30 preceding subtests plus `-race` (which perturbs
scheduling and widens the window) plus one unlucky interleave. Locally it
reproduced roughly 40% of the time only when driven in a tight loop under
`-race`; single runs looked green.

---

## 2. Reproduction

A tight loop over the fixture under `-race` reproduced the hang reliably:

```go
for i := 0; i < 3000; i++ {
    res, err := prog.Run("NestedGoroutineSum")
    // hang, or res != 50
}
```

Instrumenting the goroutine spawn path (which normally does
`defer func(){ _ = recover() }()` and silently swallows panics) showed the
inner goroutines were dying to a panic:

```
CALLSSA PANIC: reflect: call of MakeFunc with non-Func type
```

That panic is *spurious* — the interpreted code never panics. It is a symptom
of a lower-level race corrupting a `reflect.Type`. The race detector then named
two distinct racing accesses, which turned out to be **two separate bugs**.

---

## 3. Bug 1 — `typeResolver` recursion guard was shared state

### Mechanism

`typeResolver` converts `go/types.Type` → `reflect.Type`, memoised in a
mutex-guarded `cache`. To build recursive types such as
`type Node struct { Next *Node }` — which would otherwise recurse forever, and
which `reflect` cannot express as a truly cyclic struct — it kept an
`inFlight` set marking "this type is currently being built on the stack." When
the recursion re-encounters an in-flight type it returns an `interface{}`
placeholder to break the cycle.

The defect: `inFlight` lived on the **shared** resolver, next to `cache`:

```go
type typeResolver struct {
    mu       sync.RWMutex
    cache    map[types.Type]reflect.Type
    inFlight map[types.Type]bool   // <-- shared across all goroutines
    ...
}
```

`inFlight` is **per-resolution-stack** state, not shared state. When two
interpreted goroutines resolve the same uncached type concurrently — here, the
`func()` signature of the inner `go func(){ ch <- 1 }()` closure — goroutine B
observes the `inFlight[t] = true` that goroutine A set for its own in-progress
build, concludes it is in a recursive cycle, and returns `interface{}`
**for a type that is not actually recursive**.

That bogus `interface{}` flows into `reflect.MakeFunc`, which requires a `Func`
kind, and panics: `reflect: call of MakeFunc with non-Func type`. The panicking
goroutine dies (its send never happens). The `cache`/`inFlight` maps were also
written under the lock while read via a racing path, which the detector flagged
directly.

### Fix

Thread the recursion guard through the call stack as a parameter, so it is
inherently per-goroutine. The shared `cache` stays (concurrent idempotent
builds of the same type are fine — worst case is redundant work, not
corruption).

- `internal/interp/engine.go:199` — `ResolveType` is now a thin wrapper that
  calls `resolveType(t, nil)`.
- `internal/interp/engine.go:213` — `resolveType(t, inFlight map[types.Type]bool)`
  carries the guard; `build` takes and threads the same map through every
  recursive call.
- The `inFlight` field is removed from the struct.

Recursion breaking is unchanged in behaviour; it is simply stack-local now.

---

## 4. Bug 2 — panicking-frame pointer was shared state

Bug 1 was the trigger for *this* fixture, but the race detector and a dedicated
stress test (100 goroutines that each `panic` and `recover` independently)
revealed a second, independent race on the same "shared state that should be
per-goroutine" theme.

### Mechanism

`recover()` must clear the panic state of the frame that is unwinding. In a
directly-deferred function the deferring frame *is* the running frame. But in a
deferred **closure** —

```go
defer func() { if r := recover(); r != nil { ... } }()
```

— the closure runs in its own frame, whose panic flag is not set; the
panicking frame is its caller. The interpreter bridged this with a single field
on the shared program:

```go
type program struct {
    ...
    panicFrame *frame   // <-- shared; set during unwind, read by recover()
}
```

`callSSA`'s recovery path did `p.panicFrame = fr` before running defers, and
`recover()` read `p.panicFrame`. Panic/recover state is inherently
**per-goroutine**, so when multiple interpreted goroutines panicked
concurrently they overwrote each other's `panicFrame`. A `recover()` could then
clear the wrong frame — leaving a real panic un-recovered, so that goroutine
died without completing its send, reproducing the same "short send count →
receiver blocks forever" hang.

### Fix (and the subtlety that made the first attempt wrong)

The panicking frame is already available as the `caller` argument threaded
through the dispatcher (`visitInstr(caller, fr, ...)` → `runCall`). The fix is
to reach it via `caller` instead of shared program state:

- `internal/interp/ops.go:444` — `runCall` now keeps `caller` (was `_`).
- `internal/interp/ops.go:635` — `callBuiltin(caller, fr, ...)` takes it.
- `internal/interp/ops.go:735` — `recover()` resolves the target frame as
  `fr` if `fr.panicking`, else `caller` if `caller.panicking`.
- `internal/interp/frame.go` — the `p.panicFrame` save/set/restore around the
  defer loop is deleted.
- The `panicFrame` field is removed from `program`.

**The subtlety:** a first version that only threaded `caller` still broke
`recover()` — even single-threaded. Cause: `runDeferRec` dispatched a deferred
*closure* value through `reflect.Call`, which re-enters the interpreter via
`interpretedFunc.CallContext` → `callSSA(ctx, nil, ...)` — i.e. **`caller` was
nil**, severing the link to the panicking frame. The old global `panicFrame`
had masked this because it did not depend on call-graph linkage.

The completing fix (`internal/interp/defer_panic.go:136`): when a deferred
function value wraps an interpreted body, dispatch it through `callSSA` with the
deferring frame `fr` as `caller`, instead of going through `reflect.Call`.
External (host) func values still take the reflect path.

```go
if fn, ok := rec.fn.Func(); ok {
    if ifn, ok := fn.(*interpretedFunc); ok && len(ifn.fn.Blocks) > 0 {
        _, err := p.callSSA(fr.ctx, fr, ifn.fn, rec.args, ifn.freeVars, 0)
        return err
    }
}
```

---

## 5. Follow-up — goroutine call ancestry crossed the concurrency boundary

### Mechanism

The frame-threading fix for `recover()` exposed one more boundary error.
`runGo` launched a direct SSA function with the spawning frame as its
`caller`:

```go
_, _ = p.callSSA(ctx, fr, target, args, nil, 0)
```

That caller relationship is valid for an ordinary nested call, but not for a
new goroutine, whose call stack must be independent. While the parent was
unwinding, a child function that called `recover()` could therefore see
`caller.panicking`, clear the parent's panic state, and race with the parent's
unwind.

A deterministic regression coordinates the two goroutines with channels: the
parent signals the child from a deferred function after panic state is set,
and the child calls `recover()`. Before the fix, the child consumed
`"parent panic"` and the parent returned normally.

### Fix

Start direct SSA and builtin goroutines without caller ancestry:

```go
_, _ = p.callSSA(ctx, nil, target, args, nil, 0)
_, _ = p.callBuiltinDirect(nil, builtin, args)
```

The builtin trampoline treats a nil frame as not panicking. Indirect
interpreted function values already re-enter through `CallContext` with a nil
caller, so all goroutine target forms now share the same isolation boundary.

---

## 6. Common root cause

All three bugs are the same class of error: **execution state that is logically
per-goroutine was stored on a process-wide singleton** (`typeResolver.inFlight`
and `program.panicFrame`), or was threaded across a goroutine boundary (the
spawning frame passed as `caller`). A single interpreter instance serves many
interpreted goroutines — every interpreted `go` statement spawns a real host
goroutine sharing one `*program` — so mutable state that models "the current
call" must remain on that goroutine's own call chain.

The fix in both cases is the same shape: move the state off the singleton and
thread it through the call stack (as a function parameter, or via the existing
`caller` frame), which makes it per-goroutine by construction and needs no
additional locking.

### Guidance for future work

- Treat every mutable field on `*program` and `*typeResolver` as suspect.
  Fields that describe "what this call is doing right now" (unwind state,
  in-progress markers, current frame) must not live there.
- Prefer threading transient state through parameters / the frame chain over
  adding a mutex to a shared field — locking a per-goroutine concept on a shared
  object serialises unrelated goroutines and is usually still wrong semantically
  (as `panicFrame` was: one lock would not stop goroutine A's `recover()` from
  reading goroutine B's frame).
- Shared *caches* (idempotent, write-once-per-key) are fine on the singleton;
  shared *cursors* are not.

---

## 7. Verification

- `TestConcurrentStatefulFixtures` (the original CI test): passes under
  `-race`.
- Regression tests added in `tests/concurrency_race_test.go`:
  - `TestConcurrentNestedGoroutines` — 500 iterations of the nested-goroutine
    fixture (guards bug 1).
  - `TestConcurrentPanicRecover` — 300 iterations of 100 concurrently
    panicking/recovering goroutines (guards bug 2).
  - `TestRecoverCannotCrossGoroutineBoundary` — proves a child goroutine cannot
    consume its parent's panic (guards the follow-up ancestry defect).
- Both went from reliable hang / `WARNING: DATA RACE` before the fix to clean
  across many `-race` runs after.
- Single-threaded `recover()` semantics confirmed intact (caught the incomplete
  first attempt at bug 2).
- Full suite green under `go test -race ./...`; `go vet` clean.
