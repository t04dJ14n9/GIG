// Package host defines the explicit external Go environment used by the
// frontend and interpreter. FromRegistry exposes registered packages through
// this boundary.
package host

import (
	"go/types"
	"reflect"

	"github.com/t04dJ14n9/gig/value"
)

// Environment is the explicit host boundary for exposing Go symbols to
// interpreted code. It composes types.Importer so the frontend can run
// go/types against it directly.
type Environment interface {
	types.Importer

	// AutoImport returns the Import descriptor for an unqualified
	// identifier (e.g. "fmt") so the frontend can splice an implicit
	// import line into the parsed AST. ok=false means "let the user
	// import explicitly".
	AutoImport(name string) (Import, bool)

	LookupFunc(pkgPath, name string) (Function, bool)
	LookupVar(pkgPath, name string) (Variable, bool)
	LookupConst(pkgPath, name string) (Constant, bool)
	LookupType(pkgPath, name string) (Type, bool)
	LookupReflectType(t types.Type) (reflect.Type, bool)
	LookupMethod(typeName, methodName string) (Method, bool)
	LookupInterfaceProxy(iface *types.Interface) (InterfaceProxy, bool)
}

// Import is a minimal descriptor for an importable package. The
// frontend uses Path; everything else is informational.
type Import struct {
	Path string
	Name string
}

// Function is a host-provided callable. Call takes interpreter-side
// values and returns interpreter-side values; reflect-based dispatch is
// hidden behind this interface so the interp package never sees
// reflect.Call directly.
type Function interface {
	Name() string
	Signature() *types.Signature
	Call(args []value.Value) ([]value.Value, error)
}

// DirectFunction is an optional fast path for host functions that can
// run without reflect.Call.
type DirectFunction interface {
	Function
	CallDirect(args []value.Value) ([]value.Value, bool, error)
}

// Variable is a host-provided readable/writable storage slot.
type Variable interface {
	Name() string
	Type() types.Type
	Get() (value.Value, error)
	Set(v value.Value) error
}

// Constant is a host-provided read-only value.
type Constant interface {
	Name() string
	Type() types.Type
	Value() value.Value
}

// Type is a host-provided named type.
type Type interface {
	Name() string
	GoType() types.Type
	ReflectType() reflect.Type
}

// Method is a host-provided method bound to a named type.
type Method interface {
	Name() string
	Receiver() types.Type
	Signature() *types.Signature
	Call(recv value.Value, args []value.Value) ([]value.Value, error)
}

// DirectMethod is an optional fast path for host methods that can run
// without reflect.MethodByName and return exactly one value.
type DirectMethod interface {
	Method
	CallDirect(recv value.Value, args []value.Value) (value.Value, bool, error)
}

// InterfaceProxy lets interpreted code satisfy a host-defined Go
// interface. The interp package builds a reflect.Value backed by Wrap
// when an interpreted struct flows into a host call.
type InterfaceProxy interface {
	Interface() *types.Interface
	Wrap(impl value.Value) (reflect.Value, error)
}
