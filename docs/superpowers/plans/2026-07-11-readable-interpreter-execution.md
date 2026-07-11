# Readable Interpreter Execution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace Gig's dual frame store and parallel fast paths with one canonical cell store and one ordered block plan while keeping every representative benchmark within 3.0x of its pre-change median.

**Architecture:** A frame owns a `map[ssa.Value]*Cell` whose pointers reference one backing `[]Cell`. A cached layout assigns each stable SSA value a cell index and compiles each basic block into one Phi group plus one ordered operation list; optimized operations address the same canonical cells by index and generic operations delegate to `visitInstr`.

**Tech Stack:** Go, `golang.org/x/tools/go/ssa`, Go tests and benchmarks, median benchmark comparison.

## Global Constraints

- Public behavior and the `gig`, `host`, and `importer` APIs remain unchanged.
- Every measured workload must remain at or below `3.0x` its pre-change median on the same machine and Go toolchain.
- Preserve the current dirty-worktree panic/recover changes; do not reset or overwrite them.
- Keep one canonical `value.Value` per SSA value and no lazy typed-value materialization.
- Keep one block execution loop, one type-agnostic Phi implementation, and no address side channel.

---

## File structure

- Create `internal/interp/plan.go`: cached cell layout, operand descriptors, ordered block plans, compilation, and optimized operation execution.
- Modify `internal/interp/frame.go`: frame lifecycle, canonical cell store, Phi resolution, and the single execution loop.
- Modify `internal/interp/interp.go`: reduce `Cell` to metadata plus canonical `Value`.
- Modify `internal/interp/composite.go`: retain generic indexed operations and native `[]int`; remove `addrRef` state and expose the safe-pair predicate to the planner.
- Modify `internal/interp/ops.go`: make generic Store/UnOp operate only on real cell values; preserve current panic/recover edits.
- Delete `internal/interp/fast_plan.go`: all retained optimized operations move into the single plan model.
- Modify `internal/interp/fuse_test.go`: replace tests of deleted internals with tests of the unified plan and canonical storage.
- Modify `internal/interp/perf_test.go`: retain allocation guards for the new execution model.
- Modify architecture and performance documentation to describe the new model.

### Task 1: Freeze behavior and performance baselines

**Files:**
- Modify: `internal/interp/fuse_test.go`
- Modify: `internal/interp/interp_test.go`
- Modify: `docs/PERFORMANCE_OPTIMIZATION_2026-06_CN.md`

**Interfaces:**
- Consumes: existing `runProgram`, `expectInt`, `frameLayout`, and benchmark functions.
- Produces: regression tests for simultaneous Phi updates, stable cell identity, unified-plan shapes, and safe indexed pairs.

- [ ] **Step 1: Record the baseline environment and medians**

```bash
go version
go env GOOS GOARCH
sysctl -n machdep.cpu.brand_string
go test ./tests -run '^$' -bench '^BenchmarkGig_(ArithmeticSum|FibRecursive|FibIterative|SliceSum|NestedLoops|BubbleSort|Sieve|ClosureCalls)$' -benchmem -count=5
(cd benchmarks && go test -run '^$' -bench '^BenchmarkGig_(ArithSum|Fib25|BubbleSort|Sieve|ClosureCalls|ExtCallDirectCall|ExtCallReflect|ExtCallMethod|ExtCallMixed)$' -benchmem -count=5)
```

Expected: both benchmark commands pass. Save each median `ns/op`, `B/op`, and `allocs/op`; calculate `3 * median(ns/op)` as its hard ceiling.

- [ ] **Step 2: Add behavior regressions before changing production code**

Add to `internal/interp/interp_test.go`:

```go
func TestInterp_PhisUsePredecessorSnapshot(t *testing.T) {
	const src = `
func SwapLoop(n int) int {
	a, b := 1, 2
	for i := 0; i < n; i++ { a, b = b, a }
	return a*10 + b
}`
	expectInt(t, runProgram(t, src, "SwapLoop", 1), 21)
	expectInt(t, runProgram(t, src, "SwapLoop", 2), 12)
}

func TestInterp_ManyPhisUseOneSnapshot(t *testing.T) {
	const src = `
func Rotate9(n int) int {
	a, b, c, d, e, f, g, h, j := 1, 2, 3, 4, 5, 6, 7, 8, 9
	for k := 0; k < n; k++ {
		a, b, c, d, e, f, g, h, j = b, c, d, e, f, g, h, j, a
	}
	return a*100000000 + b*10000000 + c*1000000 + d*100000 +
		e*10000 + f*1000 + g*100 + h*10 + j
}`
	expectInt(t, runProgram(t, src, "Rotate9", 1), 234567891)
}

func TestInterp_AllocInLoopProducesFreshAddress(t *testing.T) {
	const src = `
func FreshAlloc() int {
	ps := make([]*int, 3)
	for i := 0; i < 3; i++ { x := i + 1; ps[i] = &x }
	*ps[0] = 9
	return *ps[1]*10 + *ps[2]
}`
	expectInt(t, runProgram(t, src, "FreshAlloc"), 23)
}

func TestInterp_UnifiedPlanPreservesMixedOperationOrder(t *testing.T) {
	const src = `
func Mixed(n int) int {
	s := make([]int, 2)
	x := n + 1
	s[0] = x
	wide := int64(s[0])
	s[1] = int(wide) + 2
	return s[0]*10 + s[1]
}`
	expectInt(t, runProgram(t, src, "Mixed", 3), 46)
}

func TestInterp_IndexAddrEscapesToCallee(t *testing.T) {
	const src = `
func bump(p *int) { *p = *p + 1 }
func EscapingIndexAddr() int {
	s := make([]int, 1)
	s[0] = 41
	bump(&s[0])
	return s[0]
}`
	expectInt(t, runProgram(t, src, "EscapingIndexAddr"), 42)
}
```

- [ ] **Step 3: Run the new behavior tests**

```bash
go test ./internal/interp -run 'TestInterp_(PhisUsePredecessorSnapshot|ManyPhisUseOneSnapshot|AllocInLoopProducesFreshAddress|UnifiedPlanPreservesMixedOperationOrder|IndexAddrEscapesToCallee)' -count=1
```

Expected: PASS on the existing implementation. These tests freeze behavior that the structural refactor must preserve.

- [ ] **Step 4: Commit the tests and recorded baseline**

```bash
git add internal/interp/fuse_test.go internal/interp/interp_test.go docs/PERFORMANCE_OPTIMIZATION_2026-06_CN.md
git commit -m "test(interp): define readable execution model"
```

### Task 2: Introduce the canonical cell layout

**Files:**
- Create: `internal/interp/plan.go`
- Modify: `internal/interp/frame.go`
- Modify: `internal/interp/fast_plan.go`
- Modify: `internal/interp/interp.go`
- Test: `internal/interp/fuse_test.go`

**Interfaces:**
- Consumes: `ssa.Value`, `Cell`, `value.Value`, `program.layouts`.
- Produces: `frameLayout.values []ssa.Value`, `frameLayout.index map[ssa.Value]int`, `frame.cellStorage []Cell`, and `frame.cells map[ssa.Value]*Cell`.

- [ ] **Step 1: Write and run the failing canonical-cell test**

Replace `TestTypedFastSlotMaterializesBeforeGenericRead` in `internal/interp/fuse_test.go` with:

```go
func TestFrameUsesOneCanonicalCellPerSSAValue(t *testing.T) {
	const src = `func Identity(x int) int { return x }`
	ctx := context.Background()
	unit, err := frontend.NewBuilder().Build(ctx, frontend.Source{Content: src}, stubEnv{}, frontend.Config{})
	if err != nil { t.Fatalf("Build: %v", err) }
	fn := unit.Package().Func("Identity")
	prog := &program{}
	layout := prog.frameLayout(fn)
	fr := prog.newFrameWithLayout(fn, nil, layout)
	param := fn.Params[0]
	cell, ok := fr.cell(param)
	if !ok { t.Fatal("parameter has no canonical cell") }
	cell.Value = value.MakeInt(42)
	got, err := prog.readValue(fr, param)
	if err != nil { t.Fatalf("readValue: %v", err) }
	if got.Int() != 42 { t.Fatalf("readValue = %d, want 42", got.Int()) }
	if fr.cells[param] != cell { t.Fatal("cell lookup did not preserve identity") }
}
```

Run `go test ./internal/interp -run TestFrameUsesOneCanonicalCellPerSSAValue -count=1`. Expected RED: the current slotted parameter is not present in `fr.cells` as the canonical cell.

- [ ] **Step 2: Define stable-value collection in `plan.go`**

```go
func collectFrameValues(fn *ssa.Function) ([]ssa.Value, map[ssa.Value]int) {
	values := make([]ssa.Value, 0, len(fn.Params)+len(fn.FreeVars)+len(fn.Locals))
	index := make(map[ssa.Value]int)
	add := func(v ssa.Value) {
		if v == nil { return }
		if _, exists := index[v]; exists { return }
		index[v] = len(values)
		values = append(values, v)
	}
	for _, v := range fn.Params { add(v) }
	for _, v := range fn.FreeVars { add(v) }
	for _, v := range fn.Locals { add(v) }
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if v, ok := instr.(ssa.Value); ok { add(v) }
		}
	}
	return values, index
}
```

Keep the existing precompiled block fields temporarily so this checkpoint remains green, but add `values []ssa.Value` to `frameLayout` and remove `slotKinds`. Task 3 replaces the parallel block fields.

- [ ] **Step 3: Replace the frame's dual stores with canonical cells**

Use `cellStorage []Cell` plus `cells map[ssa.Value]*Cell`. Build the map once with pointers into `cellStorage`:

```go
func newCellStore(layout *frameLayout) ([]Cell, map[ssa.Value]*Cell) {
	storage := make([]Cell, len(layout.values))
	cells := make(map[ssa.Value]*Cell, len(layout.values))
	for i, v := range layout.values {
		storage[i].Name = v.Name()
		storage[i].Type = v.Type()
		cells[v] = &storage[i]
	}
	return storage, cells
}

func (fr *frame) cell(v ssa.Value) (*Cell, bool) { cell, ok := fr.cells[v]; return cell, ok }

func (fr *frame) setCell(v ssa.Value, val value.Value) {
	if cell, ok := fr.cells[v]; ok { cell.Value = val; return }
	fr.cells[v] = &Cell{Name: v.Name(), Type: v.Type(), Value: val}
}

func (fr *frame) bindCell(v ssa.Value, val value.Value) { fr.setCell(v, val) }
```

- [ ] **Step 4: Remove the typed cache from `Cell`**

```go
type Cell struct {
	Name  string
	Type  types.Type
	Value value.Value
}
```

Delete `setSlotValue`, `materializeSlot`, and every `fastDirty` synchronization call. Update the still-existing `fast_plan.go` to read and write the canonical values directly:

```go
func (r fastIntRef) read(fr *frame) int64 {
	if r.kind == fastIntConst { return r.constant }
	return fr.cellStorage[r.slot].Value.Int()
}

func (fr *frame) setFastIntSlot(index int, n int64) {
	fr.cellStorage[index].Value = value.MakeInt(n)
}

func (fr *frame) setFastBoolSlot(index int, b bool) {
	fr.cellStorage[index].Value = value.MakeBool(b)
}

func (fr *frame) readFastBoolSlot(index int) bool {
	return fr.cellStorage[index].Value.Bool()
}
```

`runFastIndexAddr` must likewise read `fr.cellStorage[instr.sliceSlot].Value`. These compatibility helpers keep the existing plan operational while ensuring there is only one value representation.

- [ ] **Step 5: Run canonical-store tests**

```bash
go test ./internal/interp -run 'TestFrameUsesOneCanonicalCell|TestInterp_(AddInts|ForLoop|FibRecursive)' -count=1
go test ./internal/interp -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit the canonical store**

```bash
git add internal/interp/plan.go internal/interp/frame.go internal/interp/fast_plan.go internal/interp/interp.go internal/interp/fuse_test.go
git commit -m "refactor(interp): use one canonical cell store"
```

### Task 3: Compile and execute one ordered block plan

**Files:**
- Modify: `internal/interp/plan.go`
- Modify: `internal/interp/frame.go`
- Delete: `internal/interp/fast_plan.go`
- Test: `internal/interp/fuse_test.go`

**Interfaces:**
- Consumes: `frameLayout.index`, `visitInstr`, `continuation`, `ssa.BasicBlock`.
- Produces: `blockPlan{phis []phiPlan, ops []plannedOp}`, `runPlannedOp`, and `runBlockPhis`.

- [ ] **Step 1: Write and run the failing unified-plan test**

Replace `TestFrameLayoutCachesFastIntLoopPlan` in `internal/interp/fuse_test.go` with the following test, then run it and confirm RED because the current layout exposes parallel `fastPhis`, `fastInstrs`, and `fastBlockOps` instead of `blocks`:

```go
func TestFrameLayoutBuildsOneOrderedIntLoopPlan(t *testing.T) {
	const src = `func Sum() int { s := 0; for i := 1; i <= 1000; i++ { s += i }; return s }`
	ctx := context.Background()
	unit, err := frontend.NewBuilder().Build(ctx, frontend.Source{Content: src}, stubEnv{}, frontend.Config{})
	if err != nil { t.Fatalf("Build: %v", err) }
	fn := unit.Package().Func("Sum")
	layout := (&program{}).frameLayout(fn)
	var sawPhi, sawInt, sawIf, sawJump bool
	for _, block := range layout.blocks {
		if len(block.phis) != 0 { sawPhi = true }
		for _, op := range block.ops {
			switch op.kind {
			case planIntBinOp: sawInt = true
			case planIf: sawIf = true
			case planJump: sawJump = true
			}
		}
	}
	if !sawPhi || !sawInt || !sawIf || !sawJump {
		t.Fatalf("plan shapes phi=%v int=%v if=%v jump=%v", sawPhi, sawInt, sawIf, sawJump)
	}
}
```

- [ ] **Step 2: Define unified plan descriptors**

```go
type planKind uint8
const (
	planGeneric planKind = iota
	planIntBinOp
	planIf
	planJump
	planIntIndexLoad
	planIntIndexStore
)

type valueRef struct { cell int; value ssa.Value }
type intRef struct { cell int; constant int64; isConstant bool }
type phiPlan struct { dst int; edges []valueRef }
type plannedOp struct {
	kind planKind
	instr, consumer ssa.Instruction
	x, y intRef
	dst, cond, slice int
	index, stored intRef
	op token.Token
}
type blockPlan struct { phis []phiPlan; ops []plannedOp }
```

Use `-1` as the missing-cell sentinel and initialize every descriptor explicitly.

- [ ] **Step 3: Compile every block into one ordered list**

`compileBlockPlan` collects leading Phis, skips `DebugRef`, combines a safe adjacent `IndexAddr` pair, emits optimized int BinOp/If/Jump operations when operands are resolvable, and otherwise emits `planGeneric`. It must preserve instruction order exactly.

- [ ] **Step 4: Implement one type-agnostic Phi resolver**

Select the predecessor once, stage all source `value.Value`s, then commit all destinations. Use an eight-element stack buffer and allocate only for larger Phi groups. Non-cell operands call `readValue`.

- [ ] **Step 5: Replace `runFrame` with one plan loop**

The outer loop resolves Phis once and walks `blockPlan.ops`. `runPlannedOp` writes `value.MakeInt` or `value.MakeBool` directly to canonical cells; `planGeneric` calls `visitInstr`. Handle `contNext`, `contJump`, and `contReturn` once in `runFrame`.

- [ ] **Step 6: Delete `fast_plan.go` and run focused tests**

```bash
go test ./internal/interp -run 'TestFrameLayoutBuildsOneOrdered|TestInterp_(PhiAssignmentsAreSimultaneous|ForLoop|NestedLoops|FibRecursive)' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit the unified plan**

```bash
git add internal/interp/plan.go internal/interp/frame.go internal/interp/fuse_test.go internal/interp/fast_plan.go
git commit -m "refactor(interp): execute one ordered block plan"
```

### Task 4: Fold indexed pairs into the unified plan

**Files:**
- Modify: `internal/interp/plan.go`
- Modify: `internal/interp/composite.go`
- Modify: `internal/interp/ops.go`
- Test: `internal/interp/fuse_test.go`
- Test: `internal/interp/perf_test.go`

**Interfaces:**
- Consumes: `fusableIndexAddrConsumer`, native `Value.IntSlice`, generic `runIndexAddr`, `runStore`, and `runUnOp`.
- Produces: `planIntIndexLoad` and `planIntIndexStore` in the same operation loop, with generic pair fallback.

- [ ] **Step 1: Write and run the failing planned-index test**

Replace `TestFrameLayoutCachesFusableIndexAddrConsumers` in `internal/interp/fuse_test.go` with:

```go
func TestFrameLayoutCombinesSafeIndexAddrPairs(t *testing.T) {
	const src = `func Touch() int { s := make([]int, 2); s[0] = 7; return s[0] }`
	ctx := context.Background()
	unit, err := frontend.NewBuilder().Build(ctx, frontend.Source{Content: src}, stubEnv{}, frontend.Config{})
	if err != nil { t.Fatalf("Build: %v", err) }
	layout := (&program{}).frameLayout(unit.Package().Func("Touch"))
	var loads, stores int
	for _, block := range layout.blocks {
		for _, op := range block.ops {
			switch op.kind {
			case planIntIndexLoad: loads++
			case planIntIndexStore: stores++
			}
		}
	}
	if loads == 0 || stores == 0 { t.Fatalf("planned loads=%d stores=%d", loads, stores) }
}
```

Run `go test ./internal/interp -run TestFrameLayoutCombinesSafeIndexAddrPairs -count=1`. Expected RED because Task 3's planner has not yet combined indexed pairs.

- [ ] **Step 2: Implement native planned load/store**

Read the slice, index, and stored value from canonical cells or constants. For native `[]int`, perform the access and write a load result with `value.MakeInt`. Return `handled=false` when `IntSlice()` does not match.

- [ ] **Step 3: Implement generic pair fallback**

When the native shape does not match, invoke `visitInstr` for the original `IndexAddr` and then its consumer. Preserve errors and continuation signals from either instruction.

- [ ] **Step 4: Delete the address side channel**

Remove `addrRef`, `setReflectAddrRef`, `setIntSliceAddrRef`, `frame.addrRefs`, and `fr.addrRef` branches in Store and UnOp. Generic `runIndexAddr` materializes an ordinary reflect pointer for patterns not combined by the plan.

- [ ] **Step 5: Run indexed-access and allocation tests**

```bash
go test ./internal/interp -run 'Test(FusableIndexAddrConsumer|FrameLayoutCombinesSafeIndexAddrPairs|RunSliceFromMakeSliceArray|InterpIntSliceLoopAllocations)' -count=1
```

Expected: PASS and BubbleSort allocations remain below the existing 500-allocation guard.

- [ ] **Step 6: Commit indexed-plan simplification**

```bash
git add internal/interp/plan.go internal/interp/composite.go internal/interp/ops.go internal/interp/fuse_test.go internal/interp/perf_test.go
git commit -m "refactor(interp): plan indexed slice operations"
```

### Task 5: Enforce the 3.0x performance gate

**Files:**
- Modify if required: `internal/interp/plan.go`
- Modify: `docs/PERFORMANCE_OPTIMIZATION_2026-06_CN.md`

**Interfaces:**
- Consumes: baseline medians from Task 1.
- Produces: final median table, slowdown ratios, allocation deltas, and any narrow optimized plan operations required to pass.

- [ ] **Step 1: Repeat Task 1's benchmark commands and counts**

Expected: every benchmark completes successfully.

- [ ] **Step 2: Calculate `after_median_ns / before_median_ns` for every benchmark**

Expected: every slowdown is `<= 3.0`; report `B/op` and `allocs/op` deltas separately.

- [ ] **Step 3: Profile and optimize only a failing operation**

```bash
go test ./tests -run '^$' -bench '^BenchmarkGig_<Name>$' -benchtime=3s -cpuprofile=/tmp/gig-<name>.cpu
go tool pprof -top /tmp/gig-<name>.cpu
```

Add a narrow `planKind` only when the profile identifies a repeated generic operation. Never restore typed-dirty cells, a second store, a second block loop, or parallel per-kind arrays.

- [ ] **Step 4: Record and commit the measured table**

```bash
git add internal/interp/plan.go docs/PERFORMANCE_OPTIMIZATION_2026-06_CN.md
git commit -m "perf(interp): keep readable plan within budget"
```

### Task 6: Update docs and run the full suite

**Files:**
- Modify: `docs/ARCHITECTURE.md`
- Modify: `docs/ARCHITECTURE_CN.md`
- Modify: `docs/GIG_RULE_ENGINE_OVERVIEW_CN.md`

**Interfaces:**
- Consumes: final implementation and benchmark table.
- Produces: documentation naming one cell store, one block plan, generic Phi resolution, and measured performance loss.

- [ ] **Step 1: Replace descriptions of slots, typed dirty caches, `addrRefs`, and parallel fast plans**

Document the canonical map/backing-store relationship and ordered optimized/generic operations. State that Phi remains a required block-entry phase but is no longer a type-specific engine.

- [ ] **Step 2: Run formatting and static checks**

```bash
gofmt -w internal/interp/plan.go internal/interp/frame.go internal/interp/interp.go internal/interp/composite.go internal/interp/ops.go internal/interp/fuse_test.go internal/interp/interp_test.go
go vet ./internal/interp ./host ./importer
```

Expected: formatting succeeds and `go vet` exits 0.

- [ ] **Step 3: Run correctness and race verification**

```bash
go test ./...
go test -race ./internal/interp ./host ./importer ./tests
(cd benchmarks && go test ./...)
```

Expected: all commands PASS.

- [ ] **Step 4: Commit documentation**

```bash
git add docs/ARCHITECTURE.md docs/ARCHITECTURE_CN.md docs/GIG_RULE_ENGINE_OVERVIEW_CN.md
git commit -m "docs: explain readable interpreter execution"
```
