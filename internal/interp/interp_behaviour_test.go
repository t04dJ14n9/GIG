package interp

import (
	"context"
	"reflect"
	"testing"

	"golang.org/x/tools/go/ssa"

	"github.com/t04dJ14n9/gig/internal/frontend"
	"github.com/t04dJ14n9/gig/value"
)

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
	fr := prog.newFrame(fn)
	if err := prog.runAlloc(fr, alloc); err != nil {
		t.Fatalf("runAlloc: %v", err)
	}
	if err := prog.runSlice(fr, slice); err != nil {
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

	fr := prog.newFrame(fn)
	fr.bindValue(fn.Params[0], reflectValue(reflect.ValueOf([]int{0})))
	if err := prog.runIndexAddr(fr, indexAddr); err != nil {
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

// A slice that did not arrive in the native IntSlice shape must still index,
// store, and write through to the caller's backing array.
func TestIndexStoreWritesThroughGenericReflectSlice(t *testing.T) {
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

// Indexing a slice of a *named* slice type must behave exactly like indexing
// []int. Named slice types previously took a fast path that assumed the
// native int-slice shape and read back the wrong element.
func TestIndexStoreHandlesNamedIntSliceType(t *testing.T) {
	const src = `type S []int; func Touch(s S) int { s[0] = 7; return s[0] }`
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
	got, err := prog.Call(ctx, "Touch", []value.Value{arg})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	expectInt(t, got, 7)
	if backing[0] != 7 {
		t.Fatalf("backing[0] = %d, want 7", backing[0])
	}
}

// An int loop exercises Phi merges, int arithmetic, If and Jump in one
// function. It is the smallest program that covers the whole dispatch loop.
func TestIntLoopExercisesPhiArithmeticAndBranching(t *testing.T) {
	const src = `func Sum() int { s := 0; for i := 1; i <= 1000; i++ { s += i }; return s }`
	ctx := context.Background()
	unit, err := frontend.NewBuilder().Build(ctx, frontend.Source{Content: src}, stubEnv{}, frontend.Config{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	fn := unit.Package().Func("Sum")

	var sawPhi, sawBinOp, sawIf, sawJump bool
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			switch instr.(type) {
			case *ssa.Phi:
				sawPhi = true
			case *ssa.BinOp:
				sawBinOp = true
			case *ssa.If:
				sawIf = true
			case *ssa.Jump:
				sawJump = true
			}
		}
	}
	if !sawPhi || !sawBinOp || !sawIf || !sawJump {
		t.Fatalf("SSA shapes phi=%v binop=%v if=%v jump=%v", sawPhi, sawBinOp, sawIf, sawJump)
	}

	prog, err := NewEngine().NewProgram(ctx, unit, stubEnv{}, Config{})
	if err != nil {
		t.Fatalf("NewProgram: %v", err)
	}
	got, err := prog.Call(ctx, "Sum", nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	expectInt(t, got, 500500)
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
	fr := prog.newFrameWithLayout(fn, layout)
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
	second := prog.newFrameWithLayout(fn, layout)
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
