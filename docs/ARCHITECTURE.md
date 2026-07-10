# Gig Architecture

Gig embeds Go code in a Go application. You hand it source as a string, it
returns a `*Program`, you call exported functions on the Program. Behind the
single-string API is a four-stage pipeline:

```
source ──► go/parser ──► go/types ──► go/ssa ──► interp.Engine
   │           │             │            │            │
  AST     diagnostics   type info       SSA          values
                                       package
```

The interpreter is a tree-walking SSA evaluator — no custom IR, no bytecode,
no JIT. Every value flows through `reflect.Value` at host boundaries; inside
the interpreter most primitives are unboxed in a 32-byte tagged union.

The only external dependency is `golang.org/x/tools` for SSA construction.
Everything else (parsing, type-checking) is in the Go stdlib.

This document is a tour of the codebase organised by what each package does
and why. Each section names the canonical source files so you can switch to
the code as you read.

---

## 1. Public API — `gig.go`

Four entry points cover almost all usage:

```go
prog, err := gig.Build(source, opts...)         // compile
result, err := prog.Run("Func", args...)        // run with default timeout
result, err := prog.RunWithContext(ctx, ...)    // run with caller's ctx
prog.Close()                                    // no-op, kept for source compat
```

`Build` does parse → type-check → SSA build → interp setup. Options:

- `WithRegistry(r)` — supply a `importer.PackageRegistry` instead of the
  global one. Used by sandboxed/test setups; see `NewSandboxRegistry()`.
- `WithAllowPanic()` — without this, `panic()` is rejected at compile time
  by `frontend/builder.go:checkBannedPanic`. With it, panic/recover/defer
  behave per Go.

`Run` runs with a 10-second default timeout (`gig.DefaultTimeout`); cancel
returns `gig.ErrTimeout` (= `context.DeadlineExceeded`).

The argument-conversion path lives in `Program.run()` — caller's `any` →
`value.Value` via `value.DefaultConverter().FromAny`, results back the
opposite way. Lower-level execution still uses `interp.Program.Call`
internally, but the public API intentionally exposes only the `any`-based
`Run` / `RunWithContext` wrappers.

The package-level helpers (`RegisterPackage`, `GetPackageByPath`,
`GetAllPackages`) are thin wrappers around `importer.GlobalRegistry()` —
the global registry is what stdlib package wrappers populate from their
`init()` functions.

---

## 2. Frontend — `internal/frontend/`

`builder.go` runs the deterministic compile pipeline. The phases, in order:

1. **Auto-wrap** — if the source doesn't start with `package`, prepend
   `package main`. Lets `gig.Build("func F() int { return 1 }")` work.
2. **Parse** — `parser.ParseFile(parser.AllErrors|parser.ParseComments)`.
3. **Banned imports** — reject `unsafe` and `reflect` outright. Configurable
   via `Config.BannedImports`; the default is `DefaultBannedImports`.
4. **Banned panic** — when `cfg.Panic == PanicReject`, walk the AST and
   refuse any `panic(...)` call. `WithAllowPanic()` flips this to
   `PanicAllow`.
5. **Auto-import** — `injectAutoImports` scans selector expressions like
   `fmt.Println` and asks `host.Environment.AutoImport("fmt")` whether the
   identifier maps to a registered package; if so, it splices the import in.
   Means a script can write `fmt.Println(...)` without an explicit `import "fmt"`.
6. **Type-check** — standard `types.Config{Importer: env}.NewChecker(...).Files`.
   Diagnostics are collected via the `Error` callback; missing/typed-incorrectly
   identifiers surface as a `frontend.Errors` aggregating all of them.
7. **G_iface_ban** — see §3.
8. **SSA build** — `ssa.NewProgram(... ssa.SanityCheckFunctions|ssa.BareInits)`,
   then `CreatePackage` for every imported package and the source package, then
   `ssaPkg.Build()`. The result is wrapped in a `frontend.Unit`.

The `Unit` is what the interp engine ingests. It exposes `Package()`,
`FileSet()`, `Diagnostics()` — nothing more.

---

## 3. Interpreted-struct → host-interface boundary — `internal/frontend/host_iface_check.go`

When a host function takes a non-empty interface parameter
(`io.Writer`, `error`, `heap.Interface`, etc.) and you pass an
interpreted-defined struct that "implements" it, two things would have to
happen:

1. The host code would call methods on the struct via reflect.
2. The interpreter would have to receive those reflect calls and dispatch
   them to interpreted method bodies.

Building that proxy is doable but expensive — it requires synthesising a
real Go type at runtime and wiring its methods back through the interpreter.
Gig does not. Instead, the frontend rejects the call at compile time with a
clear, deterministic error:

```
frontend: cannot pass interpreted type *main.errorImpl to host parameter
of type interface{Error() string} (main.go:19:5); interpreted types
cannot satisfy host interfaces (G_iface_ban)
```

The check (`checkHostInterfaceBoundary`) walks every `*ast.CallExpr`, finds
the static `*types.Func` callee, skips calls into the source package, and for
each parameter whose type is a non-empty interface, asks
`isInterpretedConcrete` whether the corresponding argument is a source-defined
struct or pointer-to-struct. If so, error.

What remains allowed:

- `any` parameters — empty interface carries no method requirements; the host
  just stores the value.
- Interpreted structs flowing into *interpreted* interfaces — both sides are
  in the source package, and the interp dispatches normally.
- Host concrete types (e.g. `*bytes.Buffer`) flowing into host interfaces —
  Go's normal rules apply.

---

## 4. Value system — `value/value.go`

`value.Value` is a 32-byte tagged union:

```
kind: Kind     (1 byte)
size: Size     (1 byte, free in padding)
num : int64    (bool / int / uint bits / float64 bits)
obj : any      (string, complex128, reflect.Value, composites)
```

Primitives (`bool`, `int*`, `uint*`, `float*`, `complex*`, `nil`) are unboxed
inline — `num` carries the bits and `obj` stays nil. Strings, complex
numbers, and every composite/reflect-typed value go through `obj`. Mutability
is intentionally absent: a `Value` is immutable once constructed; "mutating"
a variable means installing a new `Value` into the surrounding `Cell` (which
is the interpreter's storage layer — see §5).

The `size` field records the original Go width so `Interface()` can return
`int8(5)` rather than `int(5)` when the value was built from `int8(5)`.

Two non-obvious kinds:

- `KindReflect` — the catch-all when no inline representation fits. Used for
  pointers, slices, maps, structs, channels, named host types. The `obj` is a
  `reflect.Value`.
- `KindInterface` — produced by `MakeInterfaceBox`. The `obj` is a
  `reflect.Value` of `Kind() == reflect.Interface`. This preserves Go's
  typed-nil-in-interface semantics: `var e error = (*MyErr)(nil)` must
  compare *not equal* to nil. A plain `KindNil` would lose the dynamic type;
  the box keeps both type and value.

### Converter

`Converter` is the API surface for moving between `any`, `reflect.Value`, and
`Value`. The default implementation (`defaultConverter`) handles:

- `FromAny(any) (Value, error)` — used by `gig.Build`-callers' arg passing.
- `FromReflect(reflect.Value) (Value, error)` — used everywhere downstream of
  reflect calls.
- `ToAny(Value) (any, error)` — return-value unwrapping.
- `ToReflect(Value, reflect.Type) (reflect.Value, error)` — reverse: builds a
  reflect.Value of the requested type. Special-cases:
  - `KindString → []byte` / `[]rune`: builds with
    `reflect.MakeSlice(typ, len, len)` so cap = len. Plain
    `reflect.Value.Convert([]byte)` rounds cap up to a runtime size class
    (the allocator returns 8 or 32 byte slabs even for length-5 inputs),
    which leaks through to user code via `bytes.Buffer.Cap()`.
- `Convert(Value, types.Type, TypeResolver) (Value, error)` — Go-level type
  conversion, T(x). Routes through `ToReflect` then `FromReflect`.
- `Zero(types.Type, TypeResolver) (Value, error)` — typed-zero of any type.

`isNamedPrimitive(reflect.Type)` flags named types like `time.Duration` whose
underlying is a basic kind. `FromReflect` keeps these as `KindReflect` rather
than unboxing to `KindInt`, so `(time.Duration(3) * time.Second).Seconds()` —
which calls a method on the named type — still resolves.

---

## 5. Interpreter engine — `internal/interp/`

### Program and frame

`engine.go` defines `program`:

```go
type program struct {
    ssaPkg    *ssa.Package
    env       host.Environment
    converter value.Converter
    resolver  *typeResolver
    globals   map[*ssa.Global]*Cell
    maxDepth  int
    layouts   sync.Map
    framePools sync.Map
    panicFrame *frame
}
```

`Program.Call(ctx, name, args)` — the entry point invoked by `gig.Run` —
locates the function, recovers from any propagating panic (turning it into an
error), and hands off to `callSSA`.

`callSSA` (`frame.go`) is the function-call lifecycle:

1. Cap recursion at `maxDepth` (1024).
2. Reject body-less functions (those go through `callHostFunc` instead).
3. Build or reuse the per-call `frame`: SSA function pointer,
   current/previous block, slot array, fallback `cells` map
   (`ssa.Value → *Cell`), free-variable cells for closures.
4. Bind parameters and free variables.
5. Pre-allocate `Cell`s for every `*ssa.Local` so `Store`/`UnOp(MUL)` can
   address them.
6. Install the panic handler (see §8).
7. `runFrame(caller, fr, depth)` — the dispatch loop.

`runFrame` walks blocks. For each block: resolve all Phi nodes from a
**snapshot of the cell map** (so simultaneous Phis don't observe each
other's updates), then iterate the remaining instructions. Each handler
returns a `continuation`:

- `contNext` — advance to the next instruction.
- `contJump` — `fr.block` was changed by the handler (`If`, `Jump`); restart
  the outer loop.
- `contReturn` — function is returning with the supplied result tuple.

To keep the readable SSA interpreter close to Yaegi performance,
`frameLayout` precomputes two runtime plans on first use:

- `slotIndex`: most SSA values map to `[]Cell` slots; `ssa.Alloc` remains in
  the fallback map so closure/address-taking semantics are not broken by slot
  reuse.
- typed fast plans: plain `int`/`bool` Phi, BinOp, If, plus common `[]int`
  load/store patterns run directly from slot/const operands and bypass
  `readValue` map lookup.

Frame pooling is enabled only for functions that do not create more closures
inside their body and do not have complex local addresses. Simple closure
bodies can reuse frames; captured cells still come from the closure object.

### Instruction handlers — `ops.go`

A single `visitInstr` dispatches on the SSA concrete type. The handlers,
roughly grouped:

| Group | Instructions | File |
|---|---|---|
| Control | `Return`, `If`, `Jump` | ops.go |
| Arithmetic | `BinOp`, `UnOp`, `Convert`, `ChangeType`, `ChangeInterface` | ops.go, arith.go |
| Composite read | `Field`, `FieldAddr`, `Index`, `IndexAddr`, `Slice`, `Lookup`, `Extract` | composite.go |
| Composite write | `Alloc`, `Store`, `MapUpdate` | ops.go (Alloc/Store), composite.go |
| Constructors | `MakeSlice`, `MakeMap`, `MakeChan`, `MakeInterface`, `MakeClosure` | composite.go, closure.go |
| Iteration | `Range`, `Next` | composite.go |
| Calls | `Call` (regular + invoke) | ops.go |
| Defer | `Defer`, `RunDefers`, `Panic` | defer_panic.go, type_assert.go |
| Concurrency | `Go`, `Send`, `Select` | goroutine.go |
| Type | `TypeAssert` | type_assert.go |

Phi nodes are resolved at block entry by `runBlockPhis`; the handler in
`visitInstr` is a no-op for any Phi reached defensively.

### Cells, addressability, and the reflect bridge

The storage model in `composite.go` is the single trickiest thing about the
implementation:

- A *scalar local* is just a `Value` in the cell. Reads return the value;
  writes install a new one.
- A *composite local* (struct, array, ...) lives as an addressable
  `reflect.Value` (built by `reflect.New(rt).Elem()` and stored as
  `KindReflect`). `Field` / `IndexAddr` / `Slice` operate on these reflect
  values directly — that's how field-of-struct gets addressability.
- A *pointer* coming from `Alloc` carries the cell's address — `addr.Addr()`
  — so `UnOp(MUL)` and downstream `Store` operations dereference and mutate
  the original cell.

The pivotal helper is `reflectOf(v Value, hint reflect.Type)`:

- If `v` is a `KindInterface` box and the hint matches the interface type
  (or there is no hint), hand back the box. Reads through interface
  variables stay boxed.
- If the hint is a different interface or a concrete type, expose the box's
  dynamic value and let the caller's `reflect.Set` re-box if needed.
- If `v` is `KindReflect`, return the stored `reflect.Value` (converting
  via `Convert` only when the hint demands a different type).
- Otherwise, ask the converter to build a reflect.Value of the hinted type.

`runStore` has a small but load-bearing exception: when the destination is a
`[]interface{}` slice and the source is `[]*ConcreteType` (which happens
after the type-resolver substitutes `any` for a self-referential cycle —
see §6), it rebuilds the slice element-wise so each pointer gets boxed into
the interface slot.

---

## 6. Named-type identity — `engine.go::typeResolver`

`typeResolver` translates `types.Type → reflect.Type`. The cache is keyed by
`types.Type` identity (not `t.String()`); two named types with identical
string representations declared in different functions get separate
reflect.Types.

Three subtleties:

### Recursive types

A struct that references itself (`type Node struct { Next *Node }`) cannot
be expressed via `reflect.StructOf` — Go's runtime forbids self-referential
type construction. The resolver detects recursion via an `inFlight` set,
and returns `interface{}` for the back-edge. Composite ops continue to work
via reflect; the interpreter pays the cost in dynamic typing instead of
stalling on an unconstructible type.

### Host short-circuit

Before building anything, `ResolveType` asks the host environment via
`LookupReflectType`. If the host registry knows this `*types.Named` (or has
a registered same-named type), use the host's reflect.Type directly. This
keeps host-side identity intact — required for things like
`encoding/binary.ByteOrder` method dispatch.

### The named-type tag fix

Two interpreted named types whose underlyings are *structurally identical*
collapse to the same reflect.Type when we just hand back the underlying:

```go
type AdderStruct struct { v int }
type Chainable   struct { v int }
```

Both resolve to `struct { F_v int }`. Method dispatch then can't tell
`(*AdderStruct).Add` from `(*Chainable).Add` — `lookupInterpretedMethod`
matches both and the iteration order picks one at random.

The fix: tag field 0 with a per-named-type marker.

```go
fields[0].Tag = `gig:"<pkg>_<TypeName>"`
return reflect.StructOf(fields), nil
```

`reflect.StructOf` makes types tag-sensitive — `struct{X int "gig:\"A\""}`
and `struct{X int "gig:\"B\""}` are distinct types — but `fmt`'s `%v`/`%+v`,
`encoding/json`, and `encoding/binary` ignore the tag's `gig` key. So
identity is preserved without changing user-visible output.

The escape hatch is `isInterpretedNamedType`: only fire for types declared in
the interpreted source package (`pkg.Path() == r.srcPkgPath`). Host types
like `binary.bigEndian` keep their exact host identity.

---

## 7. Host bridge — `host/registry_bridge.go`, `internal/interp/host_call.go`

### Registration

`importer.GlobalRegistry()` returns a process-wide `Registry`. Generated
stdlib wrappers populate it from their `init()` via:

```go
pkg := importer.RegisterPackage("encoding/binary", "binary")
pkg.AddFunction("Read",  binary.Read,  "")
pkg.AddVariable("BigEndian", &binary.BigEndian, "")
pkg.AddType("ByteOrder", reflect.TypeOf((*binary.ByteOrder)(nil)).Elem(), "")
```

`AddFunction` takes a real `func` value and may also take a
`func([]value.Value) ([]value.Value, error)` DirectCall wrapper for hot paths. Calls
without a wrapper dispatch via `reflect.Call` at runtime. `AddVariable`
takes a pointer so reads load through `UnOp(MUL)` and writes can `Set` the
slot. `AddType` registers a `reflect.Type` for type-checker resolution.
Selected methods can register `AddMethodDirectCall(type, method, wrapper)`;
the wrapper receives the receiver separately from normal args.

`host.FromRegistry(reg)` wraps the importer registry as a
`host.Environment`. Building exposes:

- `Import(path)` — the `types.Importer` callback used during type-check.
- `LookupFunc(pkg, name) (Function, bool)` — used by `callHostFunc`.
- `LookupVar(pkg, name) (Variable, bool)` — global reads/writes.
- `LookupConst`, `LookupType`, `LookupReflectType` — type resolution side.
- `AutoImport(name)` — the auto-import frontend hook.

### Function dispatch

`callHostFunc` (in `host_call.go`) is what runs when SSA emits a `Call` to a
body-less function:

1. Compute the package path from `fn.Pkg.Pkg.Path()`.
2. If `fn.Signature.Recv() != nil`, it's a host method — route to
   `invokeMethodOn(args[0], fn.Name(), args[1:])`.
3. Otherwise look up `LookupFunc(pkg, fn.Name())`.
   If it implements `host.DirectFunction`, call the wrapper and store the
   single returned value directly.
4. If there is no direct wrapper, call `Function.Call`.
5. If lookup fails, fall through to `invokeMethodOn` once — covers a few
   stdlib packages whose methods aren't registered as free functions.

The `host.Function` returned is
`reflectFunc{fn: reflect.ValueOf(rawFn), directCall: wrapper}`. Its
`CallDirect` uses the wrapper when present. Its slower `Call` builds
reflect-typed args via `Converter.ToReflect` and dispatches through
`reflect.Value.Call` (or `CallSlice` for variadic with a pre-packed slice).

Variadic handling has three shapes (see `reflectFunc.Call` in
`registry_bridge.go`):

1. SSA pre-packed a slice whose element type matches the variadic param —
   call `CallSlice` directly.
2. Slice with mismatched elements (e.g. `[]any` flowing into `...io.Writer`)
   — explode and re-pack with each element re-converted.
3. Multiple positional args — pack them ourselves into a fresh slice.

### Method dispatch — the unified path

`invokeMethodOn` in `host_call.go` is shared between three call sites: SSA
`Call` with `Common.IsInvoke()`, `callHostFunc` for receiver-typed
functions, and `defer` records that captured an interface-method receiver
(see §8).

Steps:

1. **Unwrap interface boxes.** If the receiver is `KindInterface`,
   `dynRecv = box.Elem()` so the rest of the path sees the dynamic
   concrete value.
2. **Try registered host DirectMethod wrappers first.** The lookup is cached
   by `(reflect.Type, method)` so repeated host method calls do not rebuild
   package/type keys or bridge objects.
3. **Try interpreted methods** — `lookupInterpretedMethod` scans
   `ssautil.AllFunctions(prog)` for an `*ssa.Function` in the source
   package whose name matches and whose receiver matches via
   `receiverMatches`.
4. If found: `adjustReceiverShape` derefs `*T → T` or addresses `T → *T` to
   match the SSA-declared receiver type. (Go's spec auto-(de)refs; the
   interpreter has to do the same so cell types and reflect.Set targets
   agree.) Then `callSSA` runs the body.
5. If no interpreted match: build a reflect.Value for the dynamic receiver,
   try `MethodByName`, then `Addr().MethodByName`, then `Elem().MethodByName`.
   Pack args via `Converter.ToReflect`, call, unwrap results.

`receiverMatches` accepts the runtime type matching the wanted type, plus
two flexibilities:

- Pointer-value flexibility: `*T` can satisfy a `T` receiver and vice versa.
- Named-primitive equivalence: a `*types.Named` whose underlying matches the
  reflect type is accepted. `MyInt5` (underlying `int`) loses identity at
  the reflect level — both end up as plain `int` — and method-name
  uniqueness inside the interpreted package keeps this from picking the
  wrong receiver in practice.

### `LookupReflectType` chain

For a host `*types.Named`:

1. Identity hit on `LookupExternalType(t)`.
2. Name-keyed lookup: `LookupExternalTypeByName(pkgPath, typeName)`.
3. Scan registered values in that package for one whose `reflect.TypeOf`
   matches the named-type identifier.

Step 3 covers unexported types (`encoding/binary.bigEndian`,
`os.file`, ...) that are never registered explicitly because gentool only
emits `AddType` for exported identifiers. Their `reflect.Type` is reachable
through any registered exported variable of that type.

---

## 8. Defer / panic / recover — `internal/interp/defer_panic.go`, `frame.go`

SSA models `defer f(args)` as a `*ssa.Defer`. `runDefer` snapshots the args
at defer time and pushes a `*deferRecord` onto `frame.defers`. The record
stores (in order of preference):

- `invokeMethod` + `invokeRecv` — when `Common.IsInvoke()` is true (e.g.
  `defer encoder.Close()` where encoder is host-side).
- `fnSSA` — when the target is a static `*ssa.Function`.
- `builtin` — when the target is a `*ssa.Builtin` like `close` or `recover`.
- `fn` — fallback, a function-value to call via reflect.

`runRunDefers` (or the panic-handler in `callSSA`) walks `defers` in LIFO
order via `runDeferRec`. `runDeferRec` dispatches on the record kind:

- `invokeMethod != ""` → `invokeMethodOn(invokeRecv, invokeMethod, args)`.
  This is the single shared method-dispatch path (see §7).
- `fnSSA != nil`, body present → `callSSA`.
- `fnSSA != nil`, body absent → `callHostFunc` (host method).
- `builtin != nil` → `callBuiltinDirect` — handles `close`, `panic`,
  `recover`, `print`, `println`.
- `fn` set → `reflect.Value.Call`.

A panic inside a deferred body is caught by `runDeferRec`'s own `recover` and
recorded into `fr.panicVal`; subsequent deferred `recover()` calls in this
frame consume it.

The full unwind protocol in `callSSA`:

```
defer func() {
    if re := recover(); re != nil {
        fr.panicking, fr.panicVal = true, re
        for each defer (LIFO): runDeferRec(...)
        if still fr.panicking:
            re-panic so a caller frame can engage its own defers
        else if fr.fn.Recover != nil:
            fr.block = fr.fn.Recover
            results = runFrame(...)        // named-return reads land here
        else:
            results = zeroResultsFor(fn)   // typed zeroes
    }
}()
results, err = runFrame(...)
```

Two bits worth flagging:

- **Re-panic across nested calls.** Only the top-level `Program.Call`
  surfaces the panic as an error. Intermediate frames re-panic so a chained
  `recover()` further up the call stack can still consume.
- **Recover + named return.** When `recover()` succeeds, we jump into
  `fn.Recover` (the SSA-emitted recover block) so the named-return cells —
  which any deferred function may have mutated — are read from there into
  `results`. Without this, a recover inside a deferred mutator would not
  surface its update to the caller.

`runPanic` (in `type_assert.go`, despite the name — it lives there because
it shares the AST-traversal helpers) just re-raises by calling Go's `panic`
with the value's `Interface()`, letting the chain take over.

---

## 9. Concurrency — `internal/interp/goroutine.go`

`runGo` spawns a real Go goroutine. Three target shapes:

- `*ssa.Function` — `go callSSA(fn, args, ...)` inside the goroutine, with a
  recover defer to silence runaway panics (matches Go semantics where an
  unrecovered goroutine panic crashes the program; we suppress here because
  surfacing the panic from a background goroutine is rarely useful for
  embedded scripts).
- `*ssa.Builtin` — `go callBuiltinDirect(...)`.
- Function value (closure or value through indirection) — convert the
  receiver to `reflect.Value`, build reflect args, `go rv.Call(rargs)`.

`runSend` is `reflect.Value.Send(rx)`. The channel may arrive wrapped in a
`KindInterface` box; we strip interface wrapping before sending.

`runSelect` builds a `[]reflect.SelectCase` and dispatches via
`reflect.Select`. The result of an SSA `Select` is a tuple `(chosen int, ok
bool, recvN ...)` packed into a synthesised struct so downstream `Extract`
ops can read individual fields.

What we don't implement: Go's "all goroutines are asleep" deadlock
detection. A sequential `Send` on an unbuffered channel from the only
goroutine will block until the context times out. The fuzz tests under
`tests/fuzz_test.go` clamp `buf >= 1` for this reason.

---

## 10. Type assertions — `internal/interp/type_assert.go`

`runTypeAssert` is conservative on purpose. The check is:

```go
if dst.Kind() == reflect.Interface {
    assignable = rv.Type().AssignableTo(dst) || rv.Type().Implements(dst)
} else {
    assignable = rv.Type() == dst || rv.Type().AssignableTo(dst)
}
```

Crucially we **do not** consult `ConvertibleTo`. Go's runtime would never
let `any(42.0).(int)` succeed even though `float64` is convertible to
`int`. Using `ConvertibleTo` would make a type-switch case `case int:`
match a `float64` value — a real bug we hit and removed.

`CommaOk` form returns `(T, bool)` packed into a synthetic tuple for
downstream `Extract`. Plain form panics with
`"interface conversion: %s is not %s"` to mirror Go's runtime message.

---

## 11. Code generation — `cmd/gig/`

The CLI has two relevant subcommands; `cli-guide.md` is the user-facing
walkthrough. From the architecture side:

- `cmd/gig/commands/gen.go` parses `pkgs.go` for blank imports, then for each
  import path calls `gentool.PackageImport(path, outDir, "packages")`.
- `cmd/gig/gentool/generator.go` uses `importer.ForCompiler` to load the
  package's `*types.Package`, walks the exported objects, and emits a single
  `.go` file with `init()` calls registering each:

```go
func init() {
    pkg := importer.RegisterPackage("strings", "strings")
    pkg.AddFunction("ToUpper", strings.ToUpper, "")
    pkg.AddFunction("Split", strings.Split, "")
    pkg.AddType("Builder", reflect.TypeOf((*strings.Builder)(nil)).Elem(), "")
    // ...
}
```

Three properties of the generated code matter:

1. The function pointer is still the real `strings.ToUpper`, so the host
   bridge always has a correct reflect fallback.
2. The registration API supports an optional fourth argument:
   `func([]value.Value) ([]value.Value, error)`. Hot methods may call
   `AddMethodDirectCall(type, method, wrapper)`, where the wrapper receives
   the receiver separately from normal arguments. Generated files now emit
   package-level function wrappers; `stdlib/packages/zz_direct_wrappers.go`
   only keeps a small method overlay.
3. The output is one file per import path under `<dir>/packages/`. The user
   blank-imports `<modPath>/packages` to trigger all `init()` registrations.

`cmd/gig/main.go` exposes the `init`, `gen`, and `dump` subcommands described
in `cli-guide.md`.

---

## 12. Context cancellation

`Program.Run(name, args...)` wraps the call in a 10-second
`context.WithTimeout`. `RunWithContext(ctx, ...)` uses the supplied context
verbatim. The frontend pipeline checks `ctx.Err()` between build phases.
The SSA runtime also polls the context at function entry and then roughly
every 1024 executed SSA/fast instructions, so tight loops return
`context.DeadlineExceeded` / `context.Canceled` without waiting for the
outer goroutine to be killed. A blocking host `reflect.Call` cannot be
preempted from inside Gig; cancellation is observed before or after that
host call returns.

`Program.run` recovers any propagating panic and converts it to an error
prefixed `interpreter panic:`, so the embedder always gets a clean error
return.

---

## 13. What's deliberately out of scope

- Imports of `unsafe`, `reflect`, and `runtime` from interpreted code.
- Passing interpreted struct types into host non-empty interfaces
  (G_iface_ban, §3).
- Goroutine deadlock detection (§9).
- Finalisers, weak references, `runtime.GC`-level introspection.
- Generics monomorphisation. SSA happens to handle most concrete generic
  uses correctly because instantiation is done before SSA construction, but
  this isn't an explicit feature.

---

## 14. Where to look next

By task:

- **Add a stdlib package** → `cli-guide.md`. The wrappers under
  `stdlib/packages/` are the model.
- **Add a third-party package** → `examples/custom/main.go` and
  `examples/custom/mydep/pkgs.go`.
- **Trace a single instruction** → `internal/interp/ops.go` and the file it
  routes to.
- **Understand a host-method failure** → `host_call.go::invokeMethodOn`
  followed by `receiverMatches` followed by `adjustReceiverShape`.
- **Investigate a value-conversion bug** → `value/value.go::ToReflect` and
  `FromReflect`.
- **Find a named-type identity issue** — grep the SSA reflect output for the
  `gig:"<pkg>_<name>"` tag; the resolver in `engine.go` is the only producer.

By file (the spelunker's index):

```
gig.go                                  Public API
internal/frontend/builder.go            Compile pipeline
internal/frontend/host_iface_check.go   G_iface_ban check
value/value.go                          Tagged-union Value, Converter
internal/interp/engine.go               program, typeResolver, named-type tag
internal/interp/frame.go                callSSA, runFrame, panic-recover
internal/interp/ops.go                  Instruction dispatch
internal/interp/arith.go                BinOp, UnOp, scalar conversion
internal/interp/composite.go            Field, Slice, Map, Range, MakeInterface
internal/interp/closure.go              MakeClosure via reflect.MakeFunc
internal/interp/defer_panic.go          Defer records, RunDefers, recover
internal/interp/goroutine.go            Go, Send, Select
internal/interp/type_assert.go          TypeAssert, Panic
internal/interp/host_call.go            Host call & method dispatch
host/registry_bridge.go                 Importer ↔ host.Environment adapter
cmd/gig/gentool/generator.go            Wrapper code generation
cmd/gig/commands/gen.go                 `gig gen` driver
```
