// ops.go is the instruction dispatcher. It pattern-matches on
// ssa.Instruction concrete types and routes each one to a small
// handler. Phase 6 vertical slice covers scalar arithmetic, control
// flow, function calls, and Alloc/Store. Composite types, closures,
// host calls, defer/panic/recover, and concurrency follow in 6.2+.
package interp

import (
	"fmt"
	"go/constant"
	"go/token"
	"go/types"
	"reflect"

	"golang.org/x/tools/go/ssa"

	"github.com/t04dJ14n9/gig/value"
)

// visitInstr dispatches one SSA instruction. Most handlers compute a
// value or perform an effect and return only error; execution then
// continues with the next instruction. Only the control-flow handlers
// return the full triple: runIf and runJump yield contJump, and
// runReturn yields contReturn with the function's result values.
func (p *program) visitInstr(fr *frame, instr ssa.Instruction, depth int, singleResult *value.Value) (continuation, []value.Value, error) {
	switch x := instr.(type) {
	case *ssa.DebugRef:
		return contNext, nil, nil

	case *ssa.Return:
		return p.runReturn(fr, x, singleResult)

	case *ssa.If:
		return p.runIf(fr, x)

	case *ssa.Jump:
		return p.runJump(fr, x)

	case *ssa.BinOp:
		return contNext, nil, p.runBinOp(fr, x)

	case *ssa.UnOp:
		return contNext, nil, p.runUnOp(fr, x)

	case *ssa.Convert:
		return contNext, nil, p.runConvert(fr, x)

	case *ssa.ChangeType:
		return contNext, nil, p.runChangeType(fr, x)

	case *ssa.ChangeInterface:
		return contNext, nil, p.runChangeInterface(fr, x)

	case *ssa.MakeInterface:
		return contNext, nil, p.runMakeInterface(fr, x)

	case *ssa.Call:
		return contNext, nil, p.runCall(fr, x, depth)

	case *ssa.Alloc:
		return contNext, nil, p.runAlloc(fr, x)

	case *ssa.Store:
		return contNext, nil, p.runStore(fr, x)

	case *ssa.Field:
		return contNext, nil, p.runField(fr, x)

	case *ssa.FieldAddr:
		return contNext, nil, p.runFieldAddr(fr, x)

	case *ssa.IndexAddr:
		return contNext, nil, p.runIndexAddr(fr, x)

	case *ssa.Index:
		return contNext, nil, p.runIndex(fr, x)

	case *ssa.Slice:
		return contNext, nil, p.runSlice(fr, x)

	case *ssa.Lookup:
		return contNext, nil, p.runLookup(fr, x)

	case *ssa.MapUpdate:
		return contNext, nil, p.runMapUpdate(fr, x)

	case *ssa.MakeSlice:
		return contNext, nil, p.runMakeSlice(fr, x)

	case *ssa.MakeMap:
		return contNext, nil, p.runMakeMap(fr, x)

	case *ssa.MakeChan:
		return contNext, nil, p.runMakeChan(fr, x)

	case *ssa.Range:
		return contNext, nil, p.runRange(fr, x)

	case *ssa.Next:
		return contNext, nil, p.runNext(fr, x)

	case *ssa.Extract:
		return contNext, nil, p.runExtract(fr, x)

	case *ssa.MakeClosure:
		return contNext, nil, p.runMakeClosure(fr, x)

	case *ssa.Defer:
		return contNext, nil, p.runDefer(fr, x)

	case *ssa.RunDefers:
		return contNext, nil, p.runRunDefers(fr, x, depth)

	case *ssa.Panic:
		return contNext, nil, p.runPanic(fr, x)

	case *ssa.TypeAssert:
		return contNext, nil, p.runTypeAssert(fr, x)

	case *ssa.Go:
		return contNext, nil, p.runGo(fr, x)

	case *ssa.Send:
		return contNext, nil, p.runSend(fr, x)

	case *ssa.Select:
		return contNext, nil, p.runSelect(fr, x)

	case *ssa.Phi:
		// Already handled by runBlockPhis, but defensively no-op here
		// in case dispatch reaches us anyway.
		return contNext, nil, nil
	}
	return contNext, nil,
		fmt.Errorf("interp: %s: unsupported instruction %T at %s",
			fr.fn.Name(), instr, instr)
}

// readValue resolves any ssa.Value reference to a runtime Value. It
// covers: parameters, locals, prior instruction results, *ssa.Const,
// *ssa.Global (read), and *ssa.Function (Phase 6.2+).
func (p *program) readValue(fr *frame, v ssa.Value) (value.Value, error) {
	if v == nil {
		return value.MakeNil(), nil
	}
	// Common case first: params, locals, and instruction results all
	// live in the frame's value slots. Consts, globals, and function
	// references are not frame-local and fall through to the switch.
	if stored, ok := fr.value(v); ok {
		return stored, nil
	}
	switch x := v.(type) {
	case *ssa.Const:
		return p.constToValue(x)
	case *ssa.Global:
		p.globalsMu.RLock()
		stored, ok := p.globals[x]
		p.globalsMu.RUnlock()
		if ok {
			return stored, nil
		}
		// Global not in our package — try the host environment for an
		// external var (fmt.Stdout, encoding/base64.StdEncoding, ...).
		if p.env != nil && x.Pkg != nil && x.Pkg.Pkg != nil {
			if hv, ok := p.env.LookupVar(x.Pkg.Pkg.Path(), x.Name()); ok {
				val, err := hv.Get()
				if err != nil {
					return value.Value{}, err
				}
				return val, nil
			}
		}
		return value.Value{}, fmt.Errorf("interp: unknown global %s", x.Name())
	case *ssa.Function:
		// A bare *ssa.Function used as a value (e.g. taking its
		// address, passing as argument). Wrap it in a reflect-func via
		// reflect.MakeFunc so it can be called by host code or stored
		// in slices/maps. No free variables.
		return p.makeFuncValue(fr.ctx, x, nil)
	}
	return value.Value{}, fmt.Errorf("interp: %s: no value for %s (%T)", fr.fn.Name(), v.Name(), v)
}

// constToValue translates an ssa.Const to a runtime Value. The Convert
// step preserves Go's typed constant semantics (untyped 1 + int8(2)
// produces an int8 Value).
func (p *program) constToValue(c *ssa.Const) (value.Value, error) {
	if c.Value == nil {
		// Typed nil: get the zero value of the type.
		return p.converter.Zero(c.Type(), p.resolver)
	}
	switch c.Value.Kind() {
	case constant.Bool:
		return value.MakeBool(constant.BoolVal(c.Value)), nil
	case constant.String:
		return value.MakeString(constant.StringVal(c.Value)), nil
	case constant.Int:
		// Constant ints can exceed int64 range when the surrounding
		// type is uint64 (e.g. SetUint64(0xFFFFFFFFFFFFFFFF)).
		// constant.Int64Val saturates on overflow which silently
		// destroys the value; check for an unsigned destination first
		// and route through MakeUint when possible.
		if isUnsignedTargetType(c.Type()) {
			if u, ok := constant.Uint64Val(c.Value); ok {
				return convertUintResult(u, c.Type(), p)
			}
		}
		if i, ok := constant.Int64Val(c.Value); ok {
			return convertIntResult(i, c.Type(), p)
		}
		// Last-resort path for big.Int-sized constants flowing into
		// untyped contexts: round-trip through uint64.
		if u, ok := constant.Uint64Val(c.Value); ok {
			return convertUintResult(u, c.Type(), p)
		}
		return value.Value{}, fmt.Errorf("interp: integer constant out of representable range: %v", c.Value)
	case constant.Float:
		return convertFloatResult(c.Float64(), c.Type(), p)
	case constant.Complex:
		re, _ := constant.Float64Val(constant.Real(c.Value))
		im, _ := constant.Float64Val(constant.Imag(c.Value))
		return convertComplexResult(complex(re, im), c.Type(), p)
	}
	return value.Value{}, fmt.Errorf("interp: unsupported const kind %v", c.Value.Kind())
}

// --- per-instruction runners ------------------------------------------------

func (p *program) runReturn(
	fr *frame, instr *ssa.Return, singleResult *value.Value,
) (continuation, []value.Value, error) {
	if len(instr.Results) == 1 && singleResult != nil {
		resolved, err := p.readValue(fr, instr.Results[0])
		if err != nil {
			return contNext, nil, err
		}
		*singleResult = resolved
		fr.block = nil
		return contReturn, nil, nil
	}
	var results []value.Value
	if len(instr.Results) > 0 {
		results = make([]value.Value, len(instr.Results))
	}
	for i, result := range instr.Results {
		resolved, err := p.readValue(fr, result)
		if err != nil {
			return contNext, nil, err
		}
		results[i] = resolved
	}
	fr.block = nil
	return contReturn, results, nil
}

func (p *program) runIf(fr *frame, instr *ssa.If) (continuation, []value.Value, error) {
	cond, err := p.readValue(fr, instr.Cond)
	if err != nil {
		return contNext, nil, err
	}
	idx := 1
	if cond.Bool() {
		idx = 0
	}
	fr.prevBlock, fr.block = fr.block, fr.block.Succs[idx]
	return contJump, nil, nil
}

func (p *program) runJump(fr *frame, _ *ssa.Jump) (continuation, []value.Value, error) {
	fr.prevBlock, fr.block = fr.block, fr.block.Succs[0]
	return contJump, nil, nil
}

func (p *program) runBinOp(fr *frame, instr *ssa.BinOp) error {
	x, err := p.readValue(fr, instr.X)
	if err != nil {
		return err
	}
	y, err := p.readValue(fr, instr.Y)
	if err != nil {
		return err
	}
	out, err := evalBinOp(instr.Op, x, y, instr.Type(), p)
	if err != nil {
		return err
	}
	fr.setValue(instr, out)
	return nil
}

func (p *program) runUnOp(fr *frame, instr *ssa.UnOp) error {
	x, err := p.readValue(fr, instr.X)
	if err != nil {
		return err
	}
	if instr.Op == token.MUL {
		// Pointer dereference. If x is a reflect-pointer (e.g. from
		// Alloc/FieldAddr/IndexAddr), follow .Elem() and rewrap.
		// For scalar pointees we snapshot the loaded value so the
		// result doesn't alias the source slot — important for tuple
		// assignment patterns like a, b = b, a where both loads must
		// capture pre-store state. For composite pointees we keep
		// the live reflect.Value because subsequent FieldAddr /
		// IndexAddr / Set must operate on the actual storage.
		if rv, ok := x.Reflect(); ok && rv.Kind() == reflect.Ptr {
			if rv.IsNil() {
				panic("runtime error: invalid memory address or nil pointer dereference")
			}
			elem := rv.Elem()
			return p.storeLoadedReflect(fr, instr, elem)
		}
		fr.setValue(instr, x)
		return nil
	}
	if instr.Op == token.ARROW {
		// Channel receive.
		rv, err := p.reflectOf(x, nil)
		if err != nil {
			return err
		}
		recv, ok, err := recvWithContext(fr.ctx, rv)
		if err != nil {
			return err
		}
		if !ok {
			recv = reflect.Zero(rv.Type().Elem())
		}
		if instr.CommaOk {
			tt := instr.Type().(*types.Tuple)
			rt, err := p.resolver.ResolveType(tt)
			if err != nil {
				return err
			}
			holder := reflect.New(rt).Elem()
			holder.Field(0).Set(recv)
			holder.Field(1).SetBool(ok)
			fr.setValue(instr, reflectValue(holder))
		} else {
			out, err := p.converter.FromReflect(recv)
			if err != nil {
				return err
			}
			fr.setValue(instr, out)
		}
		return nil
	}
	out, err := evalUnOp(instr.Op, x, instr.Type(), p)
	if err != nil {
		return err
	}
	fr.setValue(instr, out)
	return nil
}

func (p *program) storeLoadedReflect(fr *frame, instr ssa.Value, elem reflect.Value) error {
	if s, ok := reflectIntSlice(elem); ok {
		fr.setValue(instr, value.MakeIntSlice(s))
		return nil
	}
	if needsReflectSnapshot(elem) {
		snap := reflect.New(elem.Type()).Elem()
		snap.Set(elem)
		elem = snap
	}
	out, err := p.converter.FromReflect(elem)
	if err != nil {
		return err
	}
	fr.setValue(instr, out)
	return nil
}

func reflectIntSlice(rv reflect.Value) ([]int, bool) {
	for rv.Kind() == reflect.Interface && !rv.IsNil() {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Slice || rv.Type().Elem().Kind() != reflect.Int {
		return nil, false
	}
	s, ok := rv.Interface().([]int)
	return s, ok
}

func (p *program) runConvert(fr *frame, instr *ssa.Convert) error {
	x, err := p.readValue(fr, instr.X)
	if err != nil {
		return err
	}
	out, err := p.converter.Convert(x, instr.Type(), p.resolver)
	if err != nil {
		return err
	}
	fr.setValue(instr, out)
	return nil
}

func (p *program) runChangeType(fr *frame, instr *ssa.ChangeType) error {
	x, err := p.readValue(fr, instr.X)
	if err != nil {
		return err
	}
	// ChangeType is a static type rename (e.g. []int -> sort.IntSlice).
	// The runtime representation is the same, but downstream method
	// dispatch and host-interface satisfaction need the new
	// reflect.Type. Convert via reflect when possible.
	dstRT, err := p.resolver.ResolveType(instr.Type())
	if err == nil {
		if srcRV, err := p.reflectOf(x, nil); err == nil && srcRV.IsValid() &&
			srcRV.Type() != dstRT && srcRV.Type().ConvertibleTo(dstRT) {
			out, err := p.converter.FromReflect(srcRV.Convert(dstRT))
			if err == nil {
				fr.setValue(instr, out)
				return nil
			}
		}
	}
	fr.setValue(instr, x)
	return nil
}

// runChangeInterface narrows or widens an interface value to a different
// interface type without changing the dynamic value. SSA emits this when
// `var w io.Writer = somethingThatIsReadWriter`. The runtime
// representation in our interp is a KindInterface box; we just rewrap
// the dynamic value in a holder of the new interface type.
func (p *program) runChangeInterface(fr *frame, instr *ssa.ChangeInterface) error {
	x, err := p.readValue(fr, instr.X)
	if err != nil {
		return err
	}
	dstRT, err := p.resolver.ResolveType(instr.Type())
	if err != nil || dstRT.Kind() != reflect.Interface {
		fr.setValue(instr, x)
		return nil //nolint:nilerr // Missing host interface metadata falls back to the original value.
	}
	srcRV, err := p.reflectOf(x, nil)
	if err != nil || !srcRV.IsValid() {
		fr.setValue(instr, x)
		return nil //nolint:nilerr // Unreflectable values remain in their interpreter representation.
	}
	holder := reflect.New(dstRT).Elem()
	dyn := srcRV
	if dyn.Kind() == reflect.Interface && !dyn.IsNil() {
		dyn = dyn.Elem()
	}
	if dyn.IsValid() && dyn.Type().AssignableTo(dstRT) {
		holder.Set(dyn)
	} else if dyn.IsValid() && dyn.Type().ConvertibleTo(dstRT) {
		holder.Set(dyn.Convert(dstRT))
	}
	fr.setValue(instr, value.MakeInterfaceBox(holder))
	return nil
}

func (p *program) runCall(fr *frame, instr *ssa.Call, depth int) error {
	common := instr.Common()
	if common.IsInvoke() {
		return p.runInvokeCall(fr, instr, common, depth)
	}
	if builtin, ok := common.Value.(*ssa.Builtin); ok {
		out, err := p.callBuiltin(fr, builtin, common.Args)
		if err != nil {
			return err
		}
		fr.setValue(instr, out)
		return nil
	}
	if fn, ok := common.Value.(*ssa.Function); ok {
		if len(fn.Blocks) == 0 {
			return p.runHostFunctionCall(fr, instr, fn, common.Args, depth)
		}
		return p.runDirectInterpretedCall(fr, instr, fn, common.Args, depth)
	}
	return p.runIndirectCall(fr, instr, common, depth)
}

func (p *program) readValuesInto(fr *frame, refs []ssa.Value, scratch []value.Value) ([]value.Value, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	var values []value.Value
	if cap(scratch) >= len(refs) {
		values = scratch[:len(refs)]
	} else {
		values = make([]value.Value, len(refs))
	}
	if err := p.fillValues(fr, refs, values); err != nil {
		return nil, err
	}
	return values, nil
}

func (p *program) fillValues(fr *frame, refs []ssa.Value, dst []value.Value) error {
	for i, ref := range refs {
		resolved, err := p.readValue(fr, ref)
		if err != nil {
			return err
		}
		dst[i] = resolved
	}
	return nil
}

func (p *program) finishCall(fr *frame, instr *ssa.Call, results []value.Value) error {
	stored, err := p.packResults(instr.Type(), results)
	if err != nil {
		return err
	}
	fr.setValue(instr, stored)
	return nil
}

// runInvokeCall executes an interface method invocation: x.M(...) where x: I.
// SSA models this with Common.IsInvoke()==true; Common.Method names the method,
// and Common.Value is the interface receiver. fr is both the frame reading
// the operands and the caller of the invoked method; depth+1 keeps the
// recursion guard honest when the method body is interpreted.
func (p *program) runInvokeCall(fr *frame, instr *ssa.Call, common *ssa.CallCommon, depth int) error {
	recvV, err := p.readValue(fr, common.Value)
	if err != nil {
		return err
	}
	args, err := p.readValuesInto(fr, common.Args, nil)
	if err != nil {
		return err
	}
	results, err := p.invokeMethodOn(fr.ctx, fr, recvV, common.Method.Name(), args, depth+1)
	if err != nil {
		return err
	}
	return p.finishCall(fr, instr, results)
}

// runHostFunctionCall dispatches a body-less SSA function through the host
// environment.
func (p *program) runHostFunctionCall(fr *frame, instr *ssa.Call, fn *ssa.Function, refs []ssa.Value, depth int) error {
	args, err := p.readValuesInto(fr, refs, nil)
	if err != nil {
		return err
	}
	results, err := p.callHostFunc(fr.ctx, fr, fn, args, depth+1)
	if err != nil {
		return err
	}
	return p.finishCall(fr, instr, results)
}

func (p *program) runDirectInterpretedCall(fr *frame, instr *ssa.Call, fn *ssa.Function, refs []ssa.Value, depth int) error {
	var oneArg [1]value.Value
	var args []value.Value
	switch len(refs) {
	case 0:
	case 1:
		arg, err := p.readValue(fr, refs[0])
		if err != nil {
			return err
		}
		oneArg[0] = arg
		args = oneArg[:]
	default:
		var err error
		args, err = p.readValuesInto(fr, refs, nil)
		if err != nil {
			return err
		}
	}

	if fn.Signature.Results().Len() == 1 {
		var oneResult value.Value
		_, err := p.callSSAInto(fr.ctx, fr, fn, args, nil, depth+1, &oneResult)
		if err != nil {
			return err
		}
		fr.setValue(instr, oneResult)
		return nil
	}

	results, err := p.callSSAInto(fr.ctx, fr, fn, args, nil, depth+1, nil)
	if err != nil {
		return err
	}
	return p.finishCall(fr, instr, results)
}

// runIndirectCall executes a closure or other function value through its
// interpreted implementation when available, falling back to reflect.Call.
func (p *program) runIndirectCall(fr *frame, instr *ssa.Call, common *ssa.CallCommon, depth int) error {
	target, err := p.readValue(fr, common.Value)
	if err != nil {
		return err
	}
	if fn, ok := target.Func(); ok {
		if interpreted, ok := fn.(*interpretedFunc); ok {
			args, err := p.readValuesInto(fr, common.Args, nil)
			if err != nil {
				return err
			}
			results, err := interpreted.CallContext(fr.ctx, args, depth+1)
			if err != nil {
				return err
			}
			return p.finishCall(fr, instr, results)
		}
	}
	rv, err := p.reflectOf(target, nil)
	if err != nil {
		return err
	}
	if rv.Kind() != reflect.Func {
		return fmt.Errorf("interp: %s: call target %T is not callable (kind=%s)",
			fr.fn.Name(), common.Value, rv.Kind())
	}
	args, err := p.readValuesInto(fr, common.Args, nil)
	if err != nil {
		return err
	}
	rargs, err := p.reflectArgs(rv.Type(), args)
	if err != nil {
		return err
	}
	rresults := rv.Call(rargs)
	results, err := p.valuesFromReflect(rresults)
	if err != nil {
		return err
	}
	return p.finishCall(fr, instr, results)
}

// packResults turns a function's []value.Value result tuple into a
// single Value suitable for the caller's instruction value slot. Single
// returns pass through; multi-return tuples become a synthetic
// reflect-struct so ssa.Extract can read them.
func (p *program) packResults(t types.Type, results []value.Value) (value.Value, error) {
	switch len(results) {
	case 0:
		return value.MakeNil(), nil
	case 1:
		return results[0], nil
	}
	tt, ok := t.(*types.Tuple)
	if !ok {
		return value.Value{}, fmt.Errorf("interp: multi-return packed for non-tuple type %s", t)
	}
	rt, err := p.resolver.ResolveType(tt)
	if err != nil {
		return value.Value{}, err
	}
	holder := reflect.New(rt).Elem()
	for i, r := range results {
		ft := holder.Field(i).Type()
		rv, err := p.converter.ToReflect(r, ft)
		if err != nil {
			return value.Value{}, err
		}
		holder.Field(i).Set(rv)
	}
	return reflectValue(holder), nil
}

// callBuiltin resolves SSA operands before delegating to the canonical
// builtin executor.
func (p *program) callBuiltin(fr *frame, b *ssa.Builtin, ssaArgs []ssa.Value) (value.Value, error) {
	var inline [2]value.Value
	args := inline[:]
	if len(ssaArgs) > len(inline) {
		args = make([]value.Value, len(ssaArgs))
	} else {
		args = args[:len(ssaArgs)]
	}
	if err := p.fillValues(fr, ssaArgs, args); err != nil {
		return value.Value{}, err
	}
	return p.executeBuiltin(fr, b, args)
}

func (p *program) runAlloc(fr *frame, instr *ssa.Alloc) error {
	// Alloc produces a *T value. We model that by holding the *T's
	// pointee as an addressable reflect.Value, and storing the pointer
	// (via .Addr()) as this SSA value's runtime value. Subsequent
	// FieldAddr/IndexAddr/Store/UnOp(MUL) all see this pointer.
	ptr := derefSSAType(instr.Type())
	addr, err := p.makeAddressable(ptr)
	if err != nil {
		return err
	}
	pointer := addr.Addr()
	fr.setValue(instr, reflectValue(pointer))
	return nil
}

func (p *program) runStore(fr *frame, instr *ssa.Store) error {
	val, err := p.readValue(fr, instr.Val)
	if err != nil {
		return err
	}
	switch addr := instr.Addr.(type) {
	case *ssa.Global:
		p.globalsMu.Lock()
		_, ok := p.globals[addr]
		if ok {
			p.globals[addr] = val
		}
		p.globalsMu.Unlock()
		if !ok {
			return fmt.Errorf("interp: store to unknown global %s", addr.Name())
		}
		return nil
	}
	stored, ok := fr.value(instr.Addr)
	if !ok {
		return fmt.Errorf(
			"interp: %s: store to unknown address %T %s",
			fr.fn.Name(), instr.Addr, instr.Addr.Name(),
		)
	}
	if rv, ok := stored.Reflect(); ok && rv.Kind() == reflect.Ptr && !rv.IsNil() {
		if err := p.assignReflectValue(rv.Elem(), val); err != nil {
			return err
		}
		return nil
	}
	fr.setValue(instr.Addr, val)
	return nil
}

func (p *program) assignReflectValue(dst reflect.Value, val value.Value) error {
	src, err := p.reflectOf(val, dst.Type())
	if err != nil {
		return err
	}
	if coerced, ok, err := coerceReflectValue(src, dst.Type()); err != nil {
		return err
	} else if ok {
		src = coerced
	}
	// Slice-of-concrete → slice-of-interface{} can show up after the
	// type-resolver breaks a self-referential cycle by substituting
	// `any` for the back-edge field. reflect.Set rejects the direct
	// assignment; rebuild the slice element-wise so each concrete
	// pointer is boxed into the interface{} slot.
	if !src.Type().AssignableTo(dst.Type()) &&
		src.Kind() == reflect.Slice && dst.Type().Kind() == reflect.Slice &&
		dst.Type().Elem().Kind() == reflect.Interface {
		out := reflect.MakeSlice(dst.Type(), src.Len(), src.Len())
		for i := 0; i < src.Len(); i++ {
			out.Index(i).Set(src.Index(i))
		}
		src = out
	}
	dst.Set(src)
	return nil
}

func coerceReflectValue(src reflect.Value, dstType reflect.Type) (reflect.Value, bool, error) {
	if !src.IsValid() {
		return reflect.Zero(dstType), true, nil
	}
	if src.Type().AssignableTo(dstType) {
		return src, true, nil
	}
	if src.Type().ConvertibleTo(dstType) {
		return src.Convert(dstType), true, nil
	}
	if src.Kind() == reflect.Interface && !src.IsNil() {
		return coerceReflectValue(src.Elem(), dstType)
	}
	if src.Kind() == reflect.Ptr && dstType.Kind() == reflect.Ptr &&
		src.Type().Elem().Kind() == reflect.Struct && dstType.Elem().Kind() == reflect.Struct {
		if src.IsNil() {
			return reflect.Zero(dstType), true, nil
		}
		elem, ok, err := coerceReflectValue(src.Elem(), dstType.Elem())
		if err != nil || !ok {
			return reflect.Value{}, ok, err
		}
		ptr := reflect.New(dstType.Elem())
		ptr.Elem().Set(elem)
		return ptr, true, nil
	}
	if src.Kind() == reflect.Struct && dstType.Kind() == reflect.Struct {
		if src.NumField() != dstType.NumField() {
			return reflect.Value{}, false, nil
		}
		out := reflect.New(dstType).Elem()
		for i := 0; i < dstType.NumField(); i++ {
			if src.Type().Field(i).Name != dstType.Field(i).Name {
				return reflect.Value{}, false, nil
			}
			field, ok, err := coerceReflectValue(src.Field(i), dstType.Field(i).Type)
			if err != nil || !ok {
				return reflect.Value{}, ok, err
			}
			out.Field(i).Set(field)
		}
		return out, true, nil
	}
	return reflect.Value{}, false, nil
}

// derefSSAType returns the pointee type of a *T SSA type. It is the SSA
// equivalent of gofun's deref helper.
func derefSSAType(t types.Type) types.Type {
	if pt, ok := t.Underlying().(*types.Pointer); ok {
		return pt.Elem()
	}
	return t
}

// isScalarKind reports whether a reflect.Kind is one whose load
// semantics produce an independent value. Scalars must be snapshotted
// on UnOp(MUL) so tuple-assignment patterns (a, b = b, a) read
// pre-store values; composite kinds (struct, slice, map, ptr...) must
// stay live so subsequent FieldAddr/IndexAddr operate on real storage.
func isScalarKind(k reflect.Kind) bool {
	switch k {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.Complex64, reflect.Complex128,
		reflect.Ptr:
		return true
	}
	return false
}

func needsReflectSnapshot(rv reflect.Value) bool {
	if !isScalarKind(rv.Kind()) {
		return false
	}
	if rv.Kind() == reflect.Ptr {
		return true
	}
	rt := rv.Type()
	return rt.Name() != "" && rt.PkgPath() != ""
}

// isUnsignedTargetType reports whether t is or wraps a Go unsigned
// integer type. Used to pick between Int64Val/Uint64Val when projecting
// a constant.Int onto its declared type so values > math.MaxInt64
// survive the round-trip.
func isUnsignedTargetType(t types.Type) bool {
	for t != nil {
		switch tt := t.(type) {
		case *types.Basic:
			switch tt.Kind() {
			case types.Uint, types.Uint8, types.Uint16, types.Uint32,
				types.Uint64, types.Uintptr,
				types.UntypedInt:
				return tt.Kind() != types.UntypedInt && tt.Info()&types.IsUnsigned != 0
			}
			return false
		case *types.Named:
			t = tt.Underlying()
			continue
		case *types.Alias:
			t = types.Unalias(tt)
			continue
		}
		return false
	}
	return false
}
