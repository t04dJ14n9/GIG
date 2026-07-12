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

func TestCallSSAIntoWritesSingleResultSink(t *testing.T) {
	p, fn := buildFrameValueFixture(t, `func Inc(x int) int { return x + 1 }`, "Inc")
	var result value.Value
	results, err := p.callSSAInto(
		context.Background(), nil, fn,
		[]value.Value{value.MakeInt(4)}, nil, 0, &result,
	)
	if err != nil {
		t.Fatalf("callSSAInto: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("results = %v, want no aliasing result slice", results)
	}
	if result.Int() != 5 {
		t.Fatalf("result = %v, want 5", result)
	}
}

func TestCallSSAIntoAllocatesForMultiResult(t *testing.T) {
	p, fn := buildFrameValueFixture(t, `func Pair(x int) (int, int) { return x, x + 1 }`, "Pair")
	sentinel := value.MakeInt(99)
	results, err := p.callSSAInto(context.Background(), nil, fn, []value.Value{value.MakeInt(7)}, nil, 0, &sentinel)
	if err != nil {
		t.Fatalf("callSSAInto: %v", err)
	}
	if len(results) != 2 || results[0].Int() != 7 || results[1].Int() != 8 {
		t.Fatalf("results = %v, want [7 8]", results)
	}
	if sentinel.Int() != 99 {
		t.Fatalf("sentinel = %v, want unchanged 99", sentinel)
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
