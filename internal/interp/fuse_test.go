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
