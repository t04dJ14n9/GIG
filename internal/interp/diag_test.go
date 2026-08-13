package interp

import (
	"context"
	"testing"

	"github.com/t04dJ14n9/gig/internal/frontend"
	"github.com/t04dJ14n9/gig/value"
)

// TestDiagNestedStruct reproduces the "Field index out of range" failure
// to drive a fix.
func TestDiagNestedStruct(t *testing.T) {
	const src = `
func NestedStruct() int {
	type Inner struct {
		Val int
	}
	type Outer struct {
		A Inner
		B Inner
	}
	o := Outer{
		A: Inner{Val: 10},
		B: Inner{Val: 20},
	}
	return o.A.Val + o.B.Val
}
`
	results := runProgram(t, src, "NestedStruct")
	if len(results) != 1 || results[0].Kind() != value.KindInt || results[0].Int() != 30 {
		t.Fatalf("expected 30, got %v", results)
	}
}

// TestDiagEmbeddedField reproduces the embedded-field failure.
func TestDiagEmbeddedField(t *testing.T) {
	const src = `
func EmbeddedField() int {
	type Base struct {
		ID   int
		Name int
	}
	type Extended struct {
		Base
		Extra int
	}
	e := Extended{
		Base:  Base{ID: 42, Name: 7},
		Extra: 100,
	}
	return e.ID + e.Name + e.Extra
}
`
	results := runProgram(t, src, "EmbeddedField")
	if len(results) != 1 || results[0].Int() != 149 {
		t.Fatalf("expected 149, got %v", results)
	}
}

// TestDiagInterfaceMethod tests interface method dispatch on
// interpreted types.
func TestDiagInterfaceMethod(t *testing.T) {
	const src = `
type Adder interface{ Add(int) int }
type AdderStruct struct{ v int }
func (s *AdderStruct) Add(n int) int { return s.v + n }
func InterfaceMethod() int {
	var a Adder = &AdderStruct{v: 10}
	return a.Add(5)
}
`
	ctx := context.Background()
	unit, err := frontend.NewBuilder().Build(ctx, frontend.Source{Content: src}, stubEnv{}, frontend.Config{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	pkg := unit.Package()
	for k, m := range pkg.Members {
		t.Logf("Member %T %q", m, k)
	}
	results := runProgram(t, src, "InterfaceMethod")
	if len(results) != 1 || results[0].Int() != 15 {
		t.Fatalf("expected 15, got %v", results)
	}
}

// TestDiagPointerSwap reproduces s.a, s.b = s.b, s.a for pointer
// fields. Expected 21 (after swap *s.a=2, *s.b=1, so 2*10+1).
func TestDiagPointerSwap(t *testing.T) {
	const src = `
type S struct{ a, b *int }
func PointerSwap() int {
	x, y := 1, 2
	s := S{a: &x, b: &y}
	s.a, s.b = s.b, s.a
	return *s.a*10 + *s.b
}
`
	results := runProgram(t, src, "PointerSwap")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Int() != 21 {
		t.Fatalf("expected 21, got %d", results[0].Int())
	}
}
