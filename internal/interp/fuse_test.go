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

func TestFrameLayoutCachesFusableIndexAddrConsumers(t *testing.T) {
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

	layout := (&program{}).frameLayout(fn)
	if len(layout.fusedIndexAddr) == 0 {
		t.Fatal("frame layout did not cache any fusable IndexAddr consumers")
	}
	var sawBlockPlan bool
	var sawFastIndexAddr bool
	for _, plan := range layout.blockPlans {
		for _, consumer := range plan.fusedIndexAddrConsumers {
			if consumer != nil {
				sawBlockPlan = true
			}
		}
		for _, instr := range plan.fastIndexAddrs {
			if instr.kind != fastIndexNone {
				sawFastIndexAddr = true
			}
		}
	}
	if !sawBlockPlan {
		t.Fatal("frame layout did not cache fusable consumers by block instruction index")
	}
	if !sawFastIndexAddr {
		t.Fatal("frame layout did not cache typed int-slice IndexAddr plan")
	}
	for indexAddr, consumer := range layout.fusedIndexAddr {
		if !fusableIndexAddrConsumer(indexAddr, consumer) {
			t.Fatalf("cached non-fusable consumer %T for %s", consumer, indexAddr.Name())
		}
	}
}

func TestFrameLayoutCachesFastIntLoopPlan(t *testing.T) {
	const src = `
func ArithmeticSum() int {
	sum := 0
	for i := 1; i <= 1000; i++ {
		sum = sum + i
	}
	return sum
}
`
	ctx := context.Background()
	unit, err := frontend.NewBuilder().Build(ctx, frontend.Source{Content: src}, stubEnv{}, frontend.Config{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	fn := unit.Package().Func("ArithmeticSum")
	if fn == nil {
		t.Fatal("ArithmeticSum not found")
	}

	layout := (&program{}).frameLayout(fn)
	if len(layout.blockPlans) != len(fn.Blocks) {
		t.Fatalf("block plan table length = %d, want %d", len(layout.blockPlans), len(fn.Blocks))
	}
	var sawPhi, sawBinOp, sawIf, sawJump bool
	var sawFastBlock bool
	for _, plan := range layout.blockPlans {
		if plan == nil {
			continue
		}
		if len(plan.fastBlockOps) > 0 {
			sawFastBlock = true
		}
		if len(plan.fastPhis) > 0 {
			sawPhi = true
		}
		for _, instr := range plan.fastInstrs {
			switch instr.kind {
			case fastIntBinOp:
				sawBinOp = true
			case fastIf:
				sawIf = true
			case fastJump:
				sawJump = true
			}
		}
	}
	if !sawPhi {
		t.Fatal("frame layout did not cache fast int phi plan")
	}
	if !sawBinOp {
		t.Fatal("frame layout did not cache fast int binop plan")
	}
	if !sawIf {
		t.Fatal("frame layout did not cache fast if plan")
	}
	if !sawJump {
		t.Fatal("frame layout did not cache fast jump plan")
	}
	if !sawFastBlock {
		t.Fatal("frame layout did not cache a full fast block plan")
	}
}

func TestFrameUsesOneCanonicalCellPerSSAValue(t *testing.T) {
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
	cell, ok := fr.cell(param)
	if !ok {
		t.Fatal("parameter has no canonical cell")
	}
	cell.Value = value.MakeInt(42)
	got, err := prog.readValue(fr, param)
	if err != nil {
		t.Fatalf("readValue: %v", err)
	}
	if got.Int() != 42 {
		t.Fatalf("readValue = %d, want 42", got.Int())
	}
	if fr.cells[param] != cell {
		t.Fatal("cell lookup did not preserve identity")
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
