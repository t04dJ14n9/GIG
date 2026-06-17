package frontend

import (
	"context"
	"go/types"
	"reflect"
	"strings"
	"testing"

	"github.com/t04dJ14n9/gig/host"
	"github.com/t04dJ14n9/gig/value"
)

// stubEnv is a minimal host.Environment for tests. It imports nothing,
// auto-imports nothing, and looks up nothing. That is enough for any
// program whose only references are to the universe block (int, bool,
// len, append, etc.) — which covers everything we want to test the
// builder against without taking on host integration here.
type stubEnv struct{}

func (stubEnv) Import(path string) (*types.Package, error) {
	return nil, &importError{path: path}
}
func (stubEnv) AutoImport(name string) (host.Import, bool)        { return host.Import{}, false }
func (stubEnv) LookupFunc(_, _ string) (host.Function, bool)      { return nil, false }
func (stubEnv) LookupVar(_, _ string) (host.Variable, bool)       { return nil, false }
func (stubEnv) LookupConst(_, _ string) (host.Constant, bool)     { return nil, false }
func (stubEnv) LookupType(_, _ string) (host.Type, bool)          { return nil, false }
func (stubEnv) LookupReflectType(types.Type) (reflect.Type, bool) { return nil, false }
func (stubEnv) LookupMethod(_, _ string) (host.Method, bool)      { return nil, false }
func (stubEnv) LookupInterfaceProxy(*types.Interface) (host.InterfaceProxy, bool) {
	return nil, false
}

type importError struct{ path string }

func (e *importError) Error() string { return "stubEnv: cannot import " + e.path }

// stubEnvWithAutoImport is stubEnv that knows about a fixed name->Import
// map for AutoImport behaviour tests.
type stubEnvWithAutoImport struct {
	stubEnv

	auto map[string]host.Import
}

func (s stubEnvWithAutoImport) AutoImport(name string) (host.Import, bool) {
	imp, ok := s.auto[name]
	return imp, ok
}

// _ ensures Value is referenced so the import is not flagged unused if
// future tests stop using it; not currently required.
var _ value.Value

// --- success cases ----------------------------------------------------------

func TestBuilder_BuildsSimpleProgram(t *testing.T) {
	const src = `
func Add(a, b int) int {
	return a + b
}
`
	b := NewBuilder()
	unit, err := b.Build(context.Background(), Source{Content: src}, stubEnv{}, Config{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if unit.Package() == nil {
		t.Fatal("Unit.Package is nil")
	}
	if unit.FileSet() == nil {
		t.Fatal("Unit.FileSet is nil")
	}
	fn := unit.Package().Func("Add")
	if fn == nil {
		t.Fatal("expected Func(\"Add\") in built SSA")
	}
}

func TestBuilder_AutoWrapsPackageMain(t *testing.T) {
	const src = `func F() int { return 1 }` // no package decl
	b := NewBuilder()
	unit, err := b.Build(context.Background(), Source{Content: src}, stubEnv{}, Config{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if unit.Package().Pkg.Name() != "main" {
		t.Fatalf("expected package main, got %q", unit.Package().Pkg.Name())
	}
}

func TestBuilder_HonoursExplicitPackageDeclaration(t *testing.T) {
	const src = `package custom
func F() int { return 1 }`
	b := NewBuilder()
	unit, err := b.Build(context.Background(), Source{Content: src}, stubEnv{}, Config{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if unit.Package().Pkg.Name() != "custom" {
		t.Fatalf("expected package custom, got %q", unit.Package().Pkg.Name())
	}
}

func TestBuilder_HonoursExplicitPackageDeclarationAfterLeadingComment(t *testing.T) {
	const src = `// Package custom is a test package.
package custom

func F() int { return 1 }`
	b := NewBuilder()
	unit, err := b.Build(context.Background(), Source{Content: src}, stubEnv{}, Config{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if unit.Package().Pkg.Name() != "custom" {
		t.Fatalf("expected package custom, got %q", unit.Package().Pkg.Name())
	}
}

// --- banned imports ---------------------------------------------------------

func TestBuilder_RejectsUnsafeImport(t *testing.T) {
	const src = `
import "unsafe"

func F() unsafe.Pointer { return nil }
`
	b := NewBuilder()
	_, err := b.Build(context.Background(), Source{Content: src}, stubEnv{}, Config{})
	if err == nil {
		t.Fatal("expected error for unsafe import")
	}
	if !strings.Contains(err.Error(), `"unsafe"`) {
		t.Fatalf("error should name unsafe: %v", err)
	}
}

func TestBuilder_RejectsReflectImport(t *testing.T) {
	const src = `
import "reflect"

func F() reflect.Kind { return 0 }
`
	b := NewBuilder()
	_, err := b.Build(context.Background(), Source{Content: src}, stubEnv{}, Config{})
	if err == nil {
		t.Fatal("expected error for reflect import")
	}
	if !strings.Contains(err.Error(), `"reflect"`) {
		t.Fatalf("error should name reflect: %v", err)
	}
}

func TestBuilder_AllowsCustomBannedList(t *testing.T) {
	// reflect is a default ban; if the caller passes an empty banned
	// list, the import should sail through to the type-check stage
	// (where it will fail because stubEnv cannot import reflect, but
	// that is a different error class).
	const src = `
import "reflect"

func F() reflect.Kind { return 0 }
`
	b := NewBuilder()
	_, err := b.Build(context.Background(), Source{Content: src}, stubEnv{}, Config{
		BannedImports: []string{}, // explicit empty
	})
	if err == nil {
		t.Fatal("stub env cannot resolve reflect; expected an error from type-check, got nil")
	}
	if strings.Contains(err.Error(), "banned") {
		t.Fatalf("error should be from type-check / import resolution, not the banned list: %v", err)
	}
}

// --- panic policy -----------------------------------------------------------

func TestBuilder_RejectsPanicByDefault(t *testing.T) {
	const src = `func F() { panic("nope") }`
	b := NewBuilder()
	_, err := b.Build(context.Background(), Source{Content: src}, stubEnv{}, Config{})
	if err == nil {
		t.Fatal("expected error for panic() under default policy")
	}
	if !strings.Contains(err.Error(), "panic()") {
		t.Fatalf("error should mention panic: %v", err)
	}
}

func TestBuilder_AllowsPanicWhenPolicyAllow(t *testing.T) {
	const src = `func F() { panic("ok") }`
	b := NewBuilder()
	_, err := b.Build(context.Background(), Source{Content: src}, stubEnv{}, Config{Panic: PanicAllow})
	if err != nil {
		t.Fatalf("PanicAllow should permit panic, got: %v", err)
	}
}

// --- diagnostics surface from go/types --------------------------------------

func TestBuilder_ReportsTypeError(t *testing.T) {
	// Wrong type: trying to add a string to an int.
	const src = `
func Bad() int {
	return "x" + 1
}
`
	b := NewBuilder()
	_, err := b.Build(context.Background(), Source{Content: src}, stubEnv{}, Config{})
	if err == nil {
		t.Fatal("expected type-check error")
	}
}

func TestBuilder_RejectsNilEnvironment(t *testing.T) {
	b := NewBuilder()
	_, err := b.Build(context.Background(), Source{Content: `func F(){}`}, nil, Config{})
	if err == nil {
		t.Fatal("expected error for nil environment")
	}
}

func TestBuilder_HonoursContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before Build runs
	b := NewBuilder()
	_, err := b.Build(ctx, Source{Content: `func F(){}`}, stubEnv{}, Config{})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

// --- AutoImport behaviour ---------------------------------------------------

func TestBuilder_AutoImportsRegisteredPackages(t *testing.T) {
	// The stub claims fakemod.X exists at path "fakemod". With
	// AutoImport=true, the builder should splice an import in. The
	// type-check will then fail because stubEnv cannot Import
	// "fakemod" — that failure is what proves the splice happened.
	const src = `func F() int { return fakemod.X }`
	env := stubEnvWithAutoImport{
		auto: map[string]host.Import{"fakemod": {Path: "fakemod", Name: "fakemod"}},
	}
	b := NewBuilder()
	_, err := b.Build(context.Background(), Source{Content: src}, env, Config{AutoImport: true})
	if err == nil {
		t.Fatal("expected import-resolution error after auto-import")
	}
	if !strings.Contains(err.Error(), "fakemod") {
		t.Fatalf("error should reference fakemod: %v", err)
	}
}

func TestBuilder_DoesNotAutoImportWhenDisabled(t *testing.T) {
	// Same source, but AutoImport=false. Should fail at type-check
	// with an "undeclared name" error, not an import-resolution error.
	const src = `func F() int { return fakemod.X }`
	env := stubEnvWithAutoImport{
		auto: map[string]host.Import{"fakemod": {Path: "fakemod", Name: "fakemod"}},
	}
	b := NewBuilder()
	_, err := b.Build(context.Background(), Source{Content: src}, env, Config{AutoImport: false})
	if err == nil {
		t.Fatal("expected error")
	}
	// The error should NOT mention import resolution — it should be
	// about an undeclared name.
	if strings.Contains(err.Error(), "cannot import") {
		t.Fatalf("AutoImport=false should not splice imports, got: %v", err)
	}
}

// TestBuilder_IfaceBanRejectsInterpretedStructToHostInterface verifies
// the G_iface_ban rule: passing an interpreted struct to a host
// function expecting a non-empty interface is rejected at compile time.
//
// This test uses a stubEnv-backed host that exposes a single function
// taking io.Writer-shaped interface; the test source defines its own
// struct type and tries to pass it.
func TestBuilder_IfaceBanRejectsInterpretedStructToHostInterface(t *testing.T) {
	t.Skip("requires a host iface registration; skeleton kept for documentation")
}
