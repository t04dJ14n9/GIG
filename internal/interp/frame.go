// frame.go is the per-call execution record and dispatcher loop. It
// mirrors gofun's frame model: walk the SSA basic blocks one
// instruction at a time, branching on the SSA node type, with Phi
// nodes resolved at block entry.
package interp

import (
	"context"
	"fmt"

	"golang.org/x/tools/go/ssa"

	"github.com/t04dJ14n9/gig/value"
)

// continuation is the next-action signal returned by every per-instruction
// runner. It mirrors gofun's _NEXT/_JUMP/_RETURN tri-state.
type continuation int

const (
	contNext   continuation = iota // continue with the next instruction
	contJump                       // block changed; restart the outer loop
	contReturn                     // function is returning
)

const cancelCheckInterval = 1024

// frame is the per-call activation record for one interpreted function
// invocation. The SSA graph is immutable; all runtime state for that
// invocation lives here.
type frame struct {
	fn        *ssa.Function
	ctx       context.Context
	block     *ssa.BasicBlock // current basic block; nil means the frame is done
	prevBlock *ssa.BasicBlock // predecessor used to select Phi edges

	layout *frameLayout
	values []value.Value

	iters map[ssa.Value]*rangeIter

	// defer / panic / recover state.
	defers    []*deferRecord
	panicking bool
	panicVal  any

	cancelTicks int
}

type frameLayout struct {
	values []ssa.Value
	index  map[ssa.Value]int
	blocks []blockPlan
}

func (p *program) frameLayout(fn *ssa.Function) *frameLayout {
	if cached, ok := p.layouts.Load(fn); ok {
		return cached.(*frameLayout)
	}

	values, index := collectFrameValues(fn)
	blocks := make([]blockPlan, len(fn.Blocks))
	for _, block := range fn.Blocks {
		blocks[block.Index] = compileBlockPlan(block, index)
	}
	layout := &frameLayout{values: values, index: index, blocks: blocks}
	actual, _ := p.layouts.LoadOrStore(fn, layout)
	return actual.(*frameLayout)
}

func (fr *frame) value(v ssa.Value) (value.Value, bool) {
	idx, ok := fr.layout.index[v]
	if !ok {
		return value.Value{}, false
	}
	return fr.values[idx], true
}

func (fr *frame) setValue(v ssa.Value, val value.Value) {
	idx, ok := fr.layout.index[v]
	if !ok {
		panic(fmt.Sprintf("interp: internal: %s has no frame index for %T %s", fr.fn.Name(), v, v.Name()))
	}
	fr.values[idx] = val
}

func (fr *frame) bindValue(v ssa.Value, val value.Value) {
	fr.setValue(v, val)
}

// callSSA invokes an SSA function with the given args. Returns the
// function's result tuple (zero, one, or many). depth is the current
// call depth, bumped on every entry to catch runaway recursion.
//
// caller is used for diagnostics and recover() once defer/panic land.
// freeVars is for closures (Phase 6.3); pass nil for plain functions.
func (p *program) callSSA(ctx context.Context, caller *frame, fn *ssa.Function, args []value.Value, freeVars []*Cell, depth int) (results []value.Value, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if depth >= p.maxDepth {
		return nil, fmt.Errorf("interp: max call depth %d exceeded calling %s", p.maxDepth, fn.Name())
	}
	if len(fn.Blocks) == 0 {
		return nil, fmt.Errorf("interp: function %s has no body", fn.Name())
	}

	fr := p.newFrame(fn, freeVars)
	fr.ctx = ctx
	fr.cancelTicks = 0

	// Bind parameters.
	for i, param := range fn.Params {
		fr.bindValue(param, args[i])
	}

	// Bind free variables (closures). Empty for plain functions.
	for i, fv := range fn.FreeVars {
		if i >= len(freeVars) {
			break
		}
		fr.bindValue(fv, freeVars[i].Value)
	}

	// Pre-allocate values for every Local. Locals are pointer-typed in
	// SSA and the interpreter models them as addressable
	// reflect.Values: see runAlloc for the same treatment of heap
	// Allocs.
	for _, local := range fn.Locals {
		ptr := derefSSAType(local.Type())
		addr, err := p.makeAddressable(ptr)
		if err != nil {
			return nil, fmt.Errorf("interp: %s: alloc local %s: %w", fn.Name(), local.Name(), err)
		}
		fr.bindValue(local, reflectValue(addr.Addr()))
	}

	// Install a panic handler so deferred functions can run and
	// recover() can take effect. If the panic isn't recovered here,
	// re-panic so an outer interpreted callSSA can run its own defers
	// and (potentially) consume the panic. Only the top-level Call
	// surfaces the panic as a returned error; intermediate frames
	// must propagate it as a panic so chained recover() works.
	defer func() {
		if re := recover(); re != nil {
			fr.panicking = true
			fr.panicVal = re
			// Run this frame's defers with fr as their caller so a
			// deferred closure's recover() can locate the panicking
			// frame via the threaded caller (see callBuiltin "recover").
			for i := len(fr.defers) - 1; i >= 0; i-- {
				_ = p.runDeferRec(fr, fr.defers[i])
			}
			fr.defers = nil
			if fr.panicking {
				// Not recovered: propagate as a panic so the caller
				// frame can engage its own defers/recover.
				panic(fr.panicVal)
			}
			// Recovered: jump into the function's Recover block (if
			// SSA emitted one) so any named-return reads land in the
			// caller's results.
			if fr.fn.Recover != nil {
				fr.block = fr.fn.Recover
				fr.prevBlock = nil
				rs, rerr := p.runFrame(caller, fr, depth)
				if rerr != nil {
					err = rerr
					results = nil
					return
				}
				results = rs
				err = nil
				return
			}
			results, _ = p.zeroResultsFor(fn)
			err = nil
		}
	}()

	results, err = p.runFrame(caller, fr, depth)
	return results, err
}

func (p *program) newFrame(fn *ssa.Function, freeVars []*Cell) *frame {
	layout := p.frameLayout(fn)
	return p.newFrameWithLayout(fn, freeVars, layout)
}

func (p *program) newFrameWithLayout(fn *ssa.Function, freeVars []*Cell, layout *frameLayout) *frame {
	const inlineFrameValueCount = 8
	type inlineFrame struct {
		frame
		values [inlineFrameValueCount]value.Value
	}

	var fr *frame
	if len(layout.values) <= inlineFrameValueCount {
		allocation := &inlineFrame{}
		fr = &allocation.frame
		fr.values = allocation.values[:len(layout.values)]
	} else {
		fr = &frame{values: make([]value.Value, len(layout.values))}
	}
	fr.fn = fn
	fr.layout = layout
	fr.block = fn.Blocks[0]
	return fr
}

func (fr *frame) blockPlan() *blockPlan {
	if fr.block == nil || fr.layout == nil {
		return nil
	}
	if idx := fr.block.Index; idx >= 0 && idx < len(fr.layout.blocks) {
		return &fr.layout.blocks[idx]
	}
	return nil
}

// zeroResultsFor returns the function's zero result tuple (one
// value.Value per Results entry). Used by the panic-recover path
// when a deferred recover() consumes a panic and the function would
// otherwise leak nil results to the caller.
func (p *program) zeroResultsFor(fn *ssa.Function) ([]value.Value, error) {
	sig := fn.Signature
	if sig == nil {
		return nil, nil
	}
	res := sig.Results()
	if res == nil || res.Len() == 0 {
		return nil, nil
	}
	out := make([]value.Value, res.Len())
	for i := 0; i < res.Len(); i++ {
		v, err := p.converter.Zero(res.At(i).Type(), p.resolver)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// runFrame is the dispatch loop. It walks blocks until a Return is
// hit or an error escapes. Control-flow instructions update fr.block and
// fr.prevBlock; value-producing instructions update canonical value storage.
func (p *program) runFrame(caller *frame, fr *frame, depth int) ([]value.Value, error) {
	if err := fr.checkContextNow(); err != nil {
		return nil, err
	}

blocks:
	for fr.block != nil {
		plan := fr.blockPlan()
		if plan == nil {
			return nil, fmt.Errorf("interp: %s: missing plan for block %d", fr.fn.Name(), fr.block.Index)
		}
		if err := p.runBlockPhis(fr, plan.phis); err != nil {
			return nil, err
		}

		for _, op := range plan.ops {
			if err := fr.checkContext(); err != nil {
				return nil, err
			}
			contState, ret, err := p.runPlannedOp(caller, fr, op, depth)
			if err != nil {
				return nil, err
			}
			switch contState {
			case contNext:
				continue
			case contJump:
				continue blocks
			case contReturn:
				return ret, nil
			}
		}
		return nil, fmt.Errorf("interp: %s: ran off the end of block %d", fr.fn.Name(), fr.block.Index)
	}
	return nil, fmt.Errorf("interp: %s: ran off the end of a block", fr.fn.Name())
}

func (fr *frame) checkContext() error {
	fr.cancelTicks++
	if fr.cancelTicks < cancelCheckInterval {
		return nil
	}
	fr.cancelTicks = 0
	return fr.checkContextNow()
}

func (fr *frame) checkContextNow() error {
	if fr.ctx == nil {
		return nil
	}
	return fr.ctx.Err()
}
