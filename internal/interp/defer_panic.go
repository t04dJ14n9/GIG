// defer_panic.go implements ssa.Defer + ssa.RunDefers and the
// panic/recover plumbing that pairs with them. The model:
//
//   - Each frame carries a stack of pending defers (instr.Common()).
//   - ssa.Defer pushes a defer record (function value + args, captured
//     at the point of the defer statement, per Go semantics).
//   - ssa.RunDefers pops and runs them in LIFO order. Any panic during
//     a defer body is recorded in the frame; recover() consumes it.
//   - panic in a non-defer body unwinds normally through Go's panic
//     mechanism; runFrame catches it, records the panicking flag, and
//     runs defers before propagating.
package interp

import (
	"fmt"
	"reflect"

	"golang.org/x/tools/go/ssa"

	"github.com/t04dJ14n9/gig/value"
)

// deferRecord is what ssa.Defer pushes. We snapshot the args at defer
// time (so the typical for-loop closure capture pitfall is reproduced
// faithfully), and re-invoke at RunDefers time.
type deferRecord struct {
	fn   value.Value   // function value (possibly a closure)
	args []value.Value // snapshot of args
	// For builtins (close, recover, etc) we keep the SSA op around.
	builtin *ssa.Builtin
	// fnSSA: when the call target is *ssa.Function we can call it
	// directly via callSSA rather than reflect.Value.Call.
	fnSSA *ssa.Function
	// invokeMethod / invokeRecv: when the deferred call is an interface
	// method invocation (`defer iface.Method(args)`), SSA models it as
	// IsInvoke; remember the receiver and method name so we can route
	// through invokeMethodOn at fire time.
	invokeRecv   value.Value
	invokeMethod string
}

func (p *program) runDefer(fr *frame, instr *ssa.Defer) (continuation, []value.Value, error) {
	common := instr.Common()
	args := make([]value.Value, len(common.Args))
	for i, a := range common.Args {
		v, err := p.readValue(fr, a)
		if err != nil {
			return contNext, nil, err
		}
		args[i] = v
	}
	rec := &deferRecord{args: args}
	// `defer recv.Method(args)` is modelled by SSA as an Invoke whose
	// Common().Value is the receiver and Common().Method is the method
	// name. Capture both so we can dispatch through invokeMethodOn at
	// fire time — the same path that handles regular method calls.
	if common.IsInvoke() {
		recv, err := p.readValue(fr, common.Value)
		if err != nil {
			return contNext, nil, err
		}
		rec.invokeRecv = recv
		rec.invokeMethod = common.Method.Name()
		fr.defers = append(fr.defers, rec)
		return contNext, nil, nil
	}
	switch tgt := common.Value.(type) {
	case *ssa.Function:
		rec.fnSSA = tgt
	case *ssa.Builtin:
		rec.builtin = tgt
	default:
		v, err := p.readValue(fr, common.Value)
		if err != nil {
			return contNext, nil, err
		}
		rec.fn = v
	}
	fr.defers = append(fr.defers, rec)
	return contNext, nil, nil
}

func (p *program) runRunDefers(fr *frame, _ *ssa.RunDefers) (continuation, []value.Value, error) {
	if err := p.executeDefers(fr); err != nil {
		return contNext, nil, err
	}
	return contNext, nil, nil
}

// executeDefers walks the deferred records in LIFO order and runs each.
// A panic inside a defer body is captured in fr.panicVal; subsequent
// recover() in this frame will retrieve it.
func (p *program) executeDefers(fr *frame) error {
	for i := len(fr.defers) - 1; i >= 0; i-- {
		rec := fr.defers[i]
		if err := p.runDeferRec(fr, rec); err != nil {
			return err
		}
	}
	fr.defers = nil
	return nil
}

func (p *program) runDeferRec(fr *frame, rec *deferRecord) error {
	defer func() {
		if re := recover(); re != nil {
			fr.panicking = true
			fr.panicVal = re
		}
	}()
	switch {
	case rec.invokeMethod != "":
		_, err := p.invokeMethodOn(fr.ctx, rec.invokeRecv, rec.invokeMethod, rec.args)
		return err
	case rec.fnSSA != nil:
		// A deferred SSA function may be either an interpreted body or
		// a host method (e.g. `defer mu.Unlock()`). The latter has no
		// SSA blocks; route those through the host bridge.
		if len(rec.fnSSA.Blocks) == 0 {
			_, err := p.callHostFunc(fr.ctx, rec.fnSSA, rec.args)
			return err
		}
		_, err := p.callSSA(fr.ctx, fr, rec.fnSSA, rec.args, nil, 0)
		return err
	case rec.builtin != nil:
		// Builtins as defer targets are rare (close, print).
		_, err := p.callBuiltinDirect(fr, rec.builtin, rec.args)
		return err
	}
	// Function-value. If it wraps an interpreted body, dispatch through
	// callSSA with fr as the caller so a recover() inside the deferred
	// closure can find the panicking frame via the threaded caller. Going
	// through reflect.Call would re-enter with a nil caller and lose that
	// link. Genuinely-external func values still take the reflect path.
	if fn, ok := rec.fn.Func(); ok {
		if ifn, ok := fn.(*interpretedFunc); ok && len(ifn.fn.Blocks) > 0 {
			_, err := p.callSSA(fr.ctx, fr, ifn.fn, rec.args, ifn.freeVars, 0)
			return err
		}
	}
	rv, err := p.reflectOf(rec.fn, nil)
	if err != nil {
		return err
	}
	if rv.Kind() != reflect.Func {
		return fmt.Errorf("interp: defer target not callable")
	}
	rargs := make([]reflect.Value, len(rec.args))
	for i, a := range rec.args {
		rargs[i], err = p.converter.ToReflect(a, rv.Type().In(i))
		if err != nil {
			return err
		}
	}
	rv.Call(rargs)
	return nil
}

// callBuiltinDirect lets defer trampoline through the builtin path
// using already-resolved args.
func (p *program) callBuiltinDirect(fr *frame, b *ssa.Builtin, args []value.Value) (value.Value, error) {
	// Mirror callBuiltin without the SSA arg readout step.
	switch b.Name() {
	case "print", "println":
		return value.MakeNil(), nil
	case "close":
		rv, err := p.reflectOf(args[0], nil)
		if err != nil {
			return value.Value{}, err
		}
		rv.Close()
		return value.MakeNil(), nil
	case "panic":
		if len(args) > 0 {
			panic(args[0].Interface())
		}
		panic("panic with no argument")
	case "recover":
		if fr != nil && fr.panicking {
			v := fr.panicVal
			fr.panicking = false
			fr.panicVal = nil
			c := value.DefaultConverter()
			vv, err := c.FromAny(v)
			if err != nil {
				return value.Value{}, err
			}
			return vv, nil
		}
		return value.MakeNil(), nil
	}
	return value.Value{}, fmt.Errorf("interp: defer of builtin %s not supported", b.Name())
}
