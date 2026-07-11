// Package value defines the runtime value type used by the v2 SSA
// interpreter (see docs/PLAN.md). Value is a 32-byte tagged union:
// primitive scalars (bool, int, uint, float, nil) live entirely in
// inline fields; composite kinds keep their payload in obj.
//
// Mutability belongs to the interpreter's surrounding storage, not Value.
// Frame-local SSA values occupy compact value slots; globals and closure
// bindings temporarily use Cell wrappers. A Value is immutable once
// constructed.
//
// This package is leaf-level: it depends only on go/types and the Go
// stdlib, never on host, frontend, or interp. That makes it cheap to
// test and reuse.
package value

import (
	"fmt"
	"go/types"
	"math"
	"reflect"
)

// Kind tags the dynamic kind stored in a Value.
type Kind uint8

const (
	// KindInvalid is the zero Value's kind.
	KindInvalid Kind = iota
	KindNil
	KindBool
	KindInt     // int, int8, int16, int32, int64
	KindUint    // uint, uint8, uint16, uint32, uint64, uintptr
	KindFloat   // float32, float64
	KindString  // string
	KindComplex // complex64, complex128
	KindPointer // *T
	KindSlice   // []T
	KindArray   // [N]T
	KindMap     // map[K]V
	KindChan    // chan T
	KindFunc    // func(...) (...)
	KindStruct  // struct{...}
	KindInterface
	KindReflect // fallback for anything not directly modelled
)

var kindNames = [...]string{
	KindInvalid:   "invalid",
	KindNil:       "nil",
	KindBool:      "bool",
	KindInt:       "int",
	KindUint:      "uint",
	KindFloat:     "float",
	KindString:    "string",
	KindComplex:   "complex",
	KindPointer:   "pointer",
	KindSlice:     "slice",
	KindArray:     "array",
	KindMap:       "map",
	KindChan:      "chan",
	KindFunc:      "func",
	KindStruct:    "struct",
	KindInterface: "interface",
	KindReflect:   "reflect",
}

// String returns the kind name for diagnostics.
func (k Kind) String() string {
	if int(k) < len(kindNames) {
		return kindNames[k]
	}
	return "unknown"
}

// Size records the original Go bit-width for numeric kinds. It lives in
// the padding gap between kind and num so it costs zero extra memory.
type Size uint8

const (
	// Size0 means "unspecified": treated as the widest variant
	// (int64, uint64, float64).
	Size0 Size = 0
	// SizePtr is the platform int / uint width (always 64-bit for gig).
	SizePtr Size = 1
	Size8   Size = 8
	Size16  Size = 16
	Size32  Size = 32
	Size64  Size = 64
)

// Value is a tagged union. Layout on 64-bit Go (32 bytes):
//
//	kind: 1 byte
//	size: 1 byte (in padding gap, free)
//	num : 8 bytes (bool flag, int, uint bits, float64 bits)
//	obj : 16 bytes (string, complex128, reflect.Value, composites, or nil)
//
// Primitives never set obj, so they cause zero GC pressure. Composite
// kinds set obj and leave num zero (except where a specific constructor
// chooses to use it).
type Value struct {
	kind Kind
	size Size
	num  int64
	obj  any
}

type reflectValueProvider interface {
	ReflectValue() reflect.Value
}

// Kind returns the dynamic kind tag.
func (v Value) Kind() Kind { return v.kind }

// SizeTag returns the original numeric width recorded by the constructor.
// Callers outside arithmetic rarely need this; it exists so Interface()
// can return the original Go type.
func (v Value) SizeTag() Size { return v.size }

// IsValid reports whether v has been assigned a kind.
func (v Value) IsValid() bool { return v.kind != KindInvalid }

// IsNil reports whether v is nil. KindNil is always nil; KindReflect is
// nil if the underlying reflect.Value reports IsNil for its kind.
// KindInterface (a MakeInterfaceBox) is nil only if both type and value
// are nil — matching Go's typed-nil-in-interface semantics.
func (v Value) IsNil() bool {
	if v.kind == KindNil {
		return true
	}
	if v.kind == KindSlice {
		if s, ok := v.obj.([]int); ok {
			return s == nil
		}
	}
	if v.kind == KindInterface {
		rv, ok := v.obj.(reflect.Value)
		if !ok || !rv.IsValid() {
			return true
		}
		// reflect.Value.IsNil on an interface returns true iff both
		// type and value are nil. That's exactly what we want.
		return rv.IsNil()
	}
	if v.kind == KindReflect {
		rv, ok := v.obj.(reflect.Value)
		if !ok || !rv.IsValid() {
			return true
		}
		switch rv.Kind() {
		case reflect.Chan, reflect.Func, reflect.Interface,
			reflect.Map, reflect.Ptr, reflect.Slice:
			return rv.IsNil()
		}
	}
	return false
}

// --- Constructors -----------------------------------------------------------

// MakeNil returns a typed-nil sentinel. The interpreter uses MakeNil
// when it knows the value is nil but does not need to record the
// declared type.
func MakeNil() Value { return Value{kind: KindNil} }

// MakeBool wraps a Go bool.
func MakeBool(b bool) Value {
	var n int64
	if b {
		n = 1
	}
	return Value{kind: KindBool, num: n}
}

// MakeInt wraps a Go int (platform width).
func MakeInt(i int64) Value   { return Value{kind: KindInt, size: SizePtr, num: i} }
func MakeInt8(i int8) Value   { return Value{kind: KindInt, size: Size8, num: int64(i)} }
func MakeInt16(i int16) Value { return Value{kind: KindInt, size: Size16, num: int64(i)} }
func MakeInt32(i int32) Value { return Value{kind: KindInt, size: Size32, num: int64(i)} }
func MakeInt64(i int64) Value { return Value{kind: KindInt, size: Size64, num: i} }

// MakeUint wraps a Go uint (platform width).
func MakeUint(u uint64) Value   { return Value{kind: KindUint, size: SizePtr, num: int64(u)} }
func MakeUint8(u uint8) Value   { return Value{kind: KindUint, size: Size8, num: int64(u)} }
func MakeUint16(u uint16) Value { return Value{kind: KindUint, size: Size16, num: int64(u)} }
func MakeUint32(u uint32) Value { return Value{kind: KindUint, size: Size32, num: int64(u)} }
func MakeUint64(u uint64) Value { return Value{kind: KindUint, size: Size64, num: int64(u)} }

// MakeFloat wraps a float64.
func MakeFloat(f float64) Value {
	return Value{kind: KindFloat, size: Size64, num: int64(math.Float64bits(f))}
}

// MakeFloat32 wraps a float32, preserving the original width.
func MakeFloat32(f float32) Value {
	return Value{kind: KindFloat, size: Size32, num: int64(math.Float64bits(float64(f)))}
}

// MakeString wraps a Go string.
func MakeString(s string) Value { return Value{kind: KindString, obj: s} }

// MakeIntSlice wraps a native []int. The SSA interpreter uses this as a
// narrow fast path for hot integer-slice loops; generic slice handling still
// falls back to KindReflect.
func MakeIntSlice(s []int) Value { return Value{kind: KindSlice, obj: s} }

// MakeFunc wraps a function-like runtime object. If the object implements
// ReflectValue() reflect.Value, Converter.ToReflect uses that reflect func
// when the value crosses a host boundary.
func MakeFunc(fn any) Value { return Value{kind: KindFunc, obj: fn} }

// MakeComplex wraps a complex128.
func MakeComplex(re, im float64) Value {
	return Value{kind: KindComplex, size: Size64, obj: complex(re, im)}
}

// MakeComplex64 wraps a complex64.
func MakeComplex64(re, im float32) Value {
	return Value{
		kind: KindComplex,
		size: Size32,
		obj:  complex(float64(re), float64(im)),
	}
}

// makeReflect is the catch-all wrapper for kinds we do not unbox. The
// interpreter and Converter use it; embedders should not.
func makeReflect(rv reflect.Value) Value {
	if !rv.IsValid() {
		return MakeNil()
	}
	return Value{kind: KindReflect, obj: rv}
}

// MakeInterfaceBox preserves a reflect.Value of interface type without
// unwrapping its dynamic value. This is the representation produced by
// SSA's MakeInterface — necessary so typed-nil-in-interface
// (e.g. var i any = ([]int)(nil)) compares != nil per Go semantics.
//
// The reflect.Value passed in must have Kind() == reflect.Interface;
// callers that have a typed concrete value should construct an
// interface-typed slot first via reflect.New(ifaceType).Elem() and Set.
func MakeInterfaceBox(rv reflect.Value) Value {
	return Value{kind: KindInterface, obj: rv}
}

// InterfaceBox returns the boxed reflect.Value if v was produced by
// MakeInterfaceBox. The bool reports whether v is an interface box.
func (v Value) InterfaceBox() (reflect.Value, bool) {
	if v.kind != KindInterface {
		return reflect.Value{}, false
	}
	rv, ok := v.obj.(reflect.Value)
	return rv, ok
}

// --- Accessors --------------------------------------------------------------

// Bool returns the underlying bool. Panics on kind mismatch.
func (v Value) Bool() bool {
	if v.kind != KindBool {
		panic(fmt.Sprintf("value: not a bool: %s", v.kind))
	}
	return v.num != 0
}

// Int returns the underlying int as int64. Panics on kind mismatch.
func (v Value) Int() int64 {
	if v.kind != KindInt {
		panic(fmt.Sprintf("value: not an int: %s", v.kind))
	}
	return v.num
}

// Uint returns the underlying uint as uint64. Panics on kind mismatch.
func (v Value) Uint() uint64 {
	if v.kind != KindUint {
		panic(fmt.Sprintf("value: not a uint: %s", v.kind))
	}
	return uint64(v.num)
}

// Float returns the underlying float as float64. Panics on kind mismatch.
func (v Value) Float() float64 {
	if v.kind != KindFloat {
		panic(fmt.Sprintf("value: not a float: %s", v.kind))
	}
	return math.Float64frombits(uint64(v.num))
}

// Str returns the underlying string. Panics on kind mismatch. Named Str
// rather than String because Stringer would conflict with fmt.
func (v Value) Str() string {
	if v.kind != KindString {
		panic(fmt.Sprintf("value: not a string: %s", v.kind))
	}
	return v.obj.(string)
}

// IntSlice returns the native []int backing this value when it was created
// by MakeIntSlice.
func (v Value) IntSlice() ([]int, bool) {
	if v.kind != KindSlice {
		return nil, false
	}
	s, ok := v.obj.([]int)
	return s, ok
}

// Func returns the function-like payload when v was created by MakeFunc.
func (v Value) Func() (any, bool) {
	if v.kind != KindFunc {
		return nil, false
	}
	return v.obj, true
}

// Complex returns the underlying complex128. Panics on kind mismatch.
func (v Value) Complex() complex128 {
	if v.kind != KindComplex {
		panic(fmt.Sprintf("value: not a complex: %s", v.kind))
	}
	return v.obj.(complex128)
}

// Reflect returns the underlying reflect.Value when this Value wraps one.
// ok is false otherwise.
func (v Value) Reflect() (reflect.Value, bool) {
	if r, ok := v.obj.(reflectValueProvider); ok {
		return r.ReflectValue(), true
	}
	rv, ok := v.obj.(reflect.Value)
	return rv, ok
}

// Interface returns the value as an any, preserving the original Go type
// recorded by Size. For example, a Value built from int8(5) returns
// int8(5), not int(5).
func (v Value) Interface() any {
	switch v.kind {
	case KindNil:
		return nil
	case KindBool:
		return v.num != 0
	case KindInt:
		switch v.size {
		case Size8:
			return int8(v.num)
		case Size16:
			return int16(v.num)
		case Size32:
			return int32(v.num)
		case Size64:
			return v.num
		default:
			return int(v.num)
		}
	case KindUint:
		switch v.size {
		case Size8:
			return uint8(v.num)
		case Size16:
			return uint16(v.num)
		case Size32:
			return uint32(v.num)
		case Size64:
			return uint64(v.num)
		default:
			return uint(v.num)
		}
	case KindFloat:
		f := math.Float64frombits(uint64(v.num))
		if v.size == Size32 {
			return float32(f)
		}
		return f
	case KindString:
		return v.obj.(string)
	case KindComplex:
		c := v.obj.(complex128)
		if v.size == Size32 {
			return complex64(complex(float32(real(c)), float32(imag(c))))
		}
		return c
	case KindReflect:
		if rv, ok := v.obj.(reflect.Value); ok {
			return rv.Interface()
		}
		return v.obj
	case KindFunc:
		if r, ok := v.obj.(reflectValueProvider); ok {
			return r.ReflectValue().Interface()
		}
		return v.obj
	default:
		// Pointer/slice/array/map/chan/func/struct/interface all keep
		// their payload in obj. If it is a reflect.Value, unwrap it; if
		// it is a native Go value (e.g. a function literal), return it
		// directly.
		if rv, ok := v.obj.(reflect.Value); ok {
			return rv.Interface()
		}
		return v.obj
	}
}

// GoString returns a debug representation. Implements fmt.GoStringer.
func (v Value) GoString() string {
	return fmt.Sprintf("value.Value{kind:%s, size:%d, num:%d, obj:%v}",
		v.kind, v.size, v.num, v.obj)
}

// --- Converter --------------------------------------------------------------

// TypeResolver maps go/types into the interpreter's runtime types. The
// interp package owns the concrete implementation; value depends on
// nothing but its interface.
type TypeResolver interface {
	ResolveType(t types.Type) (reflect.Type, error)
}

// Converter centralises every translation between Go-side any/reflect.Value
// and runtime Value. The default implementation (DefaultConverter) is
// stateless and safe for concurrent use.
type Converter interface {
	FromAny(v any) (Value, error)
	FromReflect(rv reflect.Value) (Value, error)
	ToAny(v Value) (any, error)
	ToReflect(v Value, t reflect.Type) (reflect.Value, error)
	Zero(t types.Type, resolver TypeResolver) (Value, error)
	Convert(v Value, t types.Type, resolver TypeResolver) (Value, error)
}

// DefaultConverter returns the stateless reference Converter.
func DefaultConverter() Converter { return defaultConverter{} }

type defaultConverter struct{}

// FromAny constructs a Value from any Go value. It uses a type switch
// for the common scalars (zero reflect cost) and falls back to
// reflect.ValueOf for everything else.
func (defaultConverter) FromAny(v any) (Value, error) {
	if v == nil {
		return MakeNil(), nil
	}
	switch x := v.(type) {
	case bool:
		return MakeBool(x), nil
	case int:
		return MakeInt(int64(x)), nil
	case int8:
		return MakeInt8(x), nil
	case int16:
		return MakeInt16(x), nil
	case int32:
		return MakeInt32(x), nil
	case int64:
		return MakeInt64(x), nil
	case uint:
		return MakeUint(uint64(x)), nil
	case uint8:
		return MakeUint8(x), nil
	case uint16:
		return MakeUint16(x), nil
	case uint32:
		return MakeUint32(x), nil
	case uint64:
		return MakeUint64(x), nil
	case uintptr:
		return MakeUint64(uint64(x)), nil
	case float32:
		return MakeFloat32(x), nil
	case float64:
		return MakeFloat(x), nil
	case complex64:
		return MakeComplex64(real(x), imag(x)), nil
	case complex128:
		return MakeComplex(real(x), imag(x)), nil
	case string:
		return MakeString(x), nil
	case Value:
		return x, nil
	case reflect.Value:
		return defaultConverter{}.FromReflect(x)
	}
	return defaultConverter{}.FromReflect(reflect.ValueOf(v))
}

// FromReflect mirrors FromAny but starts from a reflect.Value, avoiding
// the extra reflect.ValueOf hop on the common path.
//
// Named types whose underlying is a primitive (e.g. time.Duration =
// int64) are preserved in their reflect.Value form so method-set
// information survives. Plain primitives unbox into KindInt/KindFloat/
// etc. for fast scalar arithmetic.
func (defaultConverter) FromReflect(rv reflect.Value) (Value, error) {
	if !rv.IsValid() {
		return MakeNil(), nil
	}
	for rv.Kind() == reflect.Interface && !rv.IsNil() {
		rv = rv.Elem()
	}
	// Preserve named primitive types so methods stay reachable.
	if isNamedPrimitive(rv.Type()) {
		return makeReflect(rv), nil
	}
	switch rv.Kind() {
	case reflect.Bool:
		return MakeBool(rv.Bool()), nil
	case reflect.Int:
		return MakeInt(rv.Int()), nil
	case reflect.Int8:
		return MakeInt8(int8(rv.Int())), nil
	case reflect.Int16:
		return MakeInt16(int16(rv.Int())), nil
	case reflect.Int32:
		return MakeInt32(int32(rv.Int())), nil
	case reflect.Int64:
		return MakeInt64(rv.Int()), nil
	case reflect.Uint:
		return MakeUint(rv.Uint()), nil
	case reflect.Uint8:
		return MakeUint8(uint8(rv.Uint())), nil
	case reflect.Uint16:
		return MakeUint16(uint16(rv.Uint())), nil
	case reflect.Uint32:
		return MakeUint32(uint32(rv.Uint())), nil
	case reflect.Uint64, reflect.Uintptr:
		return MakeUint64(rv.Uint()), nil
	case reflect.Float32:
		return MakeFloat32(float32(rv.Float())), nil
	case reflect.Float64:
		return MakeFloat(rv.Float()), nil
	case reflect.Complex64:
		c := rv.Complex()
		return MakeComplex64(float32(real(c)), float32(imag(c))), nil
	case reflect.Complex128:
		c := rv.Complex()
		return MakeComplex(real(c), imag(c)), nil
	case reflect.String:
		return MakeString(rv.String()), nil
	}
	return makeReflect(rv), nil
}

// isNamedPrimitive reports whether rt is a named type whose underlying
// is a basic Go scalar (int, float, etc.). Such types — typified by
// time.Duration — must not be unboxed to KindInt et al. because they
// carry methods we still need to reach.
func isNamedPrimitive(rt reflect.Type) bool {
	if rt == nil {
		return false
	}
	if rt.Name() == "" {
		return false
	}
	switch rt.Kind() {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.Complex64, reflect.Complex128,
		reflect.String:
		// Builtins (int, string, etc.) have rt.Name()==their kind name
		// and rt.PkgPath()=="". Only count named types from packages.
		return rt.PkgPath() != ""
	}
	return false
}

// ToAny is the inverse of FromAny: it returns the Go value that the
// caller would have passed to FromAny to produce v. Equivalent to
// v.Interface() but routed through the Converter so embedders can
// override conversions if they want.
func (defaultConverter) ToAny(v Value) (any, error) {
	return v.Interface(), nil
}

// ToReflect produces a reflect.Value of the requested type from v. The
// caller is responsible for passing a target type that the value can
// actually fit (e.g. don't ask for int8 if v holds 1<<60).
func (c defaultConverter) ToReflect(v Value, typ reflect.Type) (reflect.Value, error) {
	switch v.kind {
	case KindNil:
		return reflect.Zero(typ), nil
	case KindBool:
		return convertOrBail(reflect.ValueOf(v.Bool()), typ)
	case KindInt:
		return convertOrBail(reflectIntOf(v), typ)
	case KindUint:
		return convertOrBail(reflectUintOf(v), typ)
	case KindFloat:
		return convertOrBail(reflectFloatOf(v), typ)
	case KindString:
		s := v.obj.(string)
		// Native []byte(string-literal) gives cap = len because the
		// compiler optimises the literal-conversion case. Through
		// reflect, []byte(s) returns the runtime allocator's size-class
		// capacity — which leaks through to user code (e.g.
		// bytes.Buffer.Cap()). Construct the destination slice with
		// MakeSlice(len, len) so cap matches len exactly.
		if typ != nil && typ.Kind() == reflect.Slice {
			switch typ.Elem().Kind() {
			case reflect.Uint8:
				out := reflect.MakeSlice(typ, len(s), len(s))
				reflect.Copy(out, reflect.ValueOf([]byte(s)))
				return out, nil
			case reflect.Int32:
				rs := []rune(s)
				out := reflect.MakeSlice(typ, len(rs), len(rs))
				reflect.Copy(out, reflect.ValueOf(rs))
				return out, nil
			}
		}
		return convertOrBail(reflect.ValueOf(s), typ)
	case KindComplex:
		return convertOrBail(reflect.ValueOf(v.obj.(complex128)), typ)
	case KindInterface:
		// Interface box: caller may want either the box itself
		// (when target is interface{} or the same interface type)
		// or the unwrapped dynamic value (when target is a different
		// interface or a concrete type).
		rv, ok := v.obj.(reflect.Value)
		if !ok || !rv.IsValid() {
			if typ != nil {
				return reflect.Zero(typ), nil
			}
			return reflect.Value{}, nil
		}
		if typ == nil || rv.Type() == typ {
			return rv, nil
		}
		dyn := rv
		if !rv.IsNil() {
			dyn = rv.Elem()
		}
		return convertOrBail(dyn, typ)
	case KindReflect:
		if rv, ok := v.obj.(reflect.Value); ok {
			return convertOrBail(rv, typ)
		}
	case KindFunc:
		if r, ok := v.obj.(reflectValueProvider); ok {
			return convertOrBail(r.ReflectValue(), typ)
		}
	}
	if rv, ok := v.obj.(reflect.Value); ok {
		return convertOrBail(rv, typ)
	}
	return reflect.ValueOf(v.obj), nil
}

// Zero returns the typed-zero value for t. If t is a basic type this is
// resolved without reflect; otherwise it goes through TypeResolver.
func (defaultConverter) Zero(t types.Type, r TypeResolver) (Value, error) {
	if b, ok := t.Underlying().(*types.Basic); ok {
		switch b.Kind() {
		case types.Bool, types.UntypedBool:
			return MakeBool(false), nil
		case types.Int, types.UntypedInt:
			return MakeInt(0), nil
		case types.Int8:
			return MakeInt8(0), nil
		case types.Int16:
			return MakeInt16(0), nil
		case types.Int32, types.UntypedRune:
			return MakeInt32(0), nil
		case types.Int64:
			return MakeInt64(0), nil
		case types.Uint:
			return MakeUint(0), nil
		case types.Uint8:
			return MakeUint8(0), nil
		case types.Uint16:
			return MakeUint16(0), nil
		case types.Uint32:
			return MakeUint32(0), nil
		case types.Uint64, types.Uintptr:
			return MakeUint64(0), nil
		case types.Float32:
			return MakeFloat32(0), nil
		case types.Float64, types.UntypedFloat:
			return MakeFloat(0), nil
		case types.Complex64:
			return MakeComplex64(0, 0), nil
		case types.Complex128, types.UntypedComplex:
			return MakeComplex(0, 0), nil
		case types.String, types.UntypedString:
			return MakeString(""), nil
		}
	}
	if r == nil {
		return Value{}, fmt.Errorf("value: cannot zero %s without TypeResolver", t)
	}
	rt, err := r.ResolveType(t)
	if err != nil {
		return Value{}, err
	}
	return makeReflect(reflect.New(rt).Elem()), nil
}

// Convert performs a Go type conversion (T(x)) on v. Numeric and string
// targets are handled inline; everything else routes through reflect.
func (c defaultConverter) Convert(v Value, t types.Type, r TypeResolver) (Value, error) {
	if r == nil {
		return Value{}, fmt.Errorf("value: Convert requires a TypeResolver")
	}
	rt, err := r.ResolveType(t)
	if err != nil {
		return Value{}, err
	}
	rv, err := c.ToReflect(v, rt)
	if err != nil {
		return Value{}, err
	}
	return c.FromReflect(rv)
}

// --- helpers ---------------------------------------------------------------

func reflectIntOf(v Value) reflect.Value {
	switch v.size {
	case Size8:
		return reflect.ValueOf(int8(v.num))
	case Size16:
		return reflect.ValueOf(int16(v.num))
	case Size32:
		return reflect.ValueOf(int32(v.num))
	case Size64:
		return reflect.ValueOf(v.num)
	default:
		return reflect.ValueOf(int(v.num))
	}
}

func reflectUintOf(v Value) reflect.Value {
	switch v.size {
	case Size8:
		return reflect.ValueOf(uint8(v.num))
	case Size16:
		return reflect.ValueOf(uint16(v.num))
	case Size32:
		return reflect.ValueOf(uint32(v.num))
	case Size64:
		return reflect.ValueOf(uint64(v.num))
	default:
		return reflect.ValueOf(uint(v.num))
	}
}

func reflectFloatOf(v Value) reflect.Value {
	f := math.Float64frombits(uint64(v.num))
	if v.size == Size32 {
		return reflect.ValueOf(float32(f))
	}
	return reflect.ValueOf(f)
}

func convertOrBail(rv reflect.Value, typ reflect.Type) (reflect.Value, error) {
	if typ == nil || rv.Type() == typ {
		return rv, nil
	}
	if rv.Type().AssignableTo(typ) {
		return rv, nil
	}
	if rv.Type().ConvertibleTo(typ) {
		return rv.Convert(typ), nil
	}
	return reflect.Value{},
		fmt.Errorf("value: cannot convert %s to %s", rv.Type(), typ)
}
