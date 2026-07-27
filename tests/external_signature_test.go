package tests

import (
	"context"
	"reflect"
	"testing"

	"github.com/t04dJ14n9/gig"
	"github.com/t04dJ14n9/gig/importer"
)

type namedContextLoadFunc func(context.Context) (string, error)

func callNamedContextLoadFunc(ctx context.Context, f namedContextLoadFunc) (string, error) {
	return f(ctx)
}

func TestExternalNamedFuncLiteralWithContext(t *testing.T) {
	pkg := importer.RegisterPackage("test/namedcontextloader", "namedcontextloader")
	pkg.AddFunction("Call", callNamedContextLoadFunc, "")

	src := `package main
import (
	"context"
	loader "test/namedcontextloader"
)

func Probe() string {
	got, err := loader.Call(context.Background(), func(context.Context) (string, error) {
		return "ok", nil
	})
	if err != nil {
		return err.Error()
	}
	return got
}`

	prog, err := gig.Build(src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got, err := prog.Run("Probe")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !reflect.DeepEqual(got, "ok") {
		t.Fatalf("got %v (%T), want ok", got, got)
	}
}
