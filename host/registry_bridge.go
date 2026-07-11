// registry_bridge.go provides FromRegistry, a stop-gap host.Environment
// that delegates to the legacy importer.PackageRegistry. It exists so
// the new SSA pipeline can run against the same external-package
// definitions (fmt, strings, ...) that legacy gig already supports —
// while still allowing hot external functions and methods to register
// value.Value-based DirectCall wrappers. Calls without wrappers fall
// back to reflect.Call.
package host

import (
	"fmt"
	"go/types"
	"reflect"

	"github.com/t04dJ14n9/gig/importer"
	"github.com/t04dJ14n9/gig/model/external"
	"github.com/t04dJ14n9/gig/value"
)

// FromRegistry wraps a legacy importer.PackageRegistry in a
// host.Environment.
func FromRegistry(reg importer.PackageRegistry) Environment {
	return &registryBridge{reg: reg, imp: importer.NewImporter(reg)}
}

type registryBridge struct {
	reg importer.PackageRegistry
	imp *importer.Importer
}

// Import satisfies types.Importer by delegating to the legacy importer.
func (b *registryBridge) Import(path string) (*types.Package, error) {
	return b.imp.Import(path)
}

// AutoImport answers for the frontend's identifier-based auto-import
// step.
func (b *registryBridge) AutoImport(name string) (Import, bool) {
	if b.reg == nil {
		return Import{}, false
	}
	path, pkg, ok := b.reg.AutoImport(name)
	if !ok {
		return Import{}, false
	}
	displayName := name
	if pkg != nil && pkg.Name != "" {
		displayName = pkg.Name
	}
	return Import{Path: path, Name: displayName}, true
}

func (b *registryBridge) lookupObject(pkgPath, name string, want external.ObjectKind) (*importer.ExternalPackage, *external.ExternalObject, bool) {
	if b == nil || b.reg == nil {
		return nil, nil, false
	}
	pkg := b.reg.GetPackageByPath(pkgPath)
	if pkg == nil {
		pkg = b.reg.GetPackageByName(pkgPath)
	}
	if pkg == nil {
		return nil, nil, false
	}
	obj := pkg.Objects[name]
	if obj == nil || obj.Kind != want {
		return nil, nil, false
	}
	return pkg, obj, true
}

// LookupFunc returns a host.Function backed by the legacy registry's
// function metadata. Generated DirectCall wrappers run without
// reflect.Value.Call; functions without wrappers fall back to reflect.
func (b *registryBridge) LookupFunc(pkgPath, name string) (Function, bool) {
	_, obj, ok := b.lookupObject(pkgPath, name, external.ObjectKindFunction)
	if ok {
		rv := reflect.ValueOf(obj.Value)
		if rv.Kind() == reflect.Func {
			return &reflectFunc{name: obj.Name, fn: rv, directCall: obj.DirectCall}, true
		}
	}
	if b == nil || b.reg == nil {
		return nil, false
	}
	fn, ok := b.reg.LookupExternalFunc(pkgPath, name)
	if !ok {
		return nil, false
	}
	rv := reflect.ValueOf(fn)
	if rv.Kind() != reflect.Func {
		return nil, false
	}
	return &reflectFunc{name: name, fn: rv}, true
}

// LookupVar returns the host-side address of a registered variable.
// The legacy registry hands back the variable's *T address; we wrap
// the pointed-at value (T) but record the address-rv so Set can update
// the storage slot. SSA programs read globals via UnOp(MUL) on the
// global pointer, so the value we return must already be the pointee
// — but the global's underlying type may itself be a pointer (e.g.
// time.UTC is *time.Location), in which case we keep it as-is.
func (b *registryBridge) LookupVar(pkgPath, name string) (Variable, bool) {
	adapterName := name
	var ptr any
	_, obj, ok := b.lookupObject(pkgPath, name, external.ObjectKindVariable)
	if ok {
		adapterName = obj.Name
		ptr = obj.Value
	} else {
		if b == nil || b.reg == nil {
			return nil, false
		}
		ptr, ok = b.reg.LookupExternalVar(pkgPath, name)
		if !ok {
			return nil, false
		}
	}
	addr := reflect.ValueOf(ptr)
	if addr.Kind() != reflect.Ptr {
		// Defensive: registry should always hand back a pointer.
		return &reflectVar{name: adapterName, rv: addr}, true
	}
	return &reflectVar{name: adapterName, rv: addr.Elem(), addr: addr}, true
}

// LookupConst is best-effort: legacy gig stores const values inside the
// ExternalPackage's Objects map. Pull from there.
func (b *registryBridge) LookupConst(pkgPath, name string) (Constant, bool) {
	_, obj, ok := b.lookupObject(pkgPath, name, external.ObjectKindConstant)
	if !ok || obj.Value == nil {
		return nil, false
	}
	conv := value.DefaultConverter()
	v, err := conv.FromAny(obj.Value)
	if err != nil {
		return nil, false
	}
	return &reflectConst{name: obj.Name, v: v}, true
}

// LookupType returns a host.Type for a named type registered in the
// legacy registry.
func (b *registryBridge) LookupType(pkgPath, name string) (Type, bool) {
	pkg, obj, ok := b.lookupObject(pkgPath, name, external.ObjectKindType)
	if ok {
		if rt, found := pkg.Types[name]; found && rt != nil {
			return &reflectType{name: obj.Name, rt: rt}, true
		}
	}
	if b == nil || b.reg == nil {
		return nil, false
	}
	rt, ok := b.reg.LookupExternalTypeByName(pkgPath, name)
	if !ok {
		return nil, false
	}
	return &reflectType{name: name, rt: rt}, true
}

func (b *registryBridge) LookupReflectType(t types.Type) (reflect.Type, bool) {
	if b.reg == nil {
		return nil, false
	}
	if rt, ok := b.reg.LookupExternalType(t); ok {
		return rt, true
	}
	// Identity miss: named types may have been registered by the
	// importer for a different *types.Named instance than the one the
	// type-checker handed us. Lookup by package path + type name keys
	// on string identity instead.
	if named, ok := t.(*types.Named); ok {
		obj := named.Obj()
		if obj != nil && obj.Pkg() != nil {
			if rt, ok := b.reg.LookupExternalTypeByName(obj.Pkg().Path(), obj.Name()); ok {
				return rt, true
			}
			// Unexported named types (e.g. encoding/binary's `bigEndian`,
			// reachable only via the exported `BigEndian` var) are not
			// registered explicitly because gentool only emits AddType
			// for exported types. Recover their reflect.Type by scanning
			// the package's already-registered values for one whose
			// reflect.Type matches the named-type identifier — this gives
			// us the host runtime's exact type identity, which methods
			// like ByteOrder.PutUint16 require for dispatch.
			if pkg := b.reg.GetPackageByPath(obj.Pkg().Path()); pkg != nil {
				for _, exObj := range pkg.Objects {
					if exObj.Value == nil {
						continue
					}
					rt := reflect.TypeOf(exObj.Value)
					if rt.Kind() == reflect.Ptr {
						rt = rt.Elem()
					}
					if rt.Name() == obj.Name() {
						return rt, true
					}
				}
			}
		}
	}
	return nil, false
}

// LookupMethod resolves a method DirectCall wrapper registered for a
// host-defined named type. typeName uses the legacy key convention:
// import/path.TypeName, for example "strings.Reader".
func (b *registryBridge) LookupMethod(typeName, methodName string) (Method, bool) {
	if b.reg == nil {
		return nil, false
	}
	dc, ok := b.reg.LookupMethodDirectCall(typeName, methodName)
	if !ok {
		return nil, false
	}
	return &directMethod{typeName: typeName, name: methodName, directCall: dc}, true
}

func (b *registryBridge) LookupInterfaceProxy(*types.Interface) (InterfaceProxy, bool) {
	return nil, false
}

// --- helpers ----------------------------------------------------------------

type reflectFunc struct {
	name       string
	fn         reflect.Value
	directCall func([]value.Value) ([]value.Value, error)
}

func (f *reflectFunc) Name() string                { return f.name }
func (f *reflectFunc) Signature() *types.Signature { return nil }

func (f *reflectFunc) Call(args []value.Value) ([]value.Value, error) {
	if f.directCall != nil {
		return f.directCall(args)
	}
	if f.fn.Kind() != reflect.Func {
		return nil, fmt.Errorf("host: %s is not a function", f.name)
	}
	conv := value.DefaultConverter()
	ft := f.fn.Type()
	numIn := ft.NumIn()
	variadic := ft.IsVariadic()

	// Build reflect args. For variadic functions, gather extras into a slice.
	rargs := make([]reflect.Value, 0, len(args))
	if variadic {
		fixed := numIn - 1
		for i := 0; i < fixed && i < len(args); i++ {
			rv, err := conv.ToReflect(args[i], ft.In(i))
			if err != nil {
				return nil, fmt.Errorf("host %s arg %d: %w", f.name, i, err)
			}
			rargs = append(rargs, rv)
		}
		varType := ft.In(numIn - 1) // []T
		elemType := varType.Elem()
		extras := args[fixed:]
		// SSA pre-packs variadic args into a single []T-shaped slice
		// at the call site. Three shapes can arrive here:
		//   1. extras[0] is a slice whose type matches varType exactly
		//      — call via CallSlice so reflect spreads it.
		//   2. extras[0] is a slice whose element type doesn't match
		//      (e.g. []any from a synthetic interface fallback while
		//      varType is []io.Writer). Unwrap the slice so each
		//      element is converted to elemType individually.
		//   3. Multiple non-slice args — the user passed each
		//      variadic positionally. Pack them ourselves.
		if len(extras) == 1 {
			if rv, ok := extras[0].Reflect(); ok && rv.Kind() == reflect.Slice {
				if rv.Type() == varType {
					rargs = append(rargs, rv)
					return f.callAndConvert(rargs, true)
				}
				// Element-type mismatch: explode and re-pack.
				extras = make([]value.Value, rv.Len())
				for i := 0; i < rv.Len(); i++ {
					ev, err := conv.FromReflect(rv.Index(i))
					if err != nil {
						return nil, fmt.Errorf("host %s variadic explode %d: %w", f.name, i, err)
					}
					extras[i] = ev
				}
			}
		}
		slice := reflect.MakeSlice(varType, len(extras), len(extras))
		for i, a := range extras {
			rv, err := conv.ToReflect(a, elemType)
			if err != nil {
				return nil, fmt.Errorf("host %s variadic arg %d: %w", f.name, i, err)
			}
			slice.Index(i).Set(rv)
		}
		rargs = append(rargs, slice)
		return f.callAndConvert(rargs, true)
	}
	// Non-variadic.
	if len(args) != numIn {
		return nil, fmt.Errorf("host %s: arg count %d != %d", f.name, len(args), numIn)
	}
	for i, a := range args {
		rv, err := conv.ToReflect(a, ft.In(i))
		if err != nil {
			return nil, fmt.Errorf("host %s arg %d: %w", f.name, i, err)
		}
		rargs = append(rargs, rv)
	}
	return f.callAndConvert(rargs, false)
}

func (f *reflectFunc) CallDirect(args []value.Value) ([]value.Value, bool, error) {
	if f.directCall == nil {
		return nil, false, nil
	}
	results, err := f.directCall(args)
	return results, true, err
}

func (f *reflectFunc) callAndConvert(rargs []reflect.Value, variadicSpread bool) ([]value.Value, error) {
	conv := value.DefaultConverter()
	var rresults []reflect.Value
	if variadicSpread {
		rresults = f.fn.CallSlice(rargs)
	} else {
		rresults = f.fn.Call(rargs)
	}
	out := make([]value.Value, len(rresults))
	for i, r := range rresults {
		v, err := conv.FromReflect(r)
		if err != nil {
			return nil, fmt.Errorf("host %s result %d: %w", f.name, i, err)
		}
		out[i] = v
	}
	return out, nil
}

type directMethod struct {
	typeName   string
	name       string
	directCall func(value.Value, []value.Value) value.Value
}

func (m *directMethod) Name() string                { return m.name }
func (m *directMethod) Receiver() types.Type        { return nil }
func (m *directMethod) Signature() *types.Signature { return nil }

func (m *directMethod) Call(recv value.Value, args []value.Value) ([]value.Value, error) {
	return []value.Value{m.directCall(recv, args)}, nil
}

func (m *directMethod) CallDirect(recv value.Value, args []value.Value) (value.Value, bool, error) {
	return m.directCall(recv, args), true, nil
}

// Variable is the host-provided storage. Get returns the addressable
// reflect.Value for the slot (so callers can do their own load via
// UnOp(MUL)); Set writes through the address.
type reflectVar struct {
	name string
	rv   reflect.Value
	addr reflect.Value
}

func (v *reflectVar) Name() string     { return v.name }
func (v *reflectVar) Type() types.Type { return nil }

// Get returns the var's *T address (i.e. the reflect-pointer to the
// storage slot). The interpreter expects this so its UnOp(MUL) load
// path produces the actual pointee. Returning the pre-deref'd value
// would cause a double-load downstream.
func (v *reflectVar) Get() (value.Value, error) {
	if v.addr.IsValid() {
		return value.DefaultConverter().FromReflect(v.addr)
	}
	return value.DefaultConverter().FromReflect(v.rv)
}

func (v *reflectVar) Set(val value.Value) error {
	target := v.rv
	if v.addr.IsValid() {
		target = v.addr.Elem()
	}
	if !target.CanSet() {
		return fmt.Errorf("host: var %s is not settable", v.name)
	}
	rv, err := value.DefaultConverter().ToReflect(val, target.Type())
	if err != nil {
		return err
	}
	target.Set(rv)
	return nil
}

type reflectConst struct {
	name string
	v    value.Value
}

func (c *reflectConst) Name() string       { return c.name }
func (c *reflectConst) Type() types.Type   { return nil }
func (c *reflectConst) Value() value.Value { return c.v }

type reflectType struct {
	name string
	rt   reflect.Type
}

func (t *reflectType) Name() string              { return t.name }
func (t *reflectType) GoType() types.Type        { return nil }
func (t *reflectType) ReflectType() reflect.Type { return t.rt }
