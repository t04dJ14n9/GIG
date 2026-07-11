package host

import (
	"io"
	"reflect"
	"testing"

	"github.com/t04dJ14n9/gig/importer"
	"github.com/t04dJ14n9/gig/value"
)

type registryBridgeRecord struct {
	Value int
}

type countingRegistry struct {
	*importer.Registry
	packageLookups    int
	legacyFuncLookups int
}

type legacyLookupRegistry struct {
	*importer.Registry
	function any
	variable any
	typ      reflect.Type
}

func (r *legacyLookupRegistry) GetPackageByPath(string) *importer.ExternalPackage {
	return nil
}

func (r *legacyLookupRegistry) GetPackageByName(string) *importer.ExternalPackage {
	return nil
}

func (r *legacyLookupRegistry) LookupExternalFunc(pkgPath, name string) (any, bool) {
	if pkgPath != "example/legacy" || name != "Value" {
		return nil, false
	}
	return r.function, true
}

func (r *legacyLookupRegistry) LookupExternalVar(pkgPath, name string) (any, bool) {
	if pkgPath != "example/legacy" || name != "Mutable" {
		return nil, false
	}
	return r.variable, true
}

func (r *legacyLookupRegistry) LookupExternalTypeByName(pkgPath, name string) (reflect.Type, bool) {
	if pkgPath != "example/legacy" || name != "Record" {
		return nil, false
	}
	return r.typ, true
}

func (r *countingRegistry) GetPackageByPath(path string) *importer.ExternalPackage {
	r.packageLookups++
	return r.Registry.GetPackageByPath(path)
}

func (r *countingRegistry) LookupExternalFunc(pkgPath, name string) (any, bool) {
	r.legacyFuncLookups++
	return r.Registry.LookupExternalFunc(pkgPath, name)
}

func TestRegistryBridgeResolvesFunctionFromOneObjectLookup(t *testing.T) {
	reg := &countingRegistry{Registry: importer.NewRegistry()}
	pkg := reg.RegisterPackage("example/once", "once")
	pkg.AddFunction("Value", func() int { return 7 }, "")
	if _, ok := FromRegistry(reg).LookupFunc("example/once", "Value"); !ok {
		t.Fatal("LookupFunc did not find Value")
	}
	if reg.packageLookups != 1 || reg.legacyFuncLookups != 0 {
		t.Fatalf("package lookups=%d legacy function lookups=%d, want 1/0", reg.packageLookups, reg.legacyFuncLookups)
	}
}

func TestRegistryBridgeFallsBackToLegacyTypedLookups(t *testing.T) {
	registered := 11
	recordType := reflect.TypeOf(registryBridgeRecord{})
	reg := &legacyLookupRegistry{
		Registry: importer.NewRegistry(),
		function: func() int { return 7 },
		variable: &registered,
		typ:      recordType,
	}
	env := FromRegistry(reg)

	t.Run("function", func(t *testing.T) {
		fn, ok := env.LookupFunc("example/legacy", "Value")
		if !ok {
			t.Fatal("LookupFunc did not use the legacy typed lookup")
		}
		got, err := fn.Call(nil)
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
		if len(got) != 1 || got[0].Int() != 7 {
			t.Fatalf("Value = %v, want 7", got)
		}
	})

	t.Run("variable", func(t *testing.T) {
		variable, ok := env.LookupVar("example/legacy", "Mutable")
		if !ok {
			t.Fatal("LookupVar did not use the legacy typed lookup")
		}
		got, err := variable.Get()
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		ptr, ok := got.Interface().(*int)
		if !ok || ptr != &registered {
			t.Fatalf("Get returned %T %v, want registered pointer %p", got.Interface(), got.Interface(), &registered)
		}
	})

	t.Run("type", func(t *testing.T) {
		hostType, ok := env.LookupType("example/legacy", "Record")
		if !ok {
			t.Fatal("LookupType did not use the legacy typed lookup")
		}
		if got := hostType.ReflectType(); got != recordType {
			t.Fatalf("ReflectType = %v, want %v", got, recordType)
		}
	})
}

func TestRegistryBridgeFunctionFallsBackToReflect(t *testing.T) {
	reg := importer.NewRegistry()
	pkg := reg.RegisterPackage("example/reflect", "reflectpkg")
	pkg.AddFunction("Add", func(a, b int) int { return a + b }, "")

	fn, ok := FromRegistry(reg).LookupFunc("example/reflect", "Add")
	if !ok {
		t.Fatal("LookupFunc did not find Add")
	}
	got, err := fn.Call([]value.Value{value.MakeInt(2), value.MakeInt(5)})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if len(got) != 1 || got[0].Int() != 7 {
		t.Fatalf("Add = %v, want 7", got)
	}
}

func TestRegistryBridgeLookupFuncRejectsNonFunctionObject(t *testing.T) {
	reg := importer.NewRegistry()
	pkg := reg.RegisterPackage("example/kinds", "kinds")
	x := 1
	pkg.AddVariable("X", &x, "")

	if _, ok := FromRegistry(reg).LookupFunc("example/kinds", "X"); ok {
		t.Fatal("LookupFunc accepted a variable object")
	}
}

func TestRegistryBridgeLookupsRejectMissingObjects(t *testing.T) {
	reg := importer.NewRegistry()
	reg.RegisterPackage("example/present", "present")
	env := FromRegistry(reg)

	lookups := []struct {
		name   string
		lookup func(string, string) bool
	}{
		{name: "function", lookup: func(path, name string) bool {
			_, ok := env.LookupFunc(path, name)
			return ok
		}},
		{name: "variable", lookup: func(path, name string) bool {
			_, ok := env.LookupVar(path, name)
			return ok
		}},
		{name: "constant", lookup: func(path, name string) bool {
			_, ok := env.LookupConst(path, name)
			return ok
		}},
		{name: "type", lookup: func(path, name string) bool {
			_, ok := env.LookupType(path, name)
			return ok
		}},
	}

	for _, tc := range lookups {
		t.Run(tc.name+"/missing_package", func(t *testing.T) {
			if tc.lookup("example/missing", "Missing") {
				t.Fatalf("%s lookup found an object in a missing package", tc.name)
			}
		})
		t.Run(tc.name+"/missing_name", func(t *testing.T) {
			if tc.lookup("example/present", "Missing") {
				t.Fatalf("%s lookup found a missing object", tc.name)
			}
		})
	}
}

func TestRegistryBridgeVariableAdapter(t *testing.T) {
	reg := importer.NewRegistry()
	pkg := reg.RegisterPackage("example/objects", "objects")
	registered := 11
	pkg.AddVariable("Mutable", &registered, "")

	variable, ok := FromRegistry(reg).LookupVar("example/objects", "Mutable")
	if !ok {
		t.Fatal("LookupVar did not find Mutable")
	}
	if variable.Name() != "Mutable" {
		t.Fatalf("Name = %q, want Mutable", variable.Name())
	}
	got, err := variable.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	ptr, ok := got.Interface().(*int)
	if !ok {
		t.Fatalf("Get returned %T, want *int", got.Interface())
	}
	if ptr != &registered || *ptr != 11 {
		t.Fatalf("Get returned %p -> %d, want registered address %p -> 11", ptr, *ptr, &registered)
	}
	if err := variable.Set(value.MakeInt(29)); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if registered != 29 {
		t.Fatalf("registered value = %d after Set, want 29", registered)
	}
}

func TestRegistryBridgeConstantAdapter(t *testing.T) {
	reg := importer.NewRegistry()
	pkg := reg.RegisterPackage("example/objects", "objects")
	pkg.AddConstant("Answer", 42, "")

	constant, ok := FromRegistry(reg).LookupConst("example/objects", "Answer")
	if !ok {
		t.Fatal("LookupConst did not find Answer")
	}
	if constant.Name() != "Answer" {
		t.Fatalf("Name = %q, want Answer", constant.Name())
	}
	if got := constant.Value().Interface(); got != int(42) {
		t.Fatalf("Value = %v (%T), want 42 (int)", got, got)
	}
}

func TestRegistryBridgeTypeAdapters(t *testing.T) {
	reg := importer.NewRegistry()
	pkg := reg.RegisterPackage("example/types", "typespkg")
	recordType := reflect.TypeOf(registryBridgeRecord{})
	readerType := reflect.TypeOf((*io.Reader)(nil)).Elem()
	pkg.AddType("registryBridgeRecord", recordType, "")
	pkg.AddType("Reader", readerType, "")

	if pkg.Objects["Reader"].Value != nil {
		t.Fatalf("registered Reader zero value = %v, want nil interface value", pkg.Objects["Reader"].Value)
	}

	env := FromRegistry(reg)
	imported, err := env.Import("example/types")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	for _, tc := range []struct {
		name string
		want reflect.Type
	}{
		{name: "registryBridgeRecord", want: recordType},
		{name: "Reader", want: readerType},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hostType, ok := env.LookupType("example/types", tc.name)
			if !ok {
				t.Fatalf("LookupType did not find %s", tc.name)
			}
			if hostType.Name() != tc.name {
				t.Fatalf("Name = %q, want %q", hostType.Name(), tc.name)
			}
			if got := hostType.ReflectType(); got != tc.want {
				t.Fatalf("ReflectType = %v, want exact type %v", got, tc.want)
			}

			obj := imported.Scope().Lookup(tc.name)
			if obj == nil {
				t.Fatalf("imported package has no %s type", tc.name)
			}
			got, ok := env.LookupReflectType(obj.Type())
			if !ok {
				t.Fatalf("LookupReflectType did not resolve imported %s type", tc.name)
			}
			if got != tc.want {
				t.Fatalf("LookupReflectType = %v, want exact type %v", got, tc.want)
			}
		})
	}
}

func TestRegistryBridgeUsesFunctionDirectCall(t *testing.T) {
	reg := importer.NewRegistry()
	pkg := reg.RegisterPackage("example/direct", "direct")
	reflectCalled := false
	pkg.AddFunction("Add", func(int, int) int {
		reflectCalled = true
		return -1
	}, "", func(args []value.Value) ([]value.Value, error) {
		return []value.Value{value.MakeInt(args[0].Int() + args[1].Int())}, nil
	})

	env := FromRegistry(reg)
	fn, ok := env.LookupFunc("example/direct", "Add")
	if !ok {
		t.Fatal("LookupFunc did not find Add")
	}
	got, err := fn.Call([]value.Value{value.MakeInt(2), value.MakeInt(3)})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if reflectCalled {
		t.Fatal("reflect function body was called; DirectCall wrapper was bypassed")
	}
	if len(got) != 1 || got[0].Int() != 5 {
		t.Fatalf("Add returned %v, want 5", got)
	}
	direct, ok := fn.(DirectFunction)
	if !ok {
		t.Fatal("LookupFunc result does not implement DirectFunction")
	}
	gotDirect, ok, err := direct.CallDirect([]value.Value{value.MakeInt(4), value.MakeInt(6)})
	if err != nil {
		t.Fatalf("CallDirect: %v", err)
	}
	if !ok || len(gotDirect) != 1 || gotDirect[0].Int() != 10 {
		t.Fatalf("CallDirect returned %v/%v, want 10/true", gotDirect, ok)
	}
}

func TestRegistryBridgeUsesMultiResultFunctionDirectCall(t *testing.T) {
	reg := importer.NewRegistry()
	pkg := reg.RegisterPackage("example/direct", "direct")
	reflectCalled := false
	pkg.AddFunction("Split", func(string) (string, string) {
		reflectCalled = true
		return "reflect", "fallback"
	}, "", func(args []value.Value) ([]value.Value, error) {
		s := args[0].Str()
		return []value.Value{value.MakeString(s[:1]), value.MakeString(s[1:])}, nil
	})

	env := FromRegistry(reg)
	fn, ok := env.LookupFunc("example/direct", "Split")
	if !ok {
		t.Fatal("LookupFunc did not find Split")
	}
	got, err := fn.Call([]value.Value{value.MakeString("go")})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if reflectCalled {
		t.Fatal("reflect function body was called; DirectCall wrapper was bypassed")
	}
	if len(got) != 2 || got[0].Str() != "g" || got[1].Str() != "o" {
		t.Fatalf("Split returned %v, want [g o]", got)
	}
}

func TestRegistryBridgeUsesMethodDirectCall(t *testing.T) {
	reg := importer.NewRegistry()
	pkg := reg.RegisterPackage("example/direct", "direct")
	pkg.AddMethodDirectCall("Counter", "Len", func(recv value.Value, _ []value.Value) value.Value {
		return value.MakeInt(42 + recv.Int())
	})

	env := FromRegistry(reg)
	method, ok := env.LookupMethod("example/direct.Counter", "Len")
	if !ok {
		t.Fatal("LookupMethod did not find Counter.Len")
	}
	got, err := method.Call(value.MakeInt(8), nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if len(got) != 1 || got[0].Int() != 50 {
		t.Fatalf("Len returned %v, want 50", got)
	}
	direct, ok := method.(DirectMethod)
	if !ok {
		t.Fatal("LookupMethod result does not implement DirectMethod")
	}
	gotDirect, ok, err := direct.CallDirect(value.MakeInt(9), nil)
	if err != nil {
		t.Fatalf("CallDirect: %v", err)
	}
	if !ok || gotDirect.Int() != 51 {
		t.Fatalf("CallDirect returned %v/%v, want 51/true", gotDirect, ok)
	}
}
