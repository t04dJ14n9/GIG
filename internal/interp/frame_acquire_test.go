package interp

import (
	"context"
	"testing"

	"github.com/t04dJ14n9/gig/internal/frontend"
)

func TestNewFrameCreatesFreshFrameForRecursiveFunction(t *testing.T) {
	const src = `
func Fib(n int) int {
	if n < 2 {
		return n
	}
	return Fib(n-1) + Fib(n-2)
}
`
	ctx := context.Background()
	unit, err := frontend.NewBuilder().Build(ctx, frontend.Source{Content: src}, stubEnv{}, frontend.Config{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	fn := unit.Package().Func("Fib")
	if fn == nil {
		t.Fatal("Fib not found")
	}
	p := &program{}

	fr1 := p.newFrame(fn, nil)
	fr2 := p.newFrame(fn, nil)
	if fr1 == fr2 {
		t.Fatalf("newFrame reused a frame; want a fresh frame per call")
	}
}
