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
