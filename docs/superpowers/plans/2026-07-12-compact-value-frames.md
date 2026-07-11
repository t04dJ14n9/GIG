# Compact Value Frames Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace per-invocation `Cell` metadata and maps with compact value arrays, then remove the common one-argument and one-result allocations from direct interpreted calls.

**Architecture:** Each function keeps one immutable `frameLayout` containing the SSA identity index and ordered block plans. Each invocation owns only `[]value.Value`; globals and closure captures also store values directly. `runCall` delegates to call-kind helpers, and direct interpreted calls provide one-element caller-owned argument and result storage.

**Tech Stack:** Go 1.23.1+, `golang.org/x/tools/go/ssa`, Go tests, race detector, `go test -bench`, `pprof`.

## Global Constraints

- Preserve every public signature in `gig`, `host`, `value`, and exported interpreter interfaces.
- Preserve interpreted Go semantics for globals, pointers, closures, allocation freshness, multi-return calls, methods, interfaces, defer, panic, recover, cancellation, and depth limits.
- Keep one cached `map[ssa.Value]int` per function and no per-invocation SSA identity map.
- Keep one ordered block plan and one result-packing implementation.
- Do not introduce frame pools, arenas, unsafe pointer arithmetic, typed dirty state, parallel operation arrays, or a second dispatcher.
- `BenchmarkGig_Fib25` must finish at or below 500,000 allocs/op, 180,000,000 B/op, and 135,170,639 ns/op median using at least five sequential samples.
- All 17 representative workloads must remain below the original exact 3.0x ceilings listed in Task 5.
- Treat the current dirty worktree as user-owned. In particular, `internal/interp/frame.go`, `internal/interp/ops.go`, `internal/interp/host_call.go`, and `internal/interp/defer_panic.go` contain pre-existing recovery/caller changes. Never stage or rewrite those unrelated hunks.
- Before every task that overlaps a dirty file, record `git diff HEAD -- <paths>` in `.superpowers/sdd/compact-value-progress.md`. After committing, verify the remaining diff still contains the original user hunks and no reversal of the task's committed changes.
- Use `apply_patch` for source edits. If a task change overlaps a user hunk, apply the combined version to the working tree, construct an index-only patch against committed `HEAD`, and commit only the task-owned delta. Do not use reset, checkout, restore, or stash.

---

## File structure

- Create `internal/interp/frame_value_test.go`: focused layout, compact-frame, global, and closure-storage characterizations.
- Create `internal/interp/call_storage_test.go`: focused argument/result scratch and direct-call tests.
- Modify `internal/interp/frame.go`: shared layout pointer, compact values, threaded result storage, and SSA entry plumbing.
- Modify `internal/interp/plan.go`: planned operations read and write `frame.values`.
- Modify `internal/interp/ops.go`: generic value access, readable call helpers, direct-call scratch, global storage, and return storage.
- Modify `internal/interp/composite.go`: rename storage operations and delete the dead Cell conversion helper.
- Modify `internal/interp/goroutine.go`: use compact frame setters.
- Modify `internal/interp/closure.go`: value-based closure bindings.
- Modify `internal/interp/defer_panic.go`: only signature propagation required by value-based closure bindings.
- Modify `internal/interp/engine.go`: value-based package globals.
- Modify `internal/interp/interp.go`: remove `Cell` and update the package model comment.
- Modify `internal/interp/fuse_test.go`: update structural assertions from Cell maps to shared indexes and isolated values.
- Modify `value/value.go`: describe mutability as frame/global storage rather than Cell ownership.
- Modify architecture and performance documents in Tasks 5 and 6.

### Task 1: Replace the per-frame pointer map with compact value storage

**Files:**
- Create: `internal/interp/frame_value_test.go`
- Modify: `internal/interp/frame.go`
- Modify: `internal/interp/plan.go`
- Modify: `internal/interp/ops.go`
- Modify: `internal/interp/composite.go`
- Modify: `internal/interp/goroutine.go`
- Modify: `internal/interp/fuse_test.go`

**Interfaces:**
- Consumes: `collectFrameValues`, `frameLayout.index`, `blockPlan`, and current `Cell.Value` behavior.
- Produces:
  - `frame.layout *frameLayout`
  - `frame.values []value.Value`
  - `func (fr *frame) value(v ssa.Value) (value.Value, bool)`
  - `func (fr *frame) setValue(v ssa.Value, val value.Value)`

- [ ] **Step 1: Record the dirty overlap and write layout/value tests**

Create `internal/interp/frame_value_test.go`:

```go
package interp

import (
	"context"
	"testing"

	"golang.org/x/tools/go/ssa"

	"github.com/t04dJ14n9/gig/internal/frontend"
	"github.com/t04dJ14n9/gig/value"
)

func buildFrameValueFixture(t *testing.T, src, name string) (*program, *ssa.Function) {
	t.Helper()
	ctx := context.Background()
	env := stubEnv{}
	unit, err := frontend.NewBuilder().Build(ctx, frontend.Source{Content: src}, env, frontend.Config{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	executable, err := NewEngine().NewProgram(ctx, unit, env, Config{})
	if err != nil {
		t.Fatalf("NewProgram: %v", err)
	}
	fn := unit.Package().Func(name)
	if fn == nil {
		t.Fatalf("function %q not found", name)
	}
	return executable.(*program), fn
}

func assertLayoutCoversFunction(t *testing.T, layout *frameLayout, fn *ssa.Function) {
	t.Helper()
	check := func(v ssa.Value) {
		t.Helper()
		if _, ok := layout.index[v]; !ok {
			t.Fatalf("layout missing %T %s", v, v.Name())
		}
	}
	for _, v := range fn.Params {
		check(v)
	}
	for _, v := range fn.FreeVars {
		check(v)
	}
	for _, v := range fn.Locals {
		check(v)
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if v, ok := instr.(ssa.Value); ok {
				check(v)
			}
		}
	}
}

func TestFrameLayoutCoversAllFrameValues(t *testing.T) {
	p, outer := buildFrameValueFixture(t, `
func outer(x int) func(int) int {
	y := x + 1
	return func(z int) int { return y + z }
}`, "outer")
	assertLayoutCoversFunction(t, p.frameLayout(outer), outer)
	if len(outer.AnonFuncs) != 1 {
		t.Fatalf("anonymous functions = %d, want 1", len(outer.AnonFuncs))
	}
	inner := outer.AnonFuncs[0]
	assertLayoutCoversFunction(t, p.frameLayout(inner), inner)
}

func TestFramesShareLayoutButOwnValues(t *testing.T) {
	p, fn := buildFrameValueFixture(t, `func Echo(x int) int { return x }`, "Echo")
	layout := p.frameLayout(fn)
	first := p.newFrameWithLayout(fn, nil, layout)
	second := p.newFrameWithLayout(fn, nil, layout)

	if first.layout != second.layout || first.layout != layout {
		t.Fatal("frames do not share their immutable layout")
	}
	idx := layout.index[fn.Params[0]]
	if &first.values[idx] == &second.values[idx] {
		t.Fatal("frames share mutable value storage")
	}
	first.setValue(fn.Params[0], value.MakeInt(41))
	got, ok := first.value(fn.Params[0])
	if !ok || got.Int() != 41 {
		t.Fatalf("first value = %v/%v, want 41", got, ok)
	}
	other, ok := second.value(fn.Params[0])
	if !ok || other.IsValid() {
		t.Fatalf("second value = %v/%v, want indexed invalid zero", other, ok)
	}
}
```

In `internal/interp/fuse_test.go`, rename
`TestFrameUsesOneCanonicalCellPerSSAValue` to
`TestFrameUsesOneCanonicalValuePerSSAValue`. Replace the old
`fr.cells[param]` pointer assertion with an assertion that
`fr.layout.index[param]` points at the entry returned by `fr.value(param)` and
that a second frame has a different backing address.

- [ ] **Step 2: Run the new tests to verify the red state**

Run:

```bash
go test ./internal/interp -run 'Test(FrameLayoutCoversAllFrameValues|FramesShareLayoutButOwnValues)$' -count=1
```

Expected: compile failure because `frame.layout`, `frame.values`, `frame.value`, and `frame.setValue` do not exist.

- [ ] **Step 3: Implement compact frame values**

Replace the storage portion of `frame` and `frameLayout` in `internal/interp/frame.go`:

```go
type frame struct {
	fn        *ssa.Function
	ctx       context.Context
	block     *ssa.BasicBlock
	prevBlock *ssa.BasicBlock

	layout *frameLayout
	values []value.Value

	iters map[ssa.Value]*rangeIter

	defers        []*deferRecord
	panicking     bool
	panicVal      any
	recoverTarget *frame

	cancelTicks int
}

type frameLayout struct {
	values []ssa.Value
	index  map[ssa.Value]int
	blocks []blockPlan
}
```

Build the layout without a Cell template:

```go
values, index := collectFrameValues(fn)
blocks := make([]blockPlan, len(fn.Blocks))
for _, block := range fn.Blocks {
	blocks[block.Index] = compileBlockPlan(block, index)
}
layout := &frameLayout{values: values, index: index, blocks: blocks}
```

Delete `newCellStore` and `indexCellStore`. Add:

```go
func (fr *frame) value(v ssa.Value) (value.Value, bool) {
	idx, ok := fr.layout.index[v]
	if !ok {
		return value.Value{}, false
	}
	return fr.values[idx], true
}

func (fr *frame) setValue(v ssa.Value, val value.Value) {
	idx, ok := fr.layout.index[v]
	if !ok {
		panic(fmt.Sprintf("interp: internal: %s has no frame index for %T %s", fr.fn.Name(), v, v.Name()))
	}
	fr.values[idx] = val
}
```

Construct frames with compact inline storage:

```go
const inlineFrameValueCount = 8
type inlineFrame struct {
	frame
	values [inlineFrameValueCount]value.Value
}

var fr *frame
if len(layout.values) <= inlineFrameValueCount {
	allocation := &inlineFrame{}
	fr = &allocation.frame
	fr.values = allocation.values[:len(layout.values)]
} else {
	fr = &frame{values: make([]value.Value, len(layout.values))}
}
fr.fn = fn
fr.layout = layout
fr.block = fn.Blocks[0]
return fr
```

Remove `frame.blocks` and read block plans from `fr.layout.blocks`.

- [ ] **Step 4: Move every frame read/write to the compact accessors**

Make these exact changes:

- rename `bindCell` to `bindValue` and delegate to `setValue`;
- replace all `fr.setCell(v, val)` calls in `frame.go`, `ops.go`, `composite.go`, and `goroutine.go` with `fr.setValue(v, val)`;
- replace `fr.cell(v)` reads with `fr.value(v)`;
- in `runStore`, read the current address value, mutate a reflected pointer in place when present, otherwise call `setValue(instr.Addr, val)`;
- replace every `fr.cellStorage[index].Value` in `plan.go` with `fr.values[index]`;
- update storage comments in `composite.go` and tests.

The non-global store path must have this shape:

```go
stored, ok := fr.value(instr.Addr)
if !ok {
	return contNext, nil, fmt.Errorf(
		"interp: %s: store to unknown address %T %s",
		fr.fn.Name(), instr.Addr, instr.Addr.Name(),
	)
}
if rv, ok := stored.Reflect(); ok && rv.Kind() == reflect.Ptr && !rv.IsNil() {
	if err := p.assignReflectValue(rv.Elem(), val); err != nil {
		return contNext, nil, err
	}
	return contNext, nil, nil
}
fr.setValue(instr.Addr, val)
return contNext, nil, nil
```

- [ ] **Step 5: Run focused, package, race, and residue checks**

Run:

```bash
gofmt -w internal/interp/frame.go internal/interp/plan.go internal/interp/ops.go internal/interp/composite.go internal/interp/goroutine.go internal/interp/frame_value_test.go internal/interp/fuse_test.go
go test ./internal/interp -run 'Test(FrameLayoutCoversAllFrameValues|FramesShareLayoutButOwnValues)$' -count=1
go test ./internal/interp -count=1
go test -race ./internal/interp -count=1
go vet ./internal/interp
rg -n 'cellStorage|indexCellStore|newCellStore|\.cells\b' internal/interp
git diff --check
```

Expected: all Go commands pass. The residue search returns no production matches.

- [ ] **Step 6: Commit only Task 1 changes**

Stage clean files normally. For `frame.go` and `ops.go`, stage a patch against `HEAD` containing compact-value changes but excluding pre-existing caller/recover hunks. Confirm the staged path set, then commit:

```bash
git commit -m "refactor(interp): use compact frame values"
```

After commit, verify the remaining `frame.go` and `ops.go` diff still contains the original user changes and no compact-value reversal.

### Task 2: Store globals and closure bindings as values and remove Cell

**Files:**
- Modify: `internal/interp/frame_value_test.go`
- Modify: `internal/interp/interp.go`
- Modify: `internal/interp/engine.go`
- Modify: `internal/interp/frame.go`
- Modify: `internal/interp/closure.go`
- Modify: `internal/interp/composite.go`
- Modify: `value/value.go`
- Verify: `internal/interp/defer_panic.go`

**Interfaces:**
- Consumes: Task 1 `frame.values`, `value`, and `setValue`.
- Produces:
  - `program.globals map[*ssa.Global]value.Value`
  - `interpretedFunc.freeVars []value.Value`
  - `callSSA(..., freeVars []value.Value, ...)`
  - no `Cell` type or Cell-based helper.

- [ ] **Step 1: Add red tests for value-backed globals and closures**

Append to `internal/interp/frame_value_test.go`:

```go
func TestProgramGlobalsStoreValuesDirectly(t *testing.T) {
	p, fn := buildFrameValueFixture(t, `
var G int
func Read() int { return G }
`, "Read")
	global, ok := fn.Pkg.Members["G"].(*ssa.Global)
	if !ok {
		t.Fatal("G is not an SSA global")
	}
	stored, ok := p.globals[global]
	if !ok || stored.Int() != 0 {
		t.Fatalf("global = %v/%v, want direct zero Value", stored, ok)
	}
	p.globals[global] = value.MakeInt(9)
	results, err := p.Call(context.Background(), "Read", nil)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(results) != 1 || results[0].Int() != 9 {
		t.Fatalf("Read results = %v, want 9", results)
	}
}

func TestClosureBindingsStoreValuesDirectly(t *testing.T) {
	p, outer := buildFrameValueFixture(t, `
func Outer(x int) func() int { return func() int { return x } }
`, "Outer")
	fr := p.newFrame(outer, nil)
	fr.setValue(outer.Params[0], value.MakeInt(41))
	var closure *ssa.MakeClosure
	for _, block := range outer.Blocks {
		for _, instr := range block.Instrs {
			if candidate, ok := instr.(*ssa.MakeClosure); ok {
				closure = candidate
			}
		}
	}
	if closure == nil {
		t.Fatal("Outer has no MakeClosure")
	}
	if _, _, err := p.runMakeClosure(fr, closure); err != nil {
		t.Fatalf("runMakeClosure: %v", err)
	}
	callableValue, ok := fr.value(closure)
	if !ok {
		t.Fatal("closure value missing")
	}
	raw, ok := callableValue.Func()
	if !ok {
		t.Fatal("closure is not callable")
	}
	interpreted := raw.(*interpretedFunc)
	if len(interpreted.freeVars) != 1 || interpreted.freeVars[0].Int() != 41 {
		t.Fatalf("free vars = %v, want direct Value 41", interpreted.freeVars)
	}
}
```

- [ ] **Step 2: Run tests to verify the red state**

```bash
go test ./internal/interp -run 'Test(ProgramGlobalsStoreValuesDirectly|ClosureBindingsStoreValuesDirectly)$' -count=1
```

Expected: compile failure because globals and closure bindings still contain `*Cell`.

- [ ] **Step 3: Convert globals to direct values**

In `engine.go`, change initialization, storage, and allocation:

```go
globals: map[*ssa.Global]value.Value{},
```

```go
globals map[*ssa.Global]value.Value
```

```go
p.globals[g] = zero
```

In `readValue`, return the mapped value directly. In global `runStore`, assign:

```go
p.globals[addr] = val
```

Do not add a wrapper or pointer map.

- [ ] **Step 4: Convert closure bindings to direct values**

Change these signatures and fields consistently:

```go
func (p *program) callSSA(ctx context.Context, caller *frame, fn *ssa.Function, args []value.Value, freeVars []value.Value, depth int) ([]value.Value, error)
func (p *program) newFrame(fn *ssa.Function, freeVars []value.Value) *frame
func (p *program) newFrameWithLayout(fn *ssa.Function, freeVars []value.Value, layout *frameLayout) *frame

type interpretedFunc struct {
	p        *program
	ctx      context.Context
	fn       *ssa.Function
	freeVars []value.Value
	rv       reflect.Value
}

func (p *program) makeFuncValue(ctx context.Context, fn *ssa.Function, freeVars []value.Value) (value.Value, error)
```

Bind free variables directly:

```go
for i, fv := range fn.FreeVars {
	if i >= len(freeVars) {
		break
	}
	fr.setValue(fv, freeVars[i])
}
```

Capture them directly:

```go
freeVars := make([]value.Value, len(instr.Bindings))
for i, binding := range instr.Bindings {
	captured, err := p.readValue(fr, binding)
	if err != nil {
		return contNext, nil, err
	}
	freeVars[i] = captured
}
```

The `defer_panic.go` call through `ifn.freeVars` must compile without adding a new dispatch path.

- [ ] **Step 5: Delete Cell and dead Cell-only state**

Delete `Cell` from `internal/interp/interp.go`, delete `reflectFromCellValue` from `composite.go`, and remove any remaining `frame.freeVars`. Update comments in `interp.go`, `frame.go`, `closure.go`, `engine.go`, and `value/value.go` to state that mutable values live in frame/global storage and addressability lives inside reflected pointer values.

- [ ] **Step 6: Run behavior, race, and residue checks**

```bash
gofmt -w internal/interp/interp.go internal/interp/engine.go internal/interp/frame.go internal/interp/closure.go internal/interp/composite.go internal/interp/frame_value_test.go value/value.go
go test ./internal/interp -run 'Test(ProgramGlobalsStoreValuesDirectly|ClosureBindingsStoreValuesDirectly|LoopClosure|Alloc)' -count=1
go test ./... -count=1
go test -race ./internal/interp ./tests -count=1
go vet ./internal/interp ./value
rg -n '\bCell\b|reflectFromCellValue|\[\]\*Cell|map\[\*ssa\.Global\]\*Cell' internal/interp value
git diff --check
```

Expected: all Go commands pass. The residue search returns no production definitions or runtime-model comments naming Cell.

- [ ] **Step 7: Commit only Task 2 changes**

Use index-only staging for overlap with user-owned files, then commit:

```bash
git commit -m "refactor(interp): store globals and captures as values"
```

Verify the index is empty and the original user diffs remain.

### Task 3: Split call classification and remove the one-argument allocation

**Files:**
- Create: `internal/interp/call_storage_test.go`
- Modify: `internal/interp/ops.go`

**Interfaces:**
- Consumes: Task 2 value-backed frames and `callSSA`.
- Produces:
  - `readValuesInto(fr *frame, refs []ssa.Value, scratch []value.Value) ([]value.Value, error)`
  - one `runCall` classification point with focused invoke/static/indirect helpers
  - one shared call-result packing/storage helper.

- [ ] **Step 1: Write red tests for caller-owned argument storage**

Create `internal/interp/call_storage_test.go`:

```go
package interp

import (
	"context"
	"testing"

	"golang.org/x/tools/go/ssa"

	"github.com/t04dJ14n9/gig/value"
)

func TestReadValuesIntoUsesSingleScratchSlot(t *testing.T) {
	p, fn := buildFrameValueFixture(t, `func Echo(x int) int { return x }`, "Echo")
	fr := p.newFrame(fn, nil)
	fr.setValue(fn.Params[0], value.MakeInt(17))
	var scratch [1]value.Value
	args, err := p.readValuesInto(fr, []ssa.Value{fn.Params[0]}, scratch[:0])
	if err != nil {
		t.Fatalf("readValuesInto: %v", err)
	}
	if len(args) != 1 || args[0].Int() != 17 {
		t.Fatalf("args = %v, want 17", args)
	}
	if &args[0] != &scratch[0] {
		t.Fatal("single argument did not use caller scratch")
	}
}

func TestReadValuesIntoAllocatesForLargerShape(t *testing.T) {
	p, fn := buildFrameValueFixture(t, `func Add(x, y int) int { return x + y }`, "Add")
	fr := p.newFrame(fn, nil)
	fr.setValue(fn.Params[0], value.MakeInt(2))
	fr.setValue(fn.Params[1], value.MakeInt(3))
	var scratch [1]value.Value
	args, err := p.readValuesInto(fr, []ssa.Value{fn.Params[0], fn.Params[1]}, scratch[:0])
	if err != nil {
		t.Fatalf("readValuesInto: %v", err)
	}
	if len(args) != 2 || args[0].Int() != 2 || args[1].Int() != 3 {
		t.Fatalf("args = %v, want [2 3]", args)
	}
	if &args[0] == &scratch[0] {
		t.Fatal("two arguments incorrectly used one-slot scratch")
	}
}

func TestReadableRunCallPreservesDirectRecursion(t *testing.T) {
	p, _ := buildFrameValueFixture(t, `
func Fib(n int) int {
	if n < 2 { return n }
	return Fib(n-1) + Fib(n-2)
}`, "Fib")
	results, err := p.Call(context.Background(), "Fib", []value.Value{value.MakeInt(10)})
	if err != nil {
		t.Fatalf("Fib: %v", err)
	}
	if len(results) != 1 || results[0].Int() != 55 {
		t.Fatalf("Fib results = %v, want 55", results)
	}
}
```

- [ ] **Step 2: Run tests to verify the red state**

```bash
go test ./internal/interp -run 'Test(ReadValuesInto|ReadableRunCall)' -count=1
```

Expected: compile failure because `readValuesInto` does not exist.

- [ ] **Step 3: Add one argument reader and one result finisher**

Add:

```go
func (p *program) readValuesInto(fr *frame, refs []ssa.Value, scratch []value.Value) ([]value.Value, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	var values []value.Value
	if cap(scratch) >= len(refs) {
		values = scratch[:len(refs)]
	} else {
		values = make([]value.Value, len(refs))
	}
	for i, ref := range refs {
		resolved, err := p.readValue(fr, ref)
		if err != nil {
			return nil, err
		}
		values[i] = resolved
	}
	return values, nil
}

func (p *program) finishCall(fr *frame, instr *ssa.Call, results []value.Value) (continuation, []value.Value, error) {
	stored, err := p.packResults(instr.Type(), results)
	if err != nil {
		return contNext, nil, err
	}
	fr.setValue(instr, stored)
	return contNext, nil, nil
}
```

- [ ] **Step 4: Decompose `runCall` by call kind**

Keep `runCall` as the sole classifier:

```go
func (p *program) runCall(caller *frame, fr *frame, instr *ssa.Call, depth int) (continuation, []value.Value, error) {
	common := instr.Common()
	if common.IsInvoke() {
		return p.runInvokeCall(caller, fr, instr, common, depth)
	}
	if builtin, ok := common.Value.(*ssa.Builtin); ok {
		out, err := p.callBuiltin(caller, fr, builtin, common.Args)
		if err != nil {
			return contNext, nil, err
		}
		fr.setValue(instr, out)
		return contNext, nil, nil
	}
	if fn, ok := common.Value.(*ssa.Function); ok {
		if len(fn.Blocks) == 0 {
			return p.runHostFunctionCall(fr, instr, fn, common.Args)
		}
		return p.runDirectInterpretedCall(fr, instr, fn, common.Args, depth)
	}
	return p.runIndirectCall(fr, instr, common, depth)
}
```

Move each existing branch into the named helper without changing lookup, invocation, conversion, or errors. Host, interface, and reflect helpers call `readValuesInto` with `nil` scratch. Only `runDirectInterpretedCall` declares:

```go
var oneArg [1]value.Value
args, err := p.readValuesInto(fr, refs, oneArg[:0])
```

and calls the existing `callSSA`. Every successful helper delegates to `finishCall`; no helper duplicates `packResults` or `setValue`.

- [ ] **Step 5: Run focused behavior and escape checks**

```bash
gofmt -w internal/interp/ops.go internal/interp/call_storage_test.go
go test ./internal/interp -run 'Test(ReadValuesInto|ReadableRunCall|CallResolved|CallHost|Method)' -count=1
go test ./internal/interp -count=1
go test -race ./internal/interp -count=1
go test -gcflags='-m=2' ./internal/interp 2>&1 | rg 'oneArg|readValuesInto|escapes to heap'
go vet ./internal/interp
git diff --check
```

Expected: tests and vet pass. Record whether `oneArg` stays on the stack; Task 5 is authoritative.

- [ ] **Step 6: Commit only Task 3 changes**

Isolate the task-owned `ops.go` delta from user changes, then commit:

```bash
git commit -m "refactor(interp): clarify call execution"
```

### Task 4: Let direct interpreted calls provide one-result storage

**Files:**
- Modify: `internal/interp/call_storage_test.go`
- Modify: `internal/interp/frame.go`
- Modify: `internal/interp/ops.go`

**Interfaces:**
- Consumes: Task 3 `runDirectInterpretedCall` and `finishCall`.
- Produces:
  - `callSSAInto(..., resultScratch []value.Value)`
  - `prepareValueSlice(scratch []value.Value, n int) []value.Value`
  - result scratch threaded through `runFrame`, `runPlannedOp`, `visitInstr`, and `runReturn`
  - one-result scratch used only by direct interpreted calls.

- [ ] **Step 1: Add red tests for caller-owned result storage**

Append:

```go
func TestCallSSAIntoUsesSingleResultScratch(t *testing.T) {
	p, fn := buildFrameValueFixture(t, `func Inc(x int) int { return x + 1 }`, "Inc")
	var scratch [1]value.Value
	results, err := p.callSSAInto(context.Background(), nil, fn, []value.Value{value.MakeInt(4)}, nil, 0, scratch[:0])
	if err != nil {
		t.Fatalf("callSSAInto: %v", err)
	}
	if len(results) != 1 || results[0].Int() != 5 {
		t.Fatalf("results = %v, want 5", results)
	}
	if &results[0] != &scratch[0] {
		t.Fatal("single result did not use caller scratch")
	}
}

func TestCallSSAIntoAllocatesForMultiResult(t *testing.T) {
	p, fn := buildFrameValueFixture(t, `func Pair(x int) (int, int) { return x, x + 1 }`, "Pair")
	var scratch [1]value.Value
	results, err := p.callSSAInto(context.Background(), nil, fn, []value.Value{value.MakeInt(7)}, nil, 0, scratch[:0])
	if err != nil {
		t.Fatalf("callSSAInto: %v", err)
	}
	if len(results) != 2 || results[0].Int() != 7 || results[1].Int() != 8 {
		t.Fatalf("results = %v, want [7 8]", results)
	}
	if &results[0] == &scratch[0] {
		t.Fatal("multi result incorrectly used one-slot scratch")
	}
}
```

- [ ] **Step 2: Run tests to verify the red state**

```bash
go test ./internal/interp -run 'TestCallSSAInto' -count=1
```

Expected: compile failure because `callSSAInto` does not exist.

- [ ] **Step 3: Add result preparation and thread scratch to returns**

Add:

```go
func prepareValueSlice(scratch []value.Value, n int) []value.Value {
	if n == 0 {
		return nil
	}
	if cap(scratch) < n {
		return make([]value.Value, n)
	}
	return scratch[:n]
}
```

Thread `resultScratch []value.Value` through these internal signatures:

```go
func (p *program) runFrame(caller *frame, fr *frame, depth int, resultScratch []value.Value) ([]value.Value, error)
func (p *program) runPlannedOp(caller *frame, fr *frame, op plannedOp, depth int, resultScratch []value.Value) (continuation, []value.Value, error)
func (p *program) visitInstr(caller *frame, fr *frame, instr ssa.Instruction, depth int, resultScratch []value.Value) (continuation, []value.Value, error)
func (p *program) runReturn(fr *frame, instr *ssa.Return, resultScratch []value.Value) (continuation, []value.Value, error)
```

Every `runPlannedOp` fallback to `visitInstr` passes the same scratch. Only the
`ssa.Return` branch consumes it. Change `runReturn`:

```go
results := prepareValueSlice(resultScratch, len(instr.Results))
for i, result := range instr.Results {
	resolved, err := p.readValue(fr, result)
	if err != nil {
		return contNext, nil, err
	}
	results[i] = resolved
}
fr.block = nil
return contReturn, results, nil
```

- [ ] **Step 4: Add `callSSAInto` without changing the ordinary entry point**

Keep the existing entry point as a wrapper:

```go
func (p *program) callSSA(ctx context.Context, caller *frame, fn *ssa.Function, args []value.Value, freeVars []value.Value, depth int) ([]value.Value, error) {
	return p.callSSAInto(ctx, caller, fn, args, freeVars, depth, nil)
}
```

Move its body to:

```go
func (p *program) callSSAInto(ctx context.Context, caller *frame, fn *ssa.Function, args []value.Value, freeVars []value.Value, depth int, resultScratch []value.Value) (results []value.Value, err error)
```

Pass `resultScratch` to both `runFrame` calls in `callSSAInto`, including the
recovered-return path. Change `zeroResultsFor` to accept scratch and use
`prepareValueSlice`; pass the same threaded scratch when recovery produces zero
results. Do not store scratch on `frame`, and do not change panic propagation,
recover-target threading, or named-result behavior.

- [ ] **Step 5: Use one result slot in direct interpreted calls**

In `runDirectInterpretedCall`:

```go
var oneResult [1]value.Value
var resultScratch []value.Value
if fn.Signature.Results().Len() == 1 {
	resultScratch = oneResult[:0]
}
results, err := p.callSSAInto(fr.ctx, fr, fn, args, nil, depth+1, resultScratch)
```

Delegate to `finishCall` before returning. Do not pass scratch to host, interface, reflect, deferred, goroutine, or public calls.

- [ ] **Step 6: Run result, recursion, recover, and race checks**

```bash
gofmt -w internal/interp/frame.go internal/interp/ops.go internal/interp/call_storage_test.go
go test ./internal/interp -run 'Test(CallSSAInto|ReadableRunCall|Recover|Panic|MaxDepth)' -count=1
go test ./... -count=1
go test -race ./internal/interp ./tests -count=1
go test -gcflags='-m=2' ./internal/interp 2>&1 | rg 'oneResult|resultScratch|escapes to heap'
go vet ./internal/interp
git diff --check
```

Expected: all behavior checks pass. Record escape output; benchmarks decide whether scratch is effective.

- [ ] **Step 7: Commit only Task 4 changes**

Isolate `frame.go` and `ops.go` from user hunks, then commit:

```bash
git commit -m "perf(interp): reuse direct call result storage"
```

### Task 5: Enforce performance and allocation gates

**Files:**
- Modify: `docs/PERFORMANCE_OPTIMIZATION_2026-06_CN.md`
- Modify only if a measured gate fails: `internal/interp/frame.go`, `internal/interp/ops.go`, or `internal/interp/plan.go`

**Interfaces:**
- Consumes: fresh baseline in the design specification and Tasks 1-4.
- Produces: final medians, allocation deltas, exact ceiling checks, and profiles for any failed gate.

- [ ] **Step 1: Measure `Fib25` seven times**

From `benchmarks`:

```bash
go test -run '^$' -bench '^BenchmarkGig_Fib25$' -benchmem -count=7
```

Calculate medians. Expected:

- `ns/op <= 135,170,639`;
- `B/op <= 180,000,000`; and
- `allocs/op <= 500,000`.

- [ ] **Step 2: Profile only if a compact-frame gate fails**

```bash
go test -run '^$' -bench '^BenchmarkGig_Fib25$' -benchtime=5s -cpuprofile=/tmp/gig-compact-fib25.cpu -memprofile=/tmp/gig-compact-fib25.mem
go tool pprof -top -nodecount=40 /tmp/gig-compact-fib25.cpu
go tool pprof -top -alloc_objects -nodecount=40 /tmp/gig-compact-fib25.mem
go tool pprof -top -alloc_space -nodecount=40 /tmp/gig-compact-fib25.mem
```

If scratch still escapes, fix the direct helper boundary without pooling. Add a narrow `planKind` only if the CPU profile identifies a repeated generic SSA instruction. Repeat Step 1 after any fix.

- [ ] **Step 3: Re-run all 17 representative benchmarks five times**

From repository root:

```bash
go test ./tests -run '^$' -bench '^BenchmarkGig_(ArithmeticSum|FibIterative|FibRecursive|SliceSum|NestedLoops|BubbleSort|Sieve|ClosureCalls)$' -benchmem -count=5
```

From `benchmarks`:

```bash
go test -run '^$' -bench '^BenchmarkGig_(ArithSum|Fib25|BubbleSort|Sieve|ClosureCalls|ExtCallDirectCall|ExtCallReflect|ExtCallMethod|ExtCallMixed)$' -benchmem -count=5
```

Check every after median against these exact original ceilings:

| Benchmark | Exact ceiling ns/op |
| --- | ---: |
| `tests/ArithmeticSum` | 115,953 |
| `tests/FibIterative` | 8,574 |
| `tests/FibRecursive` | 2,021,535 |
| `tests/SliceSum` | 277,122 |
| `tests/NestedLoops` | 148,110 |
| `tests/BubbleSort` | 507,894 |
| `tests/Sieve` | 509,406 |
| `tests/ClosureCalls` | 1,626,855 |
| `benchmarks/ArithSum` | 113,880 |
| `benchmarks/Fib25` | 232,673,241 |
| `benchmarks/BubbleSort` | 1,964,100 |
| `benchmarks/Sieve` | 510,000 |
| `benchmarks/ClosureCalls` | 1,617,504 |
| `benchmarks/ExtCallDirectCall` | 2,162,028 |
| `benchmarks/ExtCallReflect` | 1,301,220 |
| `benchmarks/ExtCallMethod` | 1,391,616 |
| `benchmarks/ExtCallMixed` | 1,066,902 |

Expected: 17/17 pass. Report median `ns/op`, `B/op`, and `allocs/op`, the ratio against the prior readable-model result and this pass's fresh `Fib25` baseline, and exact ceiling headroom.

- [ ] **Step 4: Update the performance record**

Append a dated compact-value-frame section to `docs/PERFORMANCE_OPTIMIZATION_2026-06_CN.md` containing:

- fresh pre-pass `Fib25` medians;
- the three profile percentages from the design;
- final seven-sample `Fib25` medians and deltas;
- the final 17-row table;
- whether one-argument and one-result scratch stayed on the stack; and
- the DirectMethod-backed external benchmark caveat.

- [ ] **Step 5: Commit the measured record**

If no production fix was needed, commit only the performance document. If profiling required a focused fix, include its tests:

```bash
git commit -m "perf(interp): verify compact value frames"
```

### Task 6: Update architecture and complete final verification

**Files:**
- Modify: `docs/ARCHITECTURE.md`
- Modify: `docs/ARCHITECTURE_CN.md`
- Modify: `docs/GIG_RULE_ENGINE_OVERVIEW_CN.md`
- Verify: all files changed since `6d17392`

**Interfaces:**
- Consumes: final compact storage, call helpers, and Task 5 measurements.
- Produces: current architecture documentation and release-quality verification evidence.

- [ ] **Step 1: Update architecture descriptions and diagrams**

Document:

- one cached `frameLayout.index` per function;
- one compact `[]value.Value` per invocation;
- inline eight-value storage for small frames;
- value-based globals and closure snapshots;
- one `runCall` classifier with focused call-kind helpers; and
- caller-owned one-element argument/result storage for direct interpreted calls.

State that multi-value and host/reflect fallbacks use ordinary slices and no frame pool or second dispatcher exists.

- [ ] **Step 2: Run format, residue, and static checks**

```bash
gofmt -l internal/interp value host
rg -n '\bCell\b|cellStorage|cellTemplate|indexCellStore|newCellStore|\.cells\b|\[\]\*Cell' internal/interp value docs/ARCHITECTURE.md docs/ARCHITECTURE_CN.md docs/GIG_RULE_ENGINE_OVERVIEW_CN.md
go vet ./...
(cd cmd/gig && go vet ./...)
(cd benchmarks && go vet ./...)
git diff --check
```

Expected: formatting and vet pass; stale-model search has no current-model matches.

- [ ] **Step 3: Run current and supported-toolchain suites**

```bash
go test ./... -count=1
go test -race ./host ./importer ./internal/interp ./tests -count=1
(cd cmd/gig && go test ./... -count=1)
(cd benchmarks && go test ./... -count=1)
(cd examples/custom && go test ./... -count=1)
(cd examples/simple && go test ./... -count=1)
GOTOOLCHAIN=go1.23.1 go test ./... -count=1
(cd cmd/gig && GOTOOLCHAIN=go1.23.1 go test ./... -count=1)
```

Expected: every command exits 0.

- [ ] **Step 4: Audit committed and user-owned changes separately**

```bash
git diff --check 6d17392..HEAD
git diff --cached --stat
git status --short
git diff -- internal/interp/frame.go internal/interp/ops.go internal/interp/host_call.go internal/interp/defer_panic.go
```

Expected: committed range clean, index empty, and remaining working-tree diffs are the user's original caller/recover and unrelated files.

- [ ] **Step 5: Request final review and fix all Critical/Important findings**

Give a fresh reviewer the design, this plan, base `6d17392`, final `HEAD`, benchmark report, and dirty-worktree caveat. Require requirement-by-requirement review of storage semantics, closure/global behavior, scratch lifetime, call fallbacks, performance gates, docs, and public API compatibility.

- [ ] **Step 6: Commit final architecture documentation**

Stage only the three architecture documents and reviewer-approved focused remediation, then commit:

```bash
git commit -m "docs: explain compact value frames"
```

Leave the branch unmerged and unpushed unless the user separately authorizes integration.
