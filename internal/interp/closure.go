// closure.go contains MakeClosure and the helper makeFuncValue. Both
// produce reflect-backed callable values that, when invoked, re-enter
// the interpreter with the captured free variables bound to their
// frame slots. Captures are direct Values; addressable bindings retain
// sharing through reflected pointer Values.
package interp

import (
	"context"
	"fmt"
	"reflect"

	"golang.org/x/tools/go/ssa"

	"github.com/t04dJ14n9/gig/value"
)

type interpretedFunc struct {
	p        *program
	ctx      context.Context
	fn       *ssa.Function
	freeVars []value.Value
	rv       reflect.Value
}

func (f *interpretedFunc) ReflectValue() reflect.Value { return f.rv }

func (f *interpretedFunc) Call(args []value.Value, depth int) ([]value.Value, error) {
	ctx := f.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return f.CallContext(ctx, args, depth)
}

func (f *interpretedFunc) CallContext(ctx context.Context, args []value.Value, depth int) ([]value.Value, error) {
	if len(f.fn.Blocks) == 0 {
		return f.p.callHostFunc(ctx, nil, f.fn, args, depth)
	}
	return f.p.callSSA(ctx, nil, f.fn, args, f.freeVars, depth)
}

// reflectValueAtDepth returns a host-callable wrapper whose interpreter
// re-entry starts at depth. The persistent wrapper stored on interpretedFunc
// uses depth zero for genuinely top-level host calls; synchronous callbacks
// passed into a host function are rebound at that host call's current depth.
func (f *interpretedFunc) reflectValueAtDepth(ctx context.Context, rt reflect.Type, depth int) reflect.Value {
	return reflect.MakeFunc(rt, func(rargs []reflect.Value) []reflect.Value {
		args := make([]value.Value, len(rargs))
		for i, ra := range rargs {
			v, err := f.p.converter.FromReflect(ra)
			if err != nil {
				panic(fmt.Sprintf("interp: convert closure arg %d: %v", i, err))
			}
			args[i] = v
		}
		results, err := f.CallContext(ctx, args, depth)
		if err != nil {
			panic(err)
		}
		out := make([]reflect.Value, rt.NumOut())
		for i := range out {
			ot := rt.Out(i)
			if i < len(results) {
				rv, err := f.p.converter.ToReflect(results[i], ot)
				if err != nil {
					panic(fmt.Sprintf("interp: convert closure result %d: %v", i, err))
				}
				out[i] = rv
			} else {
				out[i] = reflect.Zero(ot)
			}
		}
		return out
	})
}

// bindReflectDepth makes a per-host-call copy so concurrent invocations do
// not mutate the persistent top-level reflect wrapper.
func (f *interpretedFunc) bindReflectDepth(ctx context.Context, depth int) value.Value {
	bound := &interpretedFunc{
		p:        f.p,
		ctx:      ctx,
		fn:       f.fn,
		freeVars: f.freeVars,
	}
	bound.rv = bound.reflectValueAtDepth(ctx, f.rv.Type(), depth)
	return value.MakeFunc(bound)
}

// makeFuncValue wraps an *ssa.Function as a callable Value.
// freeVars is non-nil only for closures (MakeClosure); plain function
// references use nil. Interpreted code can call the returned value directly;
// the reflect.MakeFunc fallback still lets host code call interpreted
// functions transparently.
func (p *program) makeFuncValue(ctx context.Context, fn *ssa.Function, freeVars []value.Value) (value.Value, error) {
	rt, err := p.resolver.ResolveType(fn.Signature)
	if err != nil {
		return value.Value{}, err
	}
	callable := &interpretedFunc{p: p, ctx: ctx, fn: fn, freeVars: freeVars}
	callable.rv = callable.reflectValueAtDepth(ctx, rt, 0)
	return value.MakeFunc(callable), nil
}

// bindHostCallbackDepth gives direct interpreted-function arguments a
// reflect wrapper that preserves the current synchronous host-call depth.
// The original Value remains untouched, so a closure retained by interpreted
// code still has its top-level wrapper and concurrent host calls do not race.
func bindHostCallbackDepth(ctx context.Context, args []value.Value, depth int) []value.Value {
	var bound []value.Value
	for i, arg := range args {
		fn, ok := arg.Func()
		if !ok {
			continue
		}
		interpreted, ok := fn.(*interpretedFunc)
		if !ok {
			continue
		}
		if bound == nil {
			bound = append([]value.Value(nil), args...)
		}
		bound[i] = interpreted.bindReflectDepth(ctx, depth)
	}
	if bound != nil {
		return bound
	}
	return args
}

// runMakeClosure handles ssa.MakeClosure: build a free-vars list from
// the binding values in the surrounding frame, then wrap the inner
// function so subsequent Call instructions see a callable value.
func (p *program) runMakeClosure(fr *frame, instr *ssa.MakeClosure) error {
	fn, ok := instr.Fn.(*ssa.Function)
	if !ok {
		return fmt.Errorf("interp: MakeClosure target %T not a function", instr.Fn)
	}
	freeVars := make([]value.Value, len(instr.Bindings))
	for i, binding := range instr.Bindings {
		// Capture the current binding value, not the outer frame's value slot.
		// Addressable locals are already represented as pointer Values, so
		// mutations still go through shared storage. Snapshotting the Value
		// matters for loop-body Alloc instructions: the same SSA instruction
		// executes each iteration but must produce a fresh address.
		captured, err := p.readValue(fr, binding)
		if err != nil {
			return err
		}
		freeVars[i] = captured
	}
	v, err := p.makeFuncValue(fr.ctx, fn, freeVars)
	if err != nil {
		return err
	}
	fr.setValue(instr, v)
	return nil
}
