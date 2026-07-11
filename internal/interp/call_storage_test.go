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
