package interp

import (
	"context"
	"testing"
	"time"

	"github.com/t04dJ14n9/gig/host"
	"github.com/t04dJ14n9/gig/importer"
	"github.com/t04dJ14n9/gig/internal/frontend"
)

func TestGoHostFunctionExecutes(t *testing.T) {
	signal := make(chan struct{}, 1)
	reg := importer.NewRegistry()
	reg.RegisterPackage("example/async", "async").AddFunction("Notify", func() {
		signal <- struct{}{}
	}, "")
	env := host.FromRegistry(reg)
	ctx := context.Background()
	unit, err := frontend.NewBuilder().Build(ctx, frontend.Source{Content: `
		import "example/async"

		func Spawn() { go async.Notify() }
	`}, env, frontend.Config{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	prog, err := NewEngine().NewProgram(ctx, unit, env, Config{})
	if err != nil {
		t.Fatalf("new program: %v", err)
	}
	if _, err := prog.Call(ctx, "Spawn", nil); err != nil {
		t.Fatalf("call Spawn: %v", err)
	}

	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for host goroutine")
	}
}
