// composite.go contains the SSA opcodes that operate on composite
// values: structs, slices, arrays, maps, channels, and pointers.
//
// The storage model is:
//
//   - Scalar locals live as immutable value.Value with the appropriate Kind
//     stored in the frame slot (Cell.Value) for that ssa.Value.
//   - Composite locals are wrapped in a single-element addressable
//     reflect.Value (built by reflect.New(rt).Elem() and stored as
//     KindReflect). Field/IndexAddr/Slice operate on these reflect
//     values directly, which gives them addressability.
//   - Pointer SSA values that come from Alloc carry the cell's address
//     (reflect.Value of pointer kind via .Addr()) so UnOp(MUL) and
//     downstream Stores can dereference and mutate the original cell.
package interp

import (
	"fmt"
	"go/token"
	"reflect"
	"unicode/utf8"

	"go/types"

	"golang.org/x/tools/go/ssa"

	"github.com/t04dJ14n9/gig/value"
)

type addrRef struct {
	elem     reflect.Value
	intSlice []int
	index    int
}

func (fr *frame) setReflectAddrRef(v ssa.Value, elem reflect.Value) {
	if fr.addrRefs == nil {
		fr.addrRefs = make(map[ssa.Value]addrRef, 4)
	}
	fr.addrRefs[v] = addrRef{elem: elem}
}

func (fr *frame) setIntSliceAddrRef(v ssa.Value, s []int, idx int) {
	if fr.addrRefs == nil {
		fr.addrRefs = make(map[ssa.Value]addrRef, 4)
	}
	fr.addrRefs[v] = addrRef{intSlice: s, index: idx}
}

func (fr *frame) addrRef(v ssa.Value) (addrRef, bool) {
	if fr.addrRefs == nil {
		return addrRef{}, false
	}
	ref, ok := fr.addrRefs[v]
	return ref, ok
}

// reflectOf returns a reflect.Value for any value.Value, using
// instrType as the target reflect.Type when the conversion needs a
// hint. It is the workhorse for composite ops: it normalises every
// value.Value to a reflect.Value so we can call into Go's runtime.
//
// Interface-box values (built by MakeInterfaceBox) keep their
// interface form when the hint is the *same* interface type or no
// hint is given for an interface read. When the hint asks for a
// concrete or different-interface type, we expose the box's dynamic
// value so reflect can convert/dispatch.
func (p *program) reflectOf(v value.Value, hint reflect.Type) (reflect.Value, error) {
	if rv, ok := v.InterfaceBox(); ok {
		// Same interface type or no hint at all: hand back the box.
		if hint == nil || hint == rv.Type() {
			return rv, nil
		}
		// Different host interface (e.g. io.Writer): expose the
		// dynamic value so the host can do its own type checks.
		// reflect.Value.Set with an interface-typed slot will
		// re-box correctly.
		dyn := rv
		if dyn.IsValid() && !dyn.IsNil() {
			dyn = dyn.Elem()
		}
		if hint.Kind() == reflect.Interface {
			// Caller wants ANY interface — return the dynamic value
			// and let reflect.Set into the interface slot box it.
			return dyn, nil
		}
		if dyn.IsValid() && dyn.Type() != hint && dyn.Type().ConvertibleTo(hint) {
			return dyn.Convert(hint), nil
		}
		return dyn, nil
	}
	if rv, ok := v.Reflect(); ok {
		if hint != nil && rv.Type() != hint && rv.Type().ConvertibleTo(hint) {
			return rv.Convert(hint), nil
		}
		return rv, nil
	}
	rv, err := p.converter.ToReflect(v, hint)
	if err != nil {
		return reflect.Value{}, err
	}
	return rv, nil
}

// reflectValue wraps an addressable reflect.Value as a Value.
func reflectValue(rv reflect.Value) value.Value {
	conv := value.DefaultConverter()
	v, _ := conv.FromReflect(rv)
	return v
}

// makeAddressable allocates a fresh addressable reflect.Value of the
// given type, initialised to its zero. This is the canonical "I need
// somewhere to Store into" cell.
func (p *program) makeAddressable(t types.Type) (reflect.Value, error) {
	rt, err := p.resolver.ResolveType(t)
	if err != nil {
		return reflect.Value{}, err
	}
	return reflect.New(rt).Elem(), nil
}

// --- runners ----------------------------------------------------------------

func (p *program) runMakeInterface(fr *frame, instr *ssa.MakeInterface) (continuation, []value.Value, error) {
	x, err := p.readValue(fr, instr.X)
	if err != nil {
		return contNext, nil, err
	}
	// MakeInterface boxes a typed value into an interface. The
	// resulting interface is non-nil even when the boxed value is a
	// typed nil (Go's canonical typed-nil-in-interface gotcha). We
	// build an interface-typed reflect.Value and tag it KindInterface
	// so downstream IsNil()/equality treat it as Go would.
	ifaceRT, err := p.resolver.ResolveType(instr.Type())
	if err != nil || ifaceRT.Kind() != reflect.Interface {
		fr.setCell(instr, x)
		return contNext, nil, nil //nolint:nilerr // Missing host interface metadata falls back to the original value.
	}
	// Resolve the source's static type and use it as a hint so that
	// named primitives (e.g. MyInt5 under the hood = int) keep their
	// reflect.Type identity inside the interface — needed for method
	// dispatch through the iface to find the right receiver type.
	innerHint, _ := p.resolver.ResolveType(instr.X.Type())
	innerRV, err := p.reflectOf(x, innerHint)
	if err != nil {
		return contNext, nil, err
	}
	if innerHint != nil && innerRV.IsValid() && innerRV.Type() != innerHint && innerRV.Type().ConvertibleTo(innerHint) {
		innerRV = innerRV.Convert(innerHint)
	}
	holder := reflect.New(ifaceRT).Elem()
	boxed := false
	if innerRV.IsValid() {
		if innerRV.Type().AssignableTo(ifaceRT) {
			holder.Set(innerRV)
			boxed = true
		} else if innerRV.Type().ConvertibleTo(ifaceRT) {
			holder.Set(innerRV.Convert(ifaceRT))
			boxed = true
		}
	}
	if !boxed && p.interpretedValueImplementsInterface(x, instr.Type()) {
		anyHolder := reflect.New(reflect.TypeOf((*any)(nil)).Elem()).Elem()
		if innerRV.IsValid() {
			anyHolder.Set(innerRV)
		}
		fr.setCell(instr, value.MakeInterfaceBox(anyHolder))
		return contNext, nil, nil
	}
	fr.setCell(instr, value.MakeInterfaceBox(holder))
	return contNext, nil, nil
}

func (p *program) runField(fr *frame, instr *ssa.Field) (continuation, []value.Value, error) {
	x, err := p.readValue(fr, instr.X)
	if err != nil {
		return contNext, nil, err
	}
	rv, err := p.reflectOf(x, nil)
	if err != nil {
		return contNext, nil, err
	}
	// Field semantics: SSA's Field operates on a struct value. In
	// practice the value can arrive boxed in an interface (after
	// MakeInterface) or wrapped in a pointer (when SSA elides an
	// explicit Load); unwrap both so we land on a struct.
	for rv.Kind() == reflect.Interface || rv.Kind() == reflect.Ptr {
		if rv.Kind() == reflect.Ptr && rv.IsNil() {
			panic("runtime error: invalid memory address or nil pointer dereference")
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return contNext, nil, fmt.Errorf("interp: Field on non-struct kind %s", rv.Kind())
	}
	if instr.Field >= rv.NumField() {
		return contNext, nil, fmt.Errorf("interp: Field %d out of range for %s (%d fields)",
			instr.Field, rv.Type(), rv.NumField())
	}
	fld := rv.Field(instr.Field)
	out, err := p.converter.FromReflect(fld)
	if err != nil {
		return contNext, nil, err
	}
	fr.setCell(instr, out)
	return contNext, nil, nil
}

func (p *program) runFieldAddr(fr *frame, instr *ssa.FieldAddr) (continuation, []value.Value, error) {
	x, err := p.readValue(fr, instr.X)
	if err != nil {
		return contNext, nil, err
	}
	rv, err := p.reflectOf(x, nil)
	if err != nil {
		return contNext, nil, err
	}
	// FieldAddr semantics: x is *T (one level of indirection); the
	// result is the addressable field of the pointee. Deref exactly
	// once. If the value is wrapped in an interface, unwrap that
	// first; then deref the pointer.
	if rv.Kind() == reflect.Interface {
		rv = rv.Elem()
	}
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			panic("runtime error: invalid memory address or nil pointer dereference")
		}
		rv = rv.Elem()
	}
	if !rv.CanAddr() {
		// We need an addressable copy — make one and copy the struct in.
		holder := reflect.New(rv.Type()).Elem()
		holder.Set(rv)
		rv = holder
	}
	addr := rv.Field(instr.Field).Addr()
	fr.setCell(instr, reflectValue(addr))
	return contNext, nil, nil
}

func (p *program) runIndexAddr(fr *frame, instr *ssa.IndexAddr) (continuation, []value.Value, error) {
	x, err := p.readValue(fr, instr.X)
	if err != nil {
		return contNext, nil, err
	}
	idxV, err := p.readValue(fr, instr.Index)
	if err != nil {
		return contNext, nil, err
	}
	idx := int(idxV.Int())
	if s, ok := x.IntSlice(); ok {
		if indexAddrRefEligible(instr) {
			fr.setIntSliceAddrRef(instr, s, idx)
			fr.setCell(instr, value.MakeNil())
			return contNext, nil, nil
		}
		// Fall through to the generic reflect-pointer materialization
		// only when the address escapes beyond Store/Load consumers.
	}
	rv, err := p.reflectOf(x, nil)
	if err != nil {
		return contNext, nil, err
	}
	// IndexAddr semantics: x is *array or slice. For pointer-to-array,
	// deref once; slices are already indexable directly. Same
	// interface-unwrap discipline as FieldAddr.
	if rv.Kind() == reflect.Interface {
		rv = rv.Elem()
	}
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			panic("runtime error: invalid memory address or nil pointer dereference")
		}
		rv = rv.Elem()
	}
	if rv.Kind() == reflect.Array && !rv.CanAddr() {
		holder := reflect.New(rv.Type()).Elem()
		holder.Set(rv)
		rv = holder
	}
	elem := rv.Index(idx)
	if indexAddrRefEligible(instr) {
		fr.setReflectAddrRef(instr, elem)
		fr.setCell(instr, value.MakeNil())
		return contNext, nil, nil
	}
	fr.setCell(instr, reflectValue(elem.Addr()))
	return contNext, nil, nil
}

func indexAddrRefEligible(v ssa.Value) bool {
	refs := v.Referrers()
	if refs == nil || len(*refs) == 0 {
		return false
	}
	for _, ref := range *refs {
		switch instr := ref.(type) {
		case *ssa.Store:
			if instr.Addr != v {
				return false
			}
		case *ssa.UnOp:
			if instr.Op != token.MUL || instr.X != v {
				return false
			}
		case *ssa.DebugRef:
			// Diagnostic-only reference; it does not need a materialized pointer.
		default:
			return false
		}
	}
	return true
}

func isPlainIntSliceType(t types.Type) bool {
	s, ok := t.Underlying().(*types.Slice)
	if !ok {
		return false
	}
	b, ok := s.Elem().Underlying().(*types.Basic)
	return ok && b.Kind() == types.Int
}

func (p *program) runIndex(fr *frame, instr *ssa.Index) (continuation, []value.Value, error) {
	x, err := p.readValue(fr, instr.X)
	if err != nil {
		return contNext, nil, err
	}
	idxV, err := p.readValue(fr, instr.Index)
	if err != nil {
		return contNext, nil, err
	}
	idx := int(idxV.Int())
	if s, ok := x.IntSlice(); ok {
		fr.setCell(instr, value.MakeInt(int64(s[idx])))
		return contNext, nil, nil
	}
	rv, err := p.reflectOf(x, nil)
	if err != nil {
		return contNext, nil, err
	}
	for rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface {
		rv = rv.Elem()
	}
	out, err := p.converter.FromReflect(rv.Index(idx))
	if err != nil {
		return contNext, nil, err
	}
	fr.setCell(instr, out)
	return contNext, nil, nil
}

func (p *program) runSlice(fr *frame, instr *ssa.Slice) (continuation, []value.Value, error) {
	x, err := p.readValue(fr, instr.X)
	if err != nil {
		return contNext, nil, err
	}
	rv, err := p.reflectOf(x, nil)
	if err != nil {
		return contNext, nil, err
	}
	for rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface {
		rv = rv.Elem()
	}
	low, high, max := 0, rv.Len(), -1
	if instr.Low != nil {
		v, err := p.readValue(fr, instr.Low)
		if err != nil {
			return contNext, nil, err
		}
		low = int(v.Int())
	}
	if instr.High != nil {
		v, err := p.readValue(fr, instr.High)
		if err != nil {
			return contNext, nil, err
		}
		high = int(v.Int())
	}
	if instr.Max != nil {
		v, err := p.readValue(fr, instr.Max)
		if err != nil {
			return contNext, nil, err
		}
		max = int(v.Int())
	}
	var sliced reflect.Value
	if max >= 0 {
		sliced = rv.Slice3(low, high, max)
	} else {
		sliced = rv.Slice(low, high)
	}
	if s, ok := reflectIntSlice(sliced); ok {
		fr.setCell(instr, value.MakeIntSlice(s))
		return contNext, nil, nil
	}
	out, err := p.converter.FromReflect(sliced)
	if err != nil {
		return contNext, nil, err
	}
	fr.setCell(instr, out)
	return contNext, nil, nil
}

func (p *program) runLookup(fr *frame, instr *ssa.Lookup) (continuation, []value.Value, error) {
	x, err := p.readValue(fr, instr.X)
	if err != nil {
		return contNext, nil, err
	}
	keyV, err := p.readValue(fr, instr.Index)
	if err != nil {
		return contNext, nil, err
	}
	rv, err := p.reflectOf(x, nil)
	if err != nil {
		return contNext, nil, err
	}
	for rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface {
		rv = rv.Elem()
	}
	if rv.Kind() == reflect.String {
		// Lookup on string returns the byte at index.
		idx := int(keyV.Int())
		fr.setCell(instr, value.MakeUint8(rv.String()[idx]))
		return contNext, nil, nil
	}
	if rv.Kind() != reflect.Map {
		return contNext, nil, fmt.Errorf("interp: Lookup on non-map kind %s", rv.Kind())
	}
	keyRT := rv.Type().Key()
	keyRV, err := p.reflectOf(keyV, keyRT)
	if err != nil {
		return contNext, nil, err
	}
	got := rv.MapIndex(keyRV)
	ok := got.IsValid()
	if !ok {
		got = reflect.Zero(rv.Type().Elem())
	}
	gotV, err := p.converter.FromReflect(got)
	if err != nil {
		return contNext, nil, err
	}
	if instr.CommaOk {
		// Tuple result: pack as a synthetic struct so Extract reads it.
		tt := instr.Type().(*types.Tuple)
		rt, err := p.resolver.ResolveType(tt)
		if err != nil {
			return contNext, nil, err
		}
		holder := reflect.New(rt).Elem()
		holder.Field(0).Set(got)
		holder.Field(1).SetBool(ok)
		fr.setCell(instr, reflectValue(holder))
	} else {
		fr.setCell(instr, gotV)
	}
	return contNext, nil, nil
}

func (p *program) runMapUpdate(fr *frame, instr *ssa.MapUpdate) (continuation, []value.Value, error) {
	mV, err := p.readValue(fr, instr.Map)
	if err != nil {
		return contNext, nil, err
	}
	kV, err := p.readValue(fr, instr.Key)
	if err != nil {
		return contNext, nil, err
	}
	vV, err := p.readValue(fr, instr.Value)
	if err != nil {
		return contNext, nil, err
	}
	rv, err := p.reflectOf(mV, nil)
	if err != nil {
		return contNext, nil, err
	}
	for rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface {
		rv = rv.Elem()
	}
	keyRV, err := p.reflectOf(kV, rv.Type().Key())
	if err != nil {
		return contNext, nil, err
	}
	valRV, err := p.reflectOf(vV, rv.Type().Elem())
	if err != nil {
		return contNext, nil, err
	}
	rv.SetMapIndex(keyRV, valRV)
	return contNext, nil, nil
}

func (p *program) runMakeSlice(fr *frame, instr *ssa.MakeSlice) (continuation, []value.Value, error) {
	lenV, err := p.readValue(fr, instr.Len)
	if err != nil {
		return contNext, nil, err
	}
	capV, err := p.readValue(fr, instr.Cap)
	if err != nil {
		return contNext, nil, err
	}
	if isPlainIntSliceType(instr.Type()) {
		fr.setCell(instr, value.MakeIntSlice(make([]int, int(lenV.Int()), int(capV.Int()))))
		return contNext, nil, nil
	}
	rt, err := p.resolver.ResolveType(instr.Type())
	if err != nil {
		return contNext, nil, err
	}
	out := reflect.MakeSlice(rt, int(lenV.Int()), int(capV.Int()))
	v, err := p.converter.FromReflect(out)
	if err != nil {
		return contNext, nil, err
	}
	fr.setCell(instr, v)
	return contNext, nil, nil
}

func (p *program) runMakeMap(fr *frame, instr *ssa.MakeMap) (continuation, []value.Value, error) {
	rt, err := p.resolver.ResolveType(instr.Type())
	if err != nil {
		return contNext, nil, err
	}
	size := 0
	if instr.Reserve != nil {
		v, err := p.readValue(fr, instr.Reserve)
		if err != nil {
			return contNext, nil, err
		}
		size = int(v.Int())
	}
	out := reflect.MakeMapWithSize(rt, size)
	v, err := p.converter.FromReflect(out)
	if err != nil {
		return contNext, nil, err
	}
	fr.setCell(instr, v)
	return contNext, nil, nil
}

func (p *program) runMakeChan(fr *frame, instr *ssa.MakeChan) (continuation, []value.Value, error) {
	rt, err := p.resolver.ResolveType(instr.Type())
	if err != nil {
		return contNext, nil, err
	}
	sizeV, err := p.readValue(fr, instr.Size)
	if err != nil {
		return contNext, nil, err
	}
	out := reflect.MakeChan(rt, int(sizeV.Int()))
	v, err := p.converter.FromReflect(out)
	if err != nil {
		return contNext, nil, err
	}
	fr.setCell(instr, v)
	return contNext, nil, nil
}

// rangeIter is the iterator state captured by ssa.Range.
type rangeIter struct {
	kind reflect.Kind
	// For maps: the materialised key list and current index.
	mapKeys []reflect.Value
	mapVal  reflect.Value
	index   int
	// For strings: the underlying string and rune-decode position.
	str    string
	strPos int
}

func (p *program) runRange(fr *frame, instr *ssa.Range) (continuation, []value.Value, error) {
	x, err := p.readValue(fr, instr.X)
	if err != nil {
		return contNext, nil, err
	}
	rv, err := p.reflectOf(x, nil)
	if err != nil {
		return contNext, nil, err
	}
	for rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface {
		rv = rv.Elem()
	}
	it := &rangeIter{kind: rv.Kind()}
	switch rv.Kind() {
	case reflect.Map:
		it.mapKeys = rv.MapKeys()
		it.mapVal = rv
	case reflect.String:
		it.str = rv.String()
	default:
		return contNext, nil, fmt.Errorf("interp: Range over %s not supported", rv.Kind())
	}
	// Stash the iterator in a side-channel keyed by ssa.Value so Next can find
	// it (the Range value itself is never read). Simpler than packaging it
	// inside value.Value.
	if fr.iters == nil {
		fr.iters = make(map[ssa.Value]*rangeIter)
	}
	fr.iters[instr] = it
	return contNext, nil, nil
}

func (p *program) runNext(fr *frame, instr *ssa.Next) (continuation, []value.Value, error) {
	it, ok := fr.iters[instr.Iter]
	if !ok {
		return contNext, nil, fmt.Errorf("interp: Next without prior Range")
	}
	tt, ok := instr.Type().(*types.Tuple)
	if !ok {
		return contNext, nil, fmt.Errorf("interp: Next type is not tuple: %s", instr.Type())
	}
	rt, err := p.resolver.ResolveType(tt)
	if err != nil {
		return contNext, nil, err
	}
	holder := reflect.New(rt).Elem()
	setField := func(idx int, value any) {
		f := holder.Field(idx)
		rv := reflect.ValueOf(value)
		if f.Kind() == reflect.Interface {
			f.Set(rv)
		} else if rv.Type() != f.Type() && rv.Type().ConvertibleTo(f.Type()) {
			f.Set(rv.Convert(f.Type()))
		} else {
			f.Set(rv)
		}
	}
	switch it.kind {
	case reflect.Map:
		if it.index < len(it.mapKeys) {
			k := it.mapKeys[it.index]
			v := it.mapVal.MapIndex(k)
			holder.Field(0).SetBool(true)
			setField(1, k.Interface())
			setField(2, v.Interface())
			it.index++
		} else {
			holder.Field(0).SetBool(false)
		}
	case reflect.String:
		if it.strPos < len(it.str) {
			r, size := utf8.DecodeRuneInString(it.str[it.strPos:])
			holder.Field(0).SetBool(true)
			setField(1, it.strPos)
			setField(2, r)
			it.strPos += size
		} else {
			holder.Field(0).SetBool(false)
		}
	}
	fr.setCell(instr, reflectValue(holder))
	return contNext, nil, nil
}

func (p *program) runExtract(fr *frame, instr *ssa.Extract) (continuation, []value.Value, error) {
	tup, err := p.readValue(fr, instr.Tuple)
	if err != nil {
		return contNext, nil, err
	}
	rv, err := p.reflectOf(tup, nil)
	if err != nil {
		return contNext, nil, err
	}
	for rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return contNext, nil, fmt.Errorf("interp: Extract on non-tuple kind %s", rv.Kind())
	}
	out, err := p.converter.FromReflect(rv.Field(instr.Index))
	if err != nil {
		return contNext, nil, err
	}
	fr.setCell(instr, out)
	return contNext, nil, nil
}
