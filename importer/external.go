// external.go implements ExternalPackage methods for adding functions,
// variables, constants, and types to a registered host package.
package importer

import (
	"reflect"

	"github.com/t04dJ14n9/gig/model/external"
	"github.com/t04dJ14n9/gig/value"
)

// AddFunction adds a function to the package.
func (p *ExternalPackage) AddFunction(name string, fn any, doc string, directCall ...func([]value.Value) ([]value.Value, error)) {
	sig := funcSignature(fn)
	if p.registry != nil {
		registerExternalReflectTypes(p.registry, reflect.TypeOf(fn), map[reflect.Type]bool{})
	}
	var dc func([]value.Value) ([]value.Value, error)
	if len(directCall) > 0 {
		dc = directCall[0]
	}
	p.Objects[name] = &external.ExternalObject{
		Name:       name,
		Kind:       external.ObjectKindFunction,
		Value:      fn,
		Type:       sig,
		Doc:        doc,
		DirectCall: dc,
	}
}

// AddVariable adds a variable to the package.
func (p *ExternalPackage) AddVariable(name string, ptr any, doc string) {
	typ := typeOf(reflect.TypeOf(ptr).Elem())
	p.Objects[name] = &external.ExternalObject{
		Name:  name,
		Kind:  external.ObjectKindVariable,
		Value: ptr,
		Type:  typ,
		Doc:   doc,
	}
}

// AddConstant adds a constant to the package.
func (p *ExternalPackage) AddConstant(name string, val any, doc string) {
	typ := typeOf(reflect.TypeOf(val))
	p.Objects[name] = &external.ExternalObject{
		Name:  name,
		Kind:  external.ObjectKindConstant,
		Value: val,
		Type:  typ,
		Doc:   doc,
	}
}

// AddType adds a named type to the package.
func (p *ExternalPackage) AddType(name string, typ reflect.Type, doc string) {
	if typ == nil {
		return
	}
	p.Types[name] = typ
	p.Objects[name] = &external.ExternalObject{
		Name:  name,
		Kind:  external.ObjectKindType,
		Value: reflect.Zero(typ).Interface(),
		Type:  typeOf(typ),
		Doc:   doc,
	}
}

// AddMethodDirectCall registers a zero-reflection wrapper for a method on a
// named type in this package. typeName is the exported type name without the
// package path, for example "Reader" for strings.Reader.
func (p *ExternalPackage) AddMethodDirectCall(typeName, methodName string, dc func(value.Value, []value.Value) value.Value) {
	if p.registry == nil {
		return
	}
	p.registry.AddMethodDirectCall(p.Path+"."+typeName, methodName, dc)
}

func registerExternalReflectTypes(reg PackageRegistry, rt reflect.Type, seen map[reflect.Type]bool) {
	if reg == nil || rt == nil {
		return
	}
	if seen[rt] {
		return
	}
	seen[rt] = true

	reg.SetExternalType(typeOf(rt), rt)

	switch rt.Kind() {
	case reflect.Array, reflect.Chan, reflect.Ptr, reflect.Slice:
		registerExternalReflectTypes(reg, rt.Elem(), seen)
	case reflect.Func:
		for i := 0; i < rt.NumIn(); i++ {
			registerExternalReflectTypes(reg, rt.In(i), seen)
		}
		for i := 0; i < rt.NumOut(); i++ {
			registerExternalReflectTypes(reg, rt.Out(i), seen)
		}
	case reflect.Interface:
		for i := 0; i < rt.NumMethod(); i++ {
			registerExternalReflectTypes(reg, rt.Method(i).Type, seen)
		}
	case reflect.Map:
		registerExternalReflectTypes(reg, rt.Key(), seen)
		registerExternalReflectTypes(reg, rt.Elem(), seen)
	case reflect.Struct:
		for i := 0; i < rt.NumField(); i++ {
			registerExternalReflectTypes(reg, rt.Field(i).Type, seen)
		}
	}
}
