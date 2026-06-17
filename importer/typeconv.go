// typeconv.go converts reflect.Type to go/types.Type for registered external packages.
//
// Keep the direction straight:
//   - reflect.Type describes real host Go values registered in importer.Registry.
//   - go/types.Type is what the parser/type checker and SSA builder need.
//
// This file builds the compile-time mirror of host types.
package importer

import (
	"go/constant"
	"go/token"
	"go/types"
	"reflect"
	"strings"
	"sync"
)

// reflectKindToBasicKind maps reflect.Kind to go/types.BasicKind. The
// table is the single source of truth for basic-type conversions inside
// the importer; it was historically shared with the legacy bytecode VM
// but is now duplicated here so the importer carries no dependency on
// bytecode/.
var reflectKindToBasicKind = map[reflect.Kind]types.BasicKind{
	reflect.Bool:          types.Bool,
	reflect.Int:           types.Int,
	reflect.Int8:          types.Int8,
	reflect.Int16:         types.Int16,
	reflect.Int32:         types.Int32,
	reflect.Int64:         types.Int64,
	reflect.Uint:          types.Uint,
	reflect.Uint8:         types.Uint8,
	reflect.Uint16:        types.Uint16,
	reflect.Uint32:        types.Uint32,
	reflect.Uint64:        types.Uint64,
	reflect.Uintptr:       types.Uintptr,
	reflect.Float32:       types.Float32,
	reflect.Float64:       types.Float64,
	reflect.Complex64:     types.Complex64,
	reflect.Complex128:    types.Complex128,
	reflect.String:        types.String,
	reflect.UnsafePointer: types.UnsafePointer,
}

// typeOf converts reflect.Type to types.Type.
//
// register.go uses this variable while constructing ExternalObject entries. The
// indirection keeps registration code decoupled from the full conversion helpers
// in this file and avoids initialization cycles.
var typeOf func(reflect.Type) types.Type

func init() {
	typeOf = convertReflectType
}

// basicTypeFromReflectKind returns the canonical *types.Basic for a
// reflect.Kind, or nil if the kind is not a basic type.
func basicTypeFromReflectKind(k reflect.Kind) *types.Basic {
	if bk, ok := reflectKindToBasicKind[k]; ok {
		return types.Typ[bk]
	}
	return nil
}

// typeCache caches converted types and breaks recursion for self-referential
// types such as linked-list nodes. Named types are inserted as placeholders
// before their underlying type is fully converted.
var typeCache sync.Map // map[reflect.Type]types.Type

// convertToConstantValue converts a Go value to a constant.Value for use in types.Const.
// Uses reflection to handle all basic types uniformly.
func convertToConstantValue(val any) constant.Value {
	rv := reflect.ValueOf(val)
	switch rv.Kind() {
	case reflect.Bool:
		return constant.MakeBool(rv.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return constant.MakeInt64(rv.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return constant.MakeUint64(rv.Uint())
	case reflect.Float32, reflect.Float64:
		return constant.MakeFloat64(rv.Float())
	case reflect.Complex64, reflect.Complex128:
		c := rv.Complex()
		re := constant.MakeFloat64(real(c))
		im := constant.MakeFloat64(imag(c))
		return constant.BinaryOp(re, token.ADD, constant.BinaryOp(im, token.MUL, constant.MakeImag(constant.MakeInt64(1))))
	case reflect.String:
		return constant.MakeString(rv.String())
	default:
		return constant.MakeUnknown()
	}
}

// convertReflectType converts a host reflect.Type to the go/types.Type seen by
// interpreted source code.
//
// This is the main entry point for compile-time type conversion. It preserves
// named types, method sets, package identity, struct fields/tags, function
// signatures, and interface method sets closely enough for type checking and SSA.
func convertReflectType(rt reflect.Type) types.Type {
	if rt == nil {
		return types.Typ[types.Invalid]
	}

	// Check cache first to prevent infinite recursion
	if cached, ok := typeCache.Load(rt); ok {
		return cached.(types.Type)
	}

	// For named types, handle specially to support self-referential types.
	// Named types include types like "time.Duration" which has Kind() == reflect.Int64
	// but has a distinct name. We must preserve the named type, not collapse to the
	// underlying basic type.
	// However, basic type aliases like "byte" (alias for uint8) have Name() != "" but
	// are NOT true named types - they should be treated as basic types to preserve
	// byte/uint8 compatibility. We detect this by checking if the type name matches
	// the underlying kind's name in types.Typ.
	if rt.Name() != "" {
		// Check if this is a basic type with a name alias (e.g., uint8, byte, string).
		// For basic kinds, types.Typ[kind] gives the canonical type with the kind's name.
		// If the reflect type's name matches the canonical kind name, it's a basic type alias.
		isBasicAlias := false
		if bt := basicTypeFromReflectKind(rt.Kind()); bt != nil && bt.Name() == rt.Name() {
			isBasicAlias = true
		}
		// Preserve predeclared named interfaces such as error as their universe
		// objects. Creating a fresh Named with no package would make it distinct
		// from the builtin type used by parsed source.
		if rt.Kind() == reflect.Interface && rt.PkgPath() == "" {
			if obj := types.Universe.Lookup(rt.Name()); obj != nil {
				if _, ok := obj.Type().Underlying().(*types.Interface); ok {
					typeCache.Store(rt, obj.Type())
					return obj.Type()
				}
			}
		}
		// If it's a basic type alias, don't treat as named type - fall through to basic handling.
		if !isBasicAlias {
			// Create a placeholder named type to break recursion.
			// Use rt.PkgPath() to attach the correct package, so the type checker
			// and compiler can distinguish types with the same name from different
			// packages (e.g., encoding/json.Encoder vs encoding/xml.Encoder).
			pkg := getOrCreateTypesPackage(rt.PkgPath())
			typeName := types.NewTypeName(0, pkg, rt.Name(), nil)
			named := types.NewNamed(typeName, types.Typ[types.Invalid], nil)
			typeCache.Store(rt, named)

			// Now compute the actual underlying type
			underlying := convertReflectTypeForUnderlying(rt)
			named.SetUnderlying(underlying)

			// Non-interface named types need their method set copied from reflect.
			// Named interfaces carry their methods on the underlying interface.
			if rt.Kind() != reflect.Interface {
				addMethodsToNamed(named, rt)
			}

			typeCache.Store(rt, named)

			return named
		}
	}

	switch rt.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128,
		reflect.String, reflect.UnsafePointer:
		return basicTypeFromReflectKind(rt.Kind())

	case reflect.Array:
		elem := convertReflectType(rt.Elem())
		result := types.NewArray(elem, int64(rt.Len()))
		typeCache.Store(rt, result)
		return result

	case reflect.Slice:
		elem := convertReflectType(rt.Elem())
		result := types.NewSlice(elem)
		typeCache.Store(rt, result)
		return result

	case reflect.Chan:
		elem := convertReflectType(rt.Elem())
		var dir types.ChanDir
		switch rt.ChanDir() {
		case reflect.SendDir:
			dir = types.SendOnly
		case reflect.RecvDir:
			dir = types.RecvOnly
		default:
			dir = types.SendRecv
		}
		result := types.NewChan(dir, elem)
		typeCache.Store(rt, result)
		return result

	case reflect.Func:
		result := convertFuncType(rt)
		typeCache.Store(rt, result)
		return result

	case reflect.Interface:
		result := convertInterfaceType(rt)
		typeCache.Store(rt, result)
		return result

	case reflect.Map:
		key := convertReflectType(rt.Key())
		elem := convertReflectType(rt.Elem())
		result := types.NewMap(key, elem)
		typeCache.Store(rt, result)
		return result

	case reflect.Ptr:
		elem := convertReflectType(rt.Elem())
		result := types.NewPointer(elem)
		typeCache.Store(rt, result)
		return result

	case reflect.Struct:
		result := convertStructType(rt)
		typeCache.Store(rt, result)
		return result

	default:
		// For other named types, create a TypeName
		if rt.Name() != "" {
			pkg := getOrCreateTypesPackage(rt.PkgPath())
			typeName := types.NewTypeName(0, pkg, rt.Name(), nil)
			underlying := convertReflectTypeForUnderlying(rt)
			result := types.NewNamed(typeName, underlying, nil)
			typeCache.Store(rt, result)
			return result
		}
		return types.Typ[types.Invalid]
	}
}

// convertReflectTypeForUnderlying converts a reflect.Type to its underlying types.Type.
// This is used for named types to avoid wrapping in another Named type.
// Unlike convertReflectType, it does NOT cache results in typeCache — doing so
// would overwrite the Named type entry that the caller stored for recursion breaking.
func convertReflectTypeForUnderlying(rt reflect.Type) types.Type {
	// For basic named types, return the corresponding basic type
	if bt := basicTypeFromReflectKind(rt.Kind()); bt != nil {
		return bt
	}

	switch rt.Kind() {
	case reflect.Struct:
		return convertStructType(rt)
	case reflect.Ptr:
		return types.NewPointer(convertReflectType(rt.Elem()))
	case reflect.Slice:
		return types.NewSlice(convertReflectType(rt.Elem()))
	case reflect.Array:
		return types.NewArray(convertReflectType(rt.Elem()), int64(rt.Len()))
	case reflect.Map:
		return types.NewMap(convertReflectType(rt.Key()), convertReflectType(rt.Elem()))
	case reflect.Func:
		return convertFuncType(rt)
	case reflect.Interface:
		return convertInterfaceTypeUncached(rt)
	case reflect.Chan:
		elem := convertReflectType(rt.Elem())
		var dir types.ChanDir
		switch rt.ChanDir() {
		case reflect.SendDir:
			dir = types.SendOnly
		case reflect.RecvDir:
			dir = types.RecvOnly
		default:
			dir = types.SendRecv
		}
		return types.NewChan(dir, elem)
	default:
		return types.Typ[types.Invalid]
	}
}

// typePkgCache caches *types.Package objects by package path to ensure
// that the same package path always maps to the same *types.Package instance.
var typePkgCache sync.Map // map[string]*types.Package

// getOrCreateTypesPackage returns a cached *types.Package for the given
// package path, creating one if it doesn't exist yet. The package name is
// derived from the last path segment (e.g., "encoding/json" → "json").
func getOrCreateTypesPackage(pkgPath string) *types.Package {
	if pkgPath == "" {
		return nil
	}
	if cached, ok := typePkgCache.Load(pkgPath); ok {
		return cached.(*types.Package)
	}
	// Derive package name from path (last segment)
	name := pkgPath
	if idx := strings.LastIndex(pkgPath, "/"); idx >= 0 {
		name = pkgPath[idx+1:]
	}
	pkg := types.NewPackage(pkgPath, name)
	actual, _ := typePkgCache.LoadOrStore(pkgPath, pkg)
	return actual.(*types.Package)
}

// convertFuncType converts a reflect.Func type to a types.Signature.
// It builds parameter and result tuples from the function's type information.
func convertFuncType(rt reflect.Type) *types.Signature {
	// Build parameter types
	var params []*types.Var
	for i := 0; i < rt.NumIn(); i++ {
		paramType := convertReflectType(rt.In(i))
		params = append(params, types.NewVar(0, nil, "", paramType))
	}

	// Build result types
	var results []*types.Var
	for i := 0; i < rt.NumOut(); i++ {
		resultType := convertReflectType(rt.Out(i))
		results = append(results, types.NewVar(0, nil, "", resultType))
	}

	// Check if last param is variadic
	variadic := rt.IsVariadic()

	return types.NewSignatureType(
		nil,      // recv
		nil, nil, // type params
		types.NewTuple(params...),
		types.NewTuple(results...),
		variadic,
	)
}

// convertInterfaceType converts a reflect.Interface type to a types.Interface.
// For empty interfaces (any), returns types.NewInterfaceType(nil, nil).
func convertInterfaceType(rt reflect.Type) *types.Interface {
	return convertInterfaceTypeWithCache(rt, true)
}

func convertInterfaceTypeUncached(rt reflect.Type) *types.Interface {
	return convertInterfaceTypeWithCache(rt, false)
}

func convertInterfaceTypeWithCache(rt reflect.Type, cache bool) *types.Interface {
	if rt.NumMethod() == 0 {
		// Empty interface (any)
		return types.NewInterfaceType(nil, nil)
	}

	// Create a placeholder interface and cache it first to break recursion
	iface := types.NewInterfaceType(nil, nil)
	if cache {
		typeCache.Store(rt, iface)
	}

	var methods []*types.Func
	for i := 0; i < rt.NumMethod(); i++ {
		method := rt.Method(i)
		sig := convertFuncType(method.Type)
		methods = append(methods, types.NewFunc(0, nil, method.Name, sig))
	}

	// Update the interface with the methods
	// Note: types.NewInterfaceType creates a complete interface, so we need to
	// create a new one with the methods
	result := types.NewInterfaceType(methods, nil)
	if cache {
		typeCache.Store(rt, result)
	}
	return result
}

// convertStructType converts a reflect.Struct type to a types.Struct.
// It preserves field names, types, anonymous fields, and struct tags.
func convertStructType(rt reflect.Type) *types.Struct {
	var fields []*types.Var
	var tags []string

	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		fieldType := convertReflectType(field.Type)
		fields = append(fields, types.NewField(0, nil, field.Name, fieldType, field.Anonymous))
		tags = append(tags, string(field.Tag))
	}

	return types.NewStruct(fields, tags)
}

// addMethodsToNamed adds methods from a reflect.Type to a types.Named type.
// This allows the type checker to find methods on external types.
// Both value receiver and pointer receiver methods are added.
// For interface types, methods do NOT include a receiver parameter.
func addMethodsToNamed(named *types.Named, rt reflect.Type) {
	// Check if this is an interface type - interface methods don't have receiver params
	isInterface := rt.Kind() == reflect.Interface

	// Enumerate all exported methods on the value receiver
	addMethodsFromType(named, rt, isInterface, false)

	// For interface types, don't process pointer receiver methods
	if isInterface {
		return
	}

	// Also enumerate methods on the pointer receiver (*T)
	addMethodsFromType(named, reflect.PointerTo(rt), false, true)
}

// addMethodsFromType adds exported methods from methodSource to the named type.
// If skipReceiver is false (interface types), parameters start at index 0.
// If isPointerRecv is true, skips methods already present on named and uses pointer receiver.
func addMethodsFromType(named *types.Named, methodSource reflect.Type, isInterface, isPointerRecv bool) {
	for i := 0; i < methodSource.NumMethod(); i++ {
		method := methodSource.Method(i)
		if !method.IsExported() {
			continue
		}
		// For pointer receiver pass, skip methods already added from value receiver
		if isPointerRecv {
			alreadyAdded := false
			for j := 0; j < named.NumMethods(); j++ {
				if named.Method(j).Name() == method.Name {
					alreadyAdded = true
					break
				}
			}
			if alreadyAdded {
				continue
			}
		}

		methodType := method.Type
		if methodType.Kind() != reflect.Func {
			continue
		}
		// For non-interface types, we need at least 1 param (the receiver)
		if !isInterface && methodType.NumIn() < 1 {
			continue
		}

		// Build the parameter list
		// For interfaces, start at 0 (no receiver). For concrete types, skip receiver at 0.
		startIdx := 0
		if !isInterface {
			startIdx = 1
		}
		var params []*types.Var
		for j := startIdx; j < methodType.NumIn(); j++ {
			paramType := convertReflectType(methodType.In(j))
			params = append(params, types.NewVar(0, nil, "", paramType))
		}

		// Build the result list
		var results []*types.Var
		for j := 0; j < methodType.NumOut(); j++ {
			resultType := convertReflectType(methodType.Out(j))
			results = append(results, types.NewVar(0, nil, "", resultType))
		}

		// Build receiver: nil for interfaces, named for value, *named for pointer
		var recv *types.Var
		if !isInterface {
			recvType := types.Type(named)
			if isPointerRecv {
				recvType = types.NewPointer(named)
			}
			recv = types.NewVar(0, nil, "", recvType)
		}

		sig := types.NewSignatureType(
			recv,
			nil, nil,
			types.NewTuple(params...),
			types.NewTuple(results...),
			methodType.IsVariadic(),
		)

		fn := types.NewFunc(0, named.Obj().Pkg(), method.Name, sig)
		named.AddMethod(fn)
	}
}
