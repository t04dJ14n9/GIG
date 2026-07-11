# Readable External Resolution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Resolve each registered external object once and invoke host functions and methods through one control flow that retains DirectCall performance and reflect fallback behavior.

**Architecture:** `registryBridge` obtains an `ExternalObject` from one package/object lookup and builds adapters from that single source. The interpreter's function and method entry points internally choose DirectCall or generic dispatch, so `runCall` reads arguments and packs results once.

**Tech Stack:** Go reflection, `go/types`, Gig `PackageRegistry`, host adapters, SSA call execution, Go tests and benchmarks.

## Global Constraints

- Do not add methods to the public `PackageRegistry` interface or break custom registry implementations.
- Preserve external function, variable, constant, type, method, auto-import, and reflect-type behavior.
- Preserve generated DirectCall wrappers and the existing `host.DirectFunction` and `host.DirectMethod` interfaces.
- Preserve the current dirty-worktree recover-target changes in `internal/interp/host_call.go`, `ops.go`, and `defer_panic.go`.
- Host-call benchmark slowdown must remain at or below `3.0x`; the intended result is approximately neutral.

---

## File structure

- Modify `host/registry_bridge.go`: one object lookup helper and adapter construction from `ExternalObject`.
- Modify `host/registry_bridge_test.go`: object-kind, DirectCall, reflect, variable, constant, and type characterization.
- Modify `internal/interp/host_call.go`: one host-function path and one host-method path.
- Modify `internal/interp/ops.go`: read call arguments and pack results once.
- Modify `internal/interp/defer_panic.go`: use the unified call entry points while preserving recover-target threading.
- Modify `internal/interp/host_call_test.go` or add it if absent: DirectFunction, DirectMethod, reflect, interpreted-method, and missing-symbol coverage.
- Modify architecture and performance documentation with the unified resolution path and measured host-call ratios.

### Task 1: Characterize single-object adapter behavior

**Files:**
- Modify: `host/registry_bridge_test.go`

**Interfaces:**
- Consumes: `importer.NewRegistry`, `ExternalPackage.AddFunction/AddVariable/AddConstant/AddType`, `FromRegistry`.
- Produces: tests proving each host adapter comes from the registered object's value and metadata.

- [ ] **Step 1: Add reflect-function fallback coverage**

```go
func TestRegistryBridgeFunctionFallsBackToReflect(t *testing.T) {
	reg := importer.NewRegistry()
	pkg := reg.RegisterPackage("example/reflect", "reflectpkg")
	pkg.AddFunction("Add", func(a, b int) int { return a + b }, "")
	fn, ok := FromRegistry(reg).LookupFunc("example/reflect", "Add")
	if !ok { t.Fatal("LookupFunc did not find Add") }
	got, err := fn.Call([]value.Value{value.MakeInt(2), value.MakeInt(5)})
	if err != nil { t.Fatalf("Call: %v", err) }
	if len(got) != 1 || got[0].Int() != 7 { t.Fatalf("Add = %v, want 7", got) }
}
```

- [ ] **Step 2: Add wrong-kind and missing-object coverage**

```go
func TestRegistryBridgeLookupFuncRejectsNonFunctionObject(t *testing.T) {
	reg := importer.NewRegistry()
	pkg := reg.RegisterPackage("example/kinds", "kinds")
	x := 1
	pkg.AddVariable("X", &x, "")
	if _, ok := FromRegistry(reg).LookupFunc("example/kinds", "X"); ok {
		t.Fatal("LookupFunc accepted a variable object")
	}
}
```

Also assert that a missing package and missing name return `ok=false` from every applicable lookup.

- [ ] **Step 3: Add variable, constant, and type adapter tests**

Register one mutable integer, one integer constant, one named struct type, and one interface type such as `io.Reader`. Verify `LookupVar.Get/Set`, `LookupConst.Value`, `LookupType.ReflectType`, and `LookupReflectType` preserve values and exact types. The interface case is required because `reflect.Zero(interfaceType).Interface()` is nil, so its reflect type must come from `ExternalPackage.Types`, not `reflect.TypeOf(obj.Value)`.

- [ ] **Step 4: Run the host bridge tests**

```bash
go test ./host -run '^TestRegistryBridge' -count=1
```

Expected: PASS before the refactor; these are behavior characterizations.

- [ ] **Step 5: Commit characterization tests**

```bash
git add host/registry_bridge_test.go
git commit -m "test(host): characterize external object adapters"
```

### Task 2: Resolve one ExternalObject per lookup

**Files:**
- Modify: `host/registry_bridge.go`
- Test: `host/registry_bridge_test.go`

**Interfaces:**
- Consumes: `PackageRegistry.GetPackageByPath/GetPackageByName` and `external.ExternalObject`.
- Produces: `registryBridge.lookupObject(pkgPath, name string, want external.ObjectKind) (*importer.ExternalPackage, *external.ExternalObject, bool)`.

- [ ] **Step 1: Write and run a failing single-resolution test**

Add a test registry that embeds the real implementation and counts only the two relevant lookup routes:

```go
type countingRegistry struct {
	*importer.Registry
	packageLookups int
	legacyFuncLookups int
}

func (r *countingRegistry) GetPackageByPath(path string) *importer.ExternalPackage {
	r.packageLookups++
	return r.Registry.GetPackageByPath(path)
}

func (r *countingRegistry) LookupExternalFunc(pkgPath, name string) (any, bool) {
	r.legacyFuncLookups++
	return r.Registry.LookupExternalFunc(pkgPath, name)
}

func TestRegistryBridgeResolvesFunctionFromOneObjectLookup(t *testing.T) {
	reg := &countingRegistry{Registry: importer.NewRegistry()}
	pkg := reg.RegisterPackage("example/once", "once")
	pkg.AddFunction("Value", func() int { return 7 }, "")
	if _, ok := FromRegistry(reg).LookupFunc("example/once", "Value"); !ok {
		t.Fatal("LookupFunc did not find Value")
	}
	if reg.packageLookups != 1 || reg.legacyFuncLookups != 0 {
		t.Fatalf("package lookups=%d legacy function lookups=%d, want 1/0", reg.packageLookups, reg.legacyFuncLookups)
	}
}
```

Run `go test ./host -run TestRegistryBridgeResolvesFunctionFromOneObjectLookup -count=1`. Expected RED: the current bridge performs both `LookupExternalFunc` and a package lookup.

- [ ] **Step 2: Add the object helper without changing `PackageRegistry`**

```go
func (b *registryBridge) lookupObject(pkgPath, name string, want external.ObjectKind) (*importer.ExternalPackage, *external.ExternalObject, bool) {
	if b == nil || b.reg == nil { return nil, nil, false }
	pkg := b.reg.GetPackageByPath(pkgPath)
	if pkg == nil { pkg = b.reg.GetPackageByName(pkgPath) }
	if pkg == nil { return nil, nil, false }
	obj := pkg.Objects[name]
	if obj == nil || obj.Kind != want { return nil, nil, false }
	return pkg, obj, true
}
```

- [ ] **Step 3: Build function adapters from the one object**

`LookupFunc` validates `reflect.ValueOf(obj.Value).Kind() == reflect.Func` and returns `reflectFunc{name: obj.Name, fn: rv, directCall: obj.DirectCall}`. It no longer calls `LookupExternalFunc` and then reopens `pkg.Objects`.

- [ ] **Step 4: Use the helper for variables, constants, and types**

Construct each adapter from the object's `Name`, `Value`, and `Type`. `LookupType` reads `pkg.Types[name]` from the package returned by the same helper call for the canonical `reflect.Type`; it must not perform a second package lookup.

- [ ] **Step 5: Run host and importer tests**

```bash
go test ./host ./importer -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit single-object resolution**

```bash
git add host/registry_bridge.go host/registry_bridge_test.go
git commit -m "refactor(host): resolve external objects once"
```

### Task 3: Unify host-function invocation

**Files:**
- Modify: `internal/interp/host_call.go`
- Modify: `internal/interp/ops.go`
- Modify: `internal/interp/closure.go`
- Test: `internal/interp/host_call_test.go`

**Interfaces:**
- Consumes: `lookupHostFunc`, `host.DirectFunction`, `host.Function.Call`, `packResults`.
- Produces: one `callHostFunc(ctx context.Context, fn *ssa.Function, args []value.Value) ([]value.Value, error)`.

- [ ] **Step 1: Add a helper that invokes a resolved function once**

```go
func callResolvedHostFunc(fn host.Function, args []value.Value) ([]value.Value, error) {
	if direct, ok := fn.(host.DirectFunction); ok {
		results, handled, err := direct.CallDirect(args)
		if err != nil || handled { return results, err }
	}
	return fn.Call(args)
}
```

- [ ] **Step 2: Fold direct dispatch into `callHostFunc`**

Resolve the package path once, route receiver-bearing SSA functions to `invokeMethodOn`, resolve/cache a free function once, and call `callResolvedHostFunc`. Preserve the legacy receiver fallback only when function lookup misses.

- [ ] **Step 3: Delete `callHostFuncDirect`**

In `runCall`, remove the direct-first branch. Call `callHostFunc` once and call `packResults` once. `interpretedFunc.CallContext` continues to use the same `callHostFunc` for body-less functions.

- [ ] **Step 4: Run direct and reflect function tests**

```bash
go test ./host ./internal/interp ./tests -run 'DirectCall|External|Host|Closure' -count=1
```

Expected: PASS and DirectCall tests prove the reflect body remains bypassed.

- [ ] **Step 5: Commit unified function invocation**

```bash
git add internal/interp/host_call.go internal/interp/ops.go internal/interp/closure.go internal/interp/host_call_test.go
git commit -m "refactor(interp): unify host function calls"
```

### Task 4: Unify host-method invocation

**Files:**
- Modify: `internal/interp/host_call.go`
- Modify: `internal/interp/ops.go`
- Modify: `internal/interp/defer_panic.go`
- Test: `internal/interp/host_call_test.go`

**Interfaces:**
- Consumes: `hostReceiverReflect`, `lookupHostMethod`, `host.DirectMethod`, `lookupInterpretedMethod`, reflect fallback, and the current `caller *frame` recover threading.
- Produces: one `invokeMethodOn(ctx context.Context, caller *frame, receiver value.Value, method string, args []value.Value) ([]value.Value, error)`.

- [ ] **Step 1: Try DirectMethod inside the ordinary host-method branch**

```go
if hm, ok := p.lookupHostMethod(rv, method); ok {
	if direct, ok := hm.(host.DirectMethod); ok {
		result, handled, err := direct.CallDirect(dynRecv, args)
		if err != nil { return nil, err }
		if handled { return []value.Value{result}, nil }
	}
	return hm.Call(dynRecv, args)
}
```

- [ ] **Step 2: Preserve fallback order in the same function**

After host adapters, try the interpreted SSA method with adjusted receiver shape, then reflect `MethodByName`, addressable receiver, and one pointer dereference. Keep existing argument/result error context.

- [ ] **Step 3: Delete direct-method entry points**

Remove `invokeMethodOnDirect` and `invokeMethodOnDirectResult`. In `runCall`, call `invokeMethodOn` once and `packResults` once. Keep `defer_panic.go` passing the current frame as caller.

- [ ] **Step 4: Run method, defer, and recover tests**

```bash
go test ./host ./internal/interp ./tests -run 'Method|Invoke|Defer|Recover|Panic' -count=1
```

Expected: PASS, including the current recover-target changes.

- [ ] **Step 5: Commit unified method invocation**

```bash
git add internal/interp/host_call.go internal/interp/ops.go internal/interp/defer_panic.go internal/interp/host_call_test.go
git commit -m "refactor(interp): unify host method calls"
```

### Task 5: Verify host-call performance and document the path

**Files:**
- Modify: `docs/ARCHITECTURE.md`
- Modify: `docs/ARCHITECTURE_CN.md`
- Modify: `docs/PERFORMANCE_OPTIMIZATION_2026-06_CN.md`

**Interfaces:**
- Consumes: Task 1 baseline medians from the execution plan.
- Produces: measured ratios for `ExtCallDirectCall`, `ExtCallReflect`, `ExtCallMethod`, and `ExtCallMixed`.

- [ ] **Step 1: Run host-call benchmarks five times**

```bash
(cd benchmarks && go test -run '^$' -bench '^BenchmarkGig_(ExtCallDirectCall|ExtCallReflect|ExtCallMethod|ExtCallMixed)$' -benchmem -count=5)
```

Expected: PASS.

- [ ] **Step 2: Calculate after/before median ratios**

Expected: every ratio is `<= 3.0`; report allocation deltas. If a ratio regresses materially, profile the single invocation function before changing architecture.

- [ ] **Step 3: Update architecture and performance docs**

Describe one registry object lookup, one function path, one method path, cache behavior, DirectCall selection, and reflect fallback order.

- [ ] **Step 4: Run full verification**

```bash
gofmt -w host/registry_bridge.go host/registry_bridge_test.go internal/interp/host_call.go internal/interp/ops.go internal/interp/closure.go internal/interp/defer_panic.go internal/interp/host_call_test.go
go test ./...
go test -race ./host ./importer ./internal/interp ./tests
(cd benchmarks && go test ./...)
```

Expected: all commands PASS.

- [ ] **Step 5: Commit documentation**

```bash
git add docs/ARCHITECTURE.md docs/ARCHITECTURE_CN.md docs/PERFORMANCE_OPTIMIZATION_2026-06_CN.md
git commit -m "docs: explain unified external resolution"
```
