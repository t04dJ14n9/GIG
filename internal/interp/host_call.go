// host_call.go bridges interpreter calls to host.Environment-provided
// functions, vars, and methods. When SSA emits a Call to a *ssa.Function
// with no body, that function was registered through the host importer;
// we look it up via host.Environment.LookupFunc and dispatch through
// the host.Function interface.
package interp

import (
	"context"
	"fmt"
	"go/types"
	"reflect"

	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"

	"github.com/t04dJ14n9/gig/host"
	"github.com/t04dJ14n9/gig/value"
)

// hostPkgPath returns the import path of the package that declares fn,
// preferring the SSA package and falling back to the type-checker object.
func hostPkgPath(fn *ssa.Function) string {
	if fn.Pkg != nil && fn.Pkg.Pkg != nil {
		return fn.Pkg.Pkg.Path()
	}
	if obj := fn.Object(); obj != nil && obj.Pkg() != nil {
		return obj.Pkg().Path()
	}
	return ""
}

// callHostFunc dispatches a body-less *ssa.Function to the host
// environment. The function name and package path come from the SSA
// node; the host bridge resolves them to a host.Function. Generated
// wrappers may run through host.DirectFunction; all other functions
// fall back to reflect-backed host.Function.Call.
func (p *program) callHostFunc(ctx context.Context, fn *ssa.Function, args []value.Value) ([]value.Value, error) {
	if p.env == nil {
		return nil, fmt.Errorf("interp: %s: no host.Environment registered", fn.Name())
	}
	pkgPath := hostPkgPath(fn)
	// Method on a host type — dispatch through reflect.MethodByName on
	// the receiver. SSA emits these with the receiver as args[0].
	if fn.Signature.Recv() != nil && len(args) > 0 {
		return p.invokeMethodOn(ctx, args[0], fn.Name(), args[1:])
	}
	// Free function.
	hf, ok := p.lookupHostFunc(fn, pkgPath)
	if !ok {
		// Fall back to reflect method dispatch — works for packages
		// like bytes/list whose methods register through the legacy
		// MethodDirectCall path that LookupFunc doesn't see.
		if len(args) > 0 {
			if results, err := p.invokeMethodOn(ctx, args[0], fn.Name(), args[1:]); err == nil {
				return results, nil
			}
		}
		return nil, fmt.Errorf("interp: host function %s.%s not found", pkgPath, fn.Name())
	}
	return hf.Call(args)
}

func (p *program) callHostFuncDirect(fn *ssa.Function, args []value.Value) ([]value.Value, bool, error) {
	if p.env == nil {
		return nil, false, fmt.Errorf("interp: %s: no host.Environment registered", fn.Name())
	}
	pkgPath := hostPkgPath(fn)
	if fn.Signature.Recv() != nil && len(args) > 0 {
		return p.invokeMethodOnDirectResult(args[0], fn.Name(), args[1:])
	}
	hf, ok := p.lookupHostFunc(fn, pkgPath)
	if !ok {
		if len(args) > 0 {
			return p.invokeMethodOnDirectResult(args[0], fn.Name(), args[1:])
		}
		return nil, false, nil
	}
	df, ok := hf.(host.DirectFunction)
	if !ok {
		return nil, false, nil
	}
	return df.CallDirect(args)
}

func (p *program) lookupHostFunc(fn *ssa.Function, pkgPath string) (host.Function, bool) {
	if cached, hit := p.hostFuncs.Load(fn); hit {
		if hf, ok := cached.(host.Function); ok {
			return hf, true
		}
		return nil, false
	}
	hf, ok := p.env.LookupFunc(pkgPath, fn.Name())
	if ok {
		p.hostFuncs.Store(fn, hf)
		return hf, true
	}
	return nil, false
}

// invokeMethodOn calls receiver.method(args), trying first the
// interpreted SSA package (for methods on user-defined types) and then
// reflect.MethodByName on the host receiver.
func (p *program) invokeMethodOn(ctx context.Context, receiver value.Value, method string, args []value.Value) ([]value.Value, error) {
	// Methods declared on interpreted types live as SSA functions on
	// the package, named like "(*AdderStruct).Add" or "AdderStruct.Add".
	// When the receiver arrived through a MakeInterface box we unwrap
	// it to its dynamic concrete value so the SSA method body sees the
	// receiver in its declared form (MyInt5, *AdderStruct, etc.) rather
	// than as an interface.
	dynRecv, rv, err := p.hostReceiverReflect(receiver)
	if err != nil {
		return nil, err
	}
	if hm, ok := p.lookupHostMethod(rv, method); ok {
		return hm.Call(dynRecv, args)
	}
	if fn := p.lookupInterpretedMethod(dynRecv, method); fn != nil {
		// Go's spec lets a *T receiver call a value-receiver method
		// (and vice versa). The interpreter's frame initialises the
		// receiver param with whatever value we pass in, so the value's
		// shape must match the SSA-declared receiver type or downstream
		// Field/Store ops will see a kind mismatch.
		recv := p.adjustReceiverShape(dynRecv, fn)
		all := append([]value.Value{recv}, args...)
		return p.callSSA(ctx, nil, fn, all, nil, 0)
	}
	conv := value.DefaultConverter()
	m := rv.MethodByName(method)
	if !m.IsValid() {
		// Try addressable (pointer) receiver — Go auto-takes the
		// address for method values.
		if rv.Kind() != reflect.Ptr && rv.CanAddr() {
			m = rv.Addr().MethodByName(method)
		}
		if !m.IsValid() && rv.Kind() == reflect.Ptr && !rv.IsNil() {
			// Receiver might be **T; try one deref.
			m = rv.Elem().MethodByName(method)
		}
	}
	if !m.IsValid() {
		return nil, fmt.Errorf("interp: method %s not found on %s", method, rv.Type())
	}
	mt := m.Type()
	rargs := make([]reflect.Value, len(args))
	for i, a := range args {
		var target reflect.Type
		if i < mt.NumIn() {
			target = mt.In(i)
		}
		ra, err := conv.ToReflect(a, target)
		if err != nil {
			return nil, fmt.Errorf("interp: method %s arg %d: %w", method, i, err)
		}
		rargs[i] = ra
	}
	rresults := m.Call(rargs)
	out := make([]value.Value, len(rresults))
	for i, r := range rresults {
		v, err := conv.FromReflect(r)
		if err != nil {
			return nil, fmt.Errorf("interp: method %s result %d: %w", method, i, err)
		}
		out[i] = v
	}
	return out, nil
}

func (p *program) invokeMethodOnDirect(receiver value.Value, method string, args []value.Value) (value.Value, bool, error) {
	dynRecv, rv, err := p.hostReceiverReflect(receiver)
	if err != nil {
		return value.Value{}, false, err
	}
	hm, ok := p.lookupHostMethod(rv, method)
	if !ok {
		return value.Value{}, false, nil
	}
	dm, ok := hm.(host.DirectMethod)
	if !ok {
		return value.Value{}, false, nil
	}
	return dm.CallDirect(dynRecv, args)
}

func (p *program) invokeMethodOnDirectResult(receiver value.Value, method string, args []value.Value) ([]value.Value, bool, error) {
	result, ok, err := p.invokeMethodOnDirect(receiver, method, args)
	if err != nil || !ok {
		return nil, ok, err
	}
	return []value.Value{result}, true, nil
}

func (p *program) hostReceiverReflect(receiver value.Value) (value.Value, reflect.Value, error) {
	dynRecv := receiver
	if rv, ok := receiver.InterfaceBox(); ok && rv.IsValid() && !rv.IsNil() {
		conv := value.DefaultConverter()
		if uv, err := conv.FromReflect(rv.Elem()); err == nil {
			dynRecv = uv
		}
	}
	rv, err := p.reflectOf(dynRecv, nil)
	return dynRecv, rv, err
}

type hostMethodCacheKey struct {
	typ    reflect.Type
	method string
}

type missingHostMethod struct{}

func (p *program) lookupHostMethod(rv reflect.Value, method string) (host.Method, bool) {
	if p.env == nil || !rv.IsValid() {
		return nil, false
	}
	for rv.Kind() == reflect.Interface && !rv.IsNil() {
		rv = rv.Elem()
	}
	key := hostMethodCacheKey{typ: rv.Type(), method: method}
	if cached, hit := p.hostMethods.Load(key); hit {
		hm, ok := cached.(host.Method)
		return hm, ok
	}
	hm, ok := p.resolveHostMethod(rv.Type(), method)
	if ok {
		p.hostMethods.Store(key, hm)
		return hm, true
	}
	p.hostMethods.Store(key, missingHostMethod{})
	return nil, false
}

func (p *program) resolveHostMethod(rt reflect.Type, method string) (host.Method, bool) {
	if hm, ok := p.lookupHostMethodType(rt, method); ok {
		return hm, true
	}
	if rt.Kind() == reflect.Ptr {
		return p.lookupHostMethodType(rt.Elem(), method)
	}
	if rt.Name() == "" || rt.PkgPath() == "" {
		return nil, false
	}
	return p.env.LookupMethod("*"+rt.PkgPath()+"."+rt.Name(), method)
}

func (p *program) lookupHostMethodType(rt reflect.Type, method string) (host.Method, bool) {
	if rt == nil || rt.Name() == "" || rt.PkgPath() == "" {
		return nil, false
	}
	return p.env.LookupMethod(rt.PkgPath()+"."+rt.Name(), method)
}

// lookupInterpretedMethod resolves a method declared in interpreted
// source. SSA does not register methods in Package.Members; they live
// on the program's method-sets. We scan the program's functions,
// matching by simple name and receiver type.
func (p *program) lookupInterpretedMethod(receiver value.Value, name string) *ssa.Function {
	prog := p.ssaPkg.Prog
	for fn := range ssautil.AllFunctions(prog) {
		if fn == nil || fn.Name() != name {
			continue
		}
		if fn.Pkg != p.ssaPkg {
			continue
		}
		sig := fn.Signature
		if sig == nil || sig.Recv() == nil {
			continue
		}
		if !p.receiverMatches(receiver, sig.Recv().Type()) {
			continue
		}
		return fn
	}
	return nil
}

// receiverMatches reports whether the runtime receiver value satisfies
// the SSA-declared receiver type. We compare reflect.Types — both
// resolved through our type-resolver, so synthetic struct types align.
func (p *program) receiverMatches(receiver value.Value, recv types.Type) bool {
	rv, err := p.reflectOf(receiver, nil)
	if err != nil || !rv.IsValid() {
		return false
	}
	for rv.Kind() == reflect.Interface && !rv.IsNil() {
		rv = rv.Elem()
	}
	wantRT, err := p.resolver.ResolveType(recv)
	if err != nil {
		return false
	}
	if rv.Type() == wantRT {
		return true
	}
	// Pointer/value flexibility: SSA may declare a value receiver but
	// store the addressable form, or vice versa.
	if rv.Kind() == reflect.Ptr && wantRT.Kind() == reflect.Ptr && rv.Type().Elem() == wantRT.Elem() {
		return true
	}
	if rv.Kind() == reflect.Ptr && wantRT.Kind() != reflect.Ptr && rv.Type().Elem() == wantRT {
		return true
	}
	if rv.Kind() != reflect.Ptr && wantRT.Kind() == reflect.Ptr && rv.Type() == wantRT.Elem() {
		return true
	}
	// Named-primitive receivers (e.g. MyInt5 underlying int) lose their
	// declared identity once we go through reflect — both end up as
	// `int`. If the SSA receiver is a named type whose underlying kind
	// matches the runtime kind, accept the match. Method-name uniqueness
	// inside the interpreted package keeps this from picking the wrong
	// receiver in practice.
	if named, ok := recv.(*types.Named); ok {
		if under, err := p.resolver.ResolveType(named.Underlying()); err == nil && under == rv.Type() {
			return true
		}
	}
	// Compare by string representation as a final fallback — types
	// constructed via different paths (named vs underlying) can be
	// structurally identical without being == in reflect.
	if rv.Type().String() == wantRT.String() {
		return true
	}
	if rv.Kind() == reflect.Ptr && wantRT.Kind() == reflect.Ptr &&
		rv.Type().Elem().String() == wantRT.Elem().String() {
		return true
	}
	return false
}

// adjustReceiverShape converts a runtime receiver to the pointer/value
// form that fn declares. Go's spec lets a value receiver be called via
// `(*T).M()` and a pointer receiver via `T.M()` (when T is addressable),
// taking the address or dereferencing as needed. The interpreter does
// the same: if fn wants `T` and we have `*T`, deref; if fn wants `*T`
// and we have an addressable `T`, take its address.
func (p *program) adjustReceiverShape(recv value.Value, fn *ssa.Function) value.Value {
	if fn.Signature == nil || fn.Signature.Recv() == nil {
		return recv
	}
	wantRT, err := p.resolver.ResolveType(fn.Signature.Recv().Type())
	if err != nil {
		return recv
	}
	rv, err := p.reflectOf(recv, nil)
	if err != nil || !rv.IsValid() {
		return recv
	}
	// Already matches — nothing to do.
	if rv.Type() == wantRT {
		return recv
	}
	// We have *T, fn wants T (value receiver via pointer call).
	if rv.Kind() == reflect.Ptr && wantRT.Kind() != reflect.Ptr && rv.Type().Elem() == wantRT && !rv.IsNil() {
		conv := value.DefaultConverter()
		if uv, err := conv.FromReflect(rv.Elem()); err == nil {
			return uv
		}
	}
	// We have T, fn wants *T (pointer receiver via value call). Need an
	// addressable copy.
	if rv.Kind() != reflect.Ptr && wantRT.Kind() == reflect.Ptr && rv.Type() == wantRT.Elem() {
		addr := reflect.New(rv.Type())
		addr.Elem().Set(rv)
		conv := value.DefaultConverter()
		if uv, err := conv.FromReflect(addr); err == nil {
			return uv
		}
	}
	return recv
}
