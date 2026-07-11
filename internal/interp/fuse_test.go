package interp

import (
	"context"
	"go/token"
	"reflect"
	"testing"

	"golang.org/x/tools/go/ssa"

	"github.com/t04dJ14n9/gig/internal/frontend"
	"github.com/t04dJ14n9/gig/value"
)

func TestFusableIndexAddrConsumerRecognizesAdjacentSliceLoadAndStore(t *testing.T) {
	const src = `
func TouchSlice() int {
	s := make([]int, 2)
	s[0] = 7
	x := s[0]
	return x
}
`
	ctx := context.Background()
	unit, err := frontend.NewBuilder().Build(ctx, frontend.Source{Content: src}, stubEnv{}, frontend.Config{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	fn := unit.Package().Func("TouchSlice")
	if fn == nil {
		t.Fatal("TouchSlice not found")
	}

	var sawStore, sawLoad bool
	for _, block := range fn.Blocks {
		for i := 0; i+1 < len(block.Instrs); i++ {
			indexAddr, ok := block.Instrs[i].(*ssa.IndexAddr)
			if !ok {
				continue
			}
			switch next := block.Instrs[i+1].(type) {
			case *ssa.Store:
				if next.Addr == indexAddr && fusableIndexAddrConsumer(indexAddr, next) {
					sawStore = true
				}
			case *ssa.UnOp:
				if next.Op == token.MUL && next.X == indexAddr && fusableIndexAddrConsumer(indexAddr, next) {
					sawLoad = true
				}
			}
		}
	}
	if !sawStore {
		t.Fatal("did not find a fusable adjacent IndexAddr -> Store pair")
	}
	if !sawLoad {
		t.Fatal("did not find a fusable adjacent IndexAddr -> UnOp(*) pair")
	}
}

func TestRunSliceFromMakeSliceArrayProducesNativeIntSlice(t *testing.T) {
	const src = `
func MakeSliceArray() int {
	s := make([]int, 2)
	return len(s)
}
`
	ctx := context.Background()
	unit, err := frontend.NewBuilder().Build(ctx, frontend.Source{Content: src}, stubEnv{}, frontend.Config{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	progIface, err := NewEngine().NewProgram(ctx, unit, stubEnv{}, Config{})
	if err != nil {
		t.Fatalf("NewProgram: %v", err)
	}
	prog := progIface.(*program)
	fn := unit.Package().Func("MakeSliceArray")
	if fn == nil {
		t.Fatal("MakeSliceArray not found")
	}
	var alloc *ssa.Alloc
	var slice *ssa.Slice
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if x, ok := instr.(*ssa.Alloc); ok && alloc == nil {
				alloc = x
			}
			if x, ok := instr.(*ssa.Slice); ok && slice == nil {
				slice = x
			}
		}
	}
	if alloc == nil || slice == nil {
		t.Fatalf("expected SSA Alloc+Slice, got alloc=%v slice=%v", alloc, slice)
	}
	fr := prog.newFrame(fn, nil)
	if _, _, err := prog.runAlloc(fr, alloc); err != nil {
		t.Fatalf("runAlloc: %v", err)
	}
	if _, _, err := prog.runSlice(fr, slice); err != nil {
		t.Fatalf("runSlice: %v", err)
	}
	got, err := prog.readValue(fr, slice)
	if err != nil {
		t.Fatalf("readValue: %v", err)
	}
	if _, ok := got.IntSlice(); !ok {
		t.Fatalf("runSlice stored %s, want native IntSlice", got.Kind())
	}
}

func TestRunIndexAddrMaterializesReflectPointerForGenericPairFallback(t *testing.T) {
	const src = `func Store(s []int) { s[0] = 7 }`
	ctx := context.Background()
	unit, err := frontend.NewBuilder().Build(ctx, frontend.Source{Content: src}, stubEnv{}, frontend.Config{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	progIface, err := NewEngine().NewProgram(ctx, unit, stubEnv{}, Config{})
	if err != nil {
		t.Fatalf("NewProgram: %v", err)
	}
	prog := progIface.(*program)
	fn := unit.Package().Func("Store")
	var indexAddr *ssa.IndexAddr
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if candidate, ok := instr.(*ssa.IndexAddr); ok {
				indexAddr = candidate
				break
			}
		}
	}
	if indexAddr == nil {
		t.Fatal("Store has no IndexAddr")
	}

	fr := prog.newFrame(fn, nil)
	fr.bindValue(fn.Params[0], reflectValue(reflect.ValueOf([]int{0})))
	if _, _, err := prog.runIndexAddr(fr, indexAddr); err != nil {
		t.Fatalf("runIndexAddr: %v", err)
	}
	got, err := prog.readValue(fr, indexAddr)
	if err != nil {
		t.Fatalf("readValue: %v", err)
	}
	rv, ok := got.Reflect()
	if !ok || rv.Kind() != reflect.Ptr || rv.Elem().Kind() != reflect.Int {
		t.Fatalf("runIndexAddr stored %s/%v, want reflect pointer to int", got.Kind(), rv)
	}
}

func TestPlannedIndexPairsFallBackToGenericReflectSlice(t *testing.T) {
	const src = `func Touch(s []int) int { s[0] = 7; return s[0] }`
	ctx := context.Background()
	unit, err := frontend.NewBuilder().Build(ctx, frontend.Source{Content: src}, stubEnv{}, frontend.Config{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	prog, err := NewEngine().NewProgram(ctx, unit, stubEnv{}, Config{})
	if err != nil {
		t.Fatalf("NewProgram: %v", err)
	}
	backing := []int{0}
	arg, err := value.DefaultConverter().FromReflect(reflect.ValueOf(backing))
	if err != nil {
		t.Fatalf("FromReflect: %v", err)
	}
	if _, ok := arg.IntSlice(); ok {
		t.Fatal("test argument unexpectedly used the native IntSlice shape")
	}
	got, err := prog.Call(ctx, "Touch", []value.Value{arg})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	expectInt(t, got, 7)
	if backing[0] != 7 {
		t.Fatalf("backing[0] = %d, want 7", backing[0])
	}
}

func TestFrameLayoutCombinesSafeIndexAddrPairs(t *testing.T) {
	const src = `func Touch() int { s := make([]int, 2); s[0] = 7; return s[0] }`
	ctx := context.Background()
	unit, err := frontend.NewBuilder().Build(ctx, frontend.Source{Content: src}, stubEnv{}, frontend.Config{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	layout := (&program{}).frameLayout(unit.Package().Func("Touch"))
	var loads, stores int
	for _, block := range layout.blocks {
		for _, op := range block.ops {
			switch op.kind {
			case planIntIndexLoad:
				loads++
			case planIntIndexStore:
				stores++
			}
		}
	}
	if loads == 0 || stores == 0 {
		t.Fatalf("planned loads=%d stores=%d", loads, stores)
	}
}

func TestFrameLayoutKeepsNamedIntSlicePairsGeneric(t *testing.T) {
	const src = `type S []int; func Touch(s S) int { s[0] = 7; return s[0] }`
	ctx := context.Background()
	unit, err := frontend.NewBuilder().Build(ctx, frontend.Source{Content: src}, stubEnv{}, frontend.Config{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	layout := (&program{}).frameLayout(unit.Package().Func("Touch"))
	var loads, stores int
	for _, block := range layout.blocks {
		for _, op := range block.ops {
			switch op.kind {
			case planIntIndexLoad:
				loads++
			case planIntIndexStore:
				stores++
			}
		}
	}
	if loads != 0 || stores != 0 {
		t.Fatalf("planned named-slice loads=%d stores=%d, want zero", loads, stores)
	}
}

func TestFrameLayoutBuildsOneOrderedIntLoopPlan(t *testing.T) {
	const src = `func Sum() int { s := 0; for i := 1; i <= 1000; i++ { s += i }; return s }`
	ctx := context.Background()
	unit, err := frontend.NewBuilder().Build(ctx, frontend.Source{Content: src}, stubEnv{}, frontend.Config{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	fn := unit.Package().Func("Sum")
	layout := (&program{}).frameLayout(fn)
	var sawPhi, sawInt, sawIf, sawJump bool
	for _, block := range layout.blocks {
		if len(block.phis) != 0 {
			sawPhi = true
		}
		for _, op := range block.ops {
			switch op.kind {
			case planIntBinOp:
				sawInt = true
			case planIf:
				sawIf = true
			case planJump:
				sawJump = true
			}
		}
	}
	if !sawPhi || !sawInt || !sawIf || !sawJump {
		t.Fatalf("plan shapes phi=%v int=%v if=%v jump=%v", sawPhi, sawInt, sawIf, sawJump)
	}
}

func TestFrameUsesOneCanonicalValuePerSSAValue(t *testing.T) {
	const src = `func Identity(x int) int { return x }`
	ctx := context.Background()
	unit, err := frontend.NewBuilder().Build(ctx, frontend.Source{Content: src}, stubEnv{}, frontend.Config{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	fn := unit.Package().Func("Identity")
	prog := &program{}
	layout := prog.frameLayout(fn)
	fr := prog.newFrameWithLayout(fn, nil, layout)
	param := fn.Params[0]
	idx, ok := fr.layout.index[param]
	if !ok {
		t.Fatal("parameter has no canonical value")
	}
	fr.values[idx] = value.MakeInt(42)
	got, err := prog.readValue(fr, param)
	if err != nil {
		t.Fatalf("readValue: %v", err)
	}
	if got.Int() != 42 {
		t.Fatalf("readValue = %d, want 42", got.Int())
	}
	indexed, ok := fr.value(param)
	if !ok || indexed.Int() != fr.values[idx].Int() {
		t.Fatal("layout index did not select the canonical value")
	}
	second := prog.newFrameWithLayout(fn, nil, layout)
	if &fr.values[idx] == &second.values[idx] {
		t.Fatal("frames share mutable value storage")
	}
}

func TestMakeFuncValueSupportsDirectAndReflectCalls(t *testing.T) {
	const src = `
func AddOne(x int) int {
	return x + 1
}
`
	ctx := context.Background()
	unit, err := frontend.NewBuilder().Build(ctx, frontend.Source{Content: src}, stubEnv{}, frontend.Config{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	progIface, err := NewEngine().NewProgram(ctx, unit, stubEnv{}, Config{})
	if err != nil {
		t.Fatalf("NewProgram: %v", err)
	}
	prog := progIface.(*program)
	fn := unit.Package().Func("AddOne")
	if fn == nil {
		t.Fatal("AddOne not found")
	}

	v, err := prog.makeFuncValue(ctx, fn, nil)
	if err != nil {
		t.Fatalf("makeFuncValue: %v", err)
	}
	raw, ok := v.Func()
	if !ok {
		t.Fatalf("makeFuncValue kind = %s, want func", v.Kind())
	}
	callable, ok := raw.(*interpretedFunc)
	if !ok {
		t.Fatalf("func payload = %T, want *interpretedFunc", raw)
	}
	got, err := callable.Call([]value.Value{value.MakeInt(2)}, 0)
	if err != nil {
		t.Fatalf("direct Call: %v", err)
	}
	expectInt(t, got, 3)

	rv, ok := v.Reflect()
	if !ok || rv.Kind() != reflect.Func {
		t.Fatalf("Reflect() = %v/%v, want func", rv.Kind(), ok)
	}
	out := rv.Call([]reflect.Value{reflect.ValueOf(4)})
	if len(out) != 1 || out[0].Int() != 5 {
		t.Fatalf("reflect call result = %v, want 5", out)
	}
}
