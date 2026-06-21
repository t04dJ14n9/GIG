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

	// slots is the hot path for SSA values known at layout time. The key is
	// the ssa.Value object identity, not Value.Name(), because SSA names are
	// only diagnostic and are not guaranteed to be unique.
	slots     []Cell
	slotKinds []fastSlotKind
	slotIndex map[ssa.Value]int

	// cells is the fallback store for values not assigned a slot, plus
	// lazily-created side state. Most ordinary instruction results should use
	// slots instead.
	cells    map[ssa.Value]*Cell
	addrRefs map[ssa.Value]addrRef

	blockPlans []*blockPlan
	freeVars   []*Cell
	iters      map[ssa.Value]*rangeIter

	// defer / panic / recover state.
	defers    []*deferRecord
	panicking bool
	panicVal  any

	cancelTicks int
}

type frameLayout struct {
	// index maps every slotted SSA value in fn to its offset in frame.slots.
	// It is built once per *ssa.Function and reused by every call frame.
	index     map[ssa.Value]int
	slotKinds []fastSlotKind

	// The remaining fields are precompiled execution hints. They do not
	// change semantics; runFrame falls back to visitInstr whenever a fast path
	// is not applicable.
	fusedIndexAddr map[*ssa.IndexAddr]ssa.Instruction
	blockPlans     []*blockPlan
}

type blockPlan struct {
	fusedIndexAddrConsumers []ssa.Instruction
	fastPhis                []fastPhi
	fastBlockOps            []fastOp
	fastInstrs              []fastInstr
	fastIndexAddrs          []fastIndexAddr
}

func (p *program) frameLayout(fn *ssa.Function) *frameLayout {
	if cached, ok := p.layouts.Load(fn); ok {
		return cached.(*frameLayout)
	}

	// Assign deterministic slot indexes for values that are local to this
	// function. Constants and globals are resolved directly by readValue, so
	// they do not need frame storage.
	index := make(map[ssa.Value]int)
	add := func(v ssa.Value) {
		if v == nil {
			return
		}
		if _, exists := index[v]; exists {
			return
		}
		index[v] = len(index)
	}
	for _, param := range fn.Params {
		add(param)
	}
	for _, freeVar := range fn.FreeVars {
		add(freeVar)
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			// Alloc returns an address. It is intentionally kept out of the
			// slot array because each execution of the same Alloc instruction
			// in a loop must produce a fresh addressable object.
			if _, ok := instr.(*ssa.Alloc); ok {
				continue
			}
			if v, ok := instr.(ssa.Value); ok {
				add(v)
			}
		}
	}
	slotKinds := make([]fastSlotKind, len(index))
	for v, idx := range index {
		switch {
		case isPlainIntType(v.Type()):
			slotKinds[idx] = fastSlotInt
		case isPlainBoolType(v.Type()):
			slotKinds[idx] = fastSlotBool
		}
	}

	// Compile block-local fast paths once. The dispatcher still uses the same
	// SSA instruction stream; these plans only short-circuit hot cases such as
	// Phis, int/bool arithmetic, and fused IndexAddr consumers.
	fusedIndexAddr := make(map[*ssa.IndexAddr]ssa.Instruction)
	blockPlans := make([]*blockPlan, len(fn.Blocks))
	for _, block := range fn.Blocks {
		var plan *blockPlan
		ensurePlan := func() *blockPlan {
			if plan == nil {
				plan = &blockPlan{}
			}
			return plan
		}
		if fastPhis := compileFastPhis(block, index); len(fastPhis) > 0 {
			ensurePlan().fastPhis = fastPhis
		}
		for i := 0; i+1 < len(block.Instrs); i++ {
			instr := block.Instrs[i]
			if indexAddr, ok := instr.(*ssa.IndexAddr); ok {
				consumer := block.Instrs[i+1]
				if isPlainIntSliceType(indexAddr.X.Type()) && fusableIndexAddrConsumer(indexAddr, consumer) {
					fusedIndexAddr[indexAddr] = consumer
					if ensurePlan().fusedIndexAddrConsumers == nil {
						plan.fusedIndexAddrConsumers = make([]ssa.Instruction, len(block.Instrs))
					}
					plan.fusedIndexAddrConsumers[i] = consumer
					if fastIndex, ok := compileFastIndexAddr(indexAddr, consumer, index); ok {
						if plan.fastIndexAddrs == nil {
							plan.fastIndexAddrs = make([]fastIndexAddr, len(block.Instrs))
						}
						plan.fastIndexAddrs[i] = fastIndex
					}
				}
			}
			if fast, ok := compileFastInstr(instr, index); ok {
				if ensurePlan().fastInstrs == nil {
					plan.fastInstrs = make([]fastInstr, len(block.Instrs))
				}
				plan.fastInstrs[i] = fast
			}
		}
		if len(block.Instrs) > 0 {
			last := len(block.Instrs) - 1
			if fast, ok := compileFastInstr(block.Instrs[last], index); ok {
				if ensurePlan().fastInstrs == nil {
					plan.fastInstrs = make([]fastInstr, len(block.Instrs))
				}
				plan.fastInstrs[last] = fast
			}
		}
		if plan != nil {
			plan.fastBlockOps = compileFastBlockOps(block, plan)
		}
		if plan != nil {
			blockPlans[block.Index] = plan
		}
	}
	layout := &frameLayout{
		index:          index,
		slotKinds:      slotKinds,
		fusedIndexAddr: fusedIndexAddr,
		blockPlans:     blockPlans,
	}
	actual, _ := p.layouts.LoadOrStore(fn, layout)
	return actual.(*frameLayout)
}

func (fr *frame) cell(v ssa.Value) (*Cell, bool) {
	if fr.slotIndex != nil {
		if idx, ok := fr.slotIndex[v]; ok {
			// Fast plans may update the typed cache without immediately
			// rebuilding Cell.Value. Materialize before exposing the Cell to
			// generic instruction code.
			fr.materializeSlot(idx)
			return &fr.slots[idx], true
		}
	}
	cell, ok := fr.cells[v]
	return cell, ok
}

func (fr *frame) ensureCells(capHint int) {
	if fr.cells != nil {
		return
	}
	if capHint < 1 {
		capHint = 1
	}
	fr.cells = make(map[ssa.Value]*Cell, capHint)
}

// setCell writes the runtime value for an SSA value. Reusing the Cell
// avoids allocating a new *Cell every time a loop re-executes the same
// static SSA instruction.
func (fr *frame) setCell(v ssa.Value, val value.Value) {
	if fr.slotIndex != nil {
		if idx, ok := fr.slotIndex[v]; ok {
			fr.setSlotValue(idx, val)
			return
		}
	}
	if cell, ok := fr.cells[v]; ok {
		cell.Value = val
		return
	}
	fr.ensureCells(1)
	fr.cells[v] = &Cell{Value: val}
}

// bindCell is used when a value first enters the frame: parameters, free
// variables, and preallocated locals. Unlike setCell, it preserves the
// source-facing name and type for diagnostics and addressable locals.
func (fr *frame) bindCell(v ssa.Value, val value.Value) {
	if fr.slotIndex != nil {
		if idx, ok := fr.slotIndex[v]; ok {
			fr.slots[idx].Name = v.Name()
			fr.slots[idx].Type = v.Type()
			fr.setSlotValue(idx, val)
			return
		}
	}
	if cell, ok := fr.cells[v]; ok {
		cell.Name = v.Name()
		cell.Type = v.Type()
		cell.Value = val
		return
	}
	fr.ensureCells(1)
	fr.cells[v] = &Cell{Name: v.Name(), Type: v.Type(), Value: val}
}

// setSlotValue keeps the generic Cell.Value and typed fast cache in sync.
// Fast instruction runners can later read fastInt/fastBool directly when
// the slot kind proves the value shape.
func (fr *frame) setSlotValue(idx int, val value.Value) {
	cell := &fr.slots[idx]
	cell.Value = val
	cell.fastDirty = false
	if idx >= len(fr.slotKinds) {
		return
	}
	switch fr.slotKinds[idx] {
	case fastSlotInt:
		if val.Kind() == value.KindInt {
			cell.fastInt = val.Int()
		}
	case fastSlotBool:
		if val.Kind() == value.KindBool {
			cell.fastBool = val.Bool()
		}
	}
}

// materializeSlot rebuilds Cell.Value after a fast path has updated only the
// typed cache and marked the slot dirty. Generic code must call this before
// reading Cell.Value.
func (fr *frame) materializeSlot(idx int) {
	if idx >= len(fr.slotKinds) || !fr.slots[idx].fastDirty {
		return
	}
	cell := &fr.slots[idx]
	switch fr.slotKinds[idx] {
	case fastSlotInt:
		cell.Value = value.MakeInt(cell.fastInt)
	case fastSlotBool:
		cell.Value = value.MakeBool(cell.fastBool)
	}
	cell.fastDirty = false
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
		fr.bindCell(param, args[i])
	}

	// Bind free variables (closures). Empty for plain functions.
	for i, fv := range fn.FreeVars {
		if i >= len(freeVars) {
			break
		}
		fr.bindCell(fv, freeVars[i].Value)
	}

	// Pre-allocate Cells for every Local. Locals are pointer-typed in
	// SSA and the interpreter models them as addressable
	// reflect.Values: see runAlloc for the same treatment of heap
	// Allocs.
	for _, local := range fn.Locals {
		ptr := derefSSAType(local.Type())
		addr, err := p.makeAddressable(ptr)
		if err != nil {
			return nil, fmt.Errorf("interp: %s: alloc local %s: %w", fn.Name(), local.Name(), err)
		}
		fr.bindCell(local, reflectValue(addr.Addr()))
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
			prev := p.panicFrame
			p.panicFrame = fr
			for i := len(fr.defers) - 1; i >= 0; i-- {
				_ = p.runDeferRec(fr, fr.defers[i])
			}
			p.panicFrame = prev
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
	// The layout is shared, but slots are per-call. A recursive call to the
	// same function gets a different frame with the same slotIndex mapping.
	return &frame{
		fn:         fn,
		block:      fn.Blocks[0],
		slots:      make([]Cell, len(layout.index)),
		slotKinds:  layout.slotKinds,
		slotIndex:  layout.index,
		blockPlans: layout.blockPlans,
		freeVars:   freeVars,
	}
}

func (fr *frame) blockPlan() *blockPlan {
	if fr.block == nil || fr.blockPlans == nil {
		return nil
	}
	if idx := fr.block.Index; idx >= 0 && idx < len(fr.blockPlans) {
		return fr.blockPlans[idx]
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
// fr.prevBlock; value-producing instructions update frame slots/cells.
func (p *program) runFrame(caller *frame, fr *frame, depth int) ([]value.Value, error) {
	if err := fr.checkContextNow(); err != nil {
		return nil, err
	}
	for fr.block != nil {
		// Phi nodes are read at block entry, BEFORE any other instruction
		// of the block runs. The semantic is "pick the edge from
		// prevBlock". We compute all Phis from a snapshot of the
		// current cell map so simultaneous updates don't see each
		// other.
		if err := p.runBlockPhis(fr); err != nil {
			return nil, err
		}
		if plan := fr.blockPlan(); plan != nil && len(plan.fastBlockOps) > 0 {
			// Some blocks compile to a straight-line fast plan. If it handles
			// the block completely, it returns a jump/return signal and the
			// generic instruction loop below is skipped.
			contState, ret, err := p.runFastBlock(fr, plan.fastBlockOps)
			if err != nil {
				return nil, err
			}
			switch contState {
			case contJump:
				continue
			case contReturn:
				return ret, nil
			}
		}

		// Step through the rest of the instructions.
		var ret []value.Value
		var contState continuation
		var err error
		var fusedIndexAddrConsumers []ssa.Instruction
		var fastInstrs []fastInstr
		var fastIndexAddrs []fastIndexAddr
		if plan := fr.blockPlan(); plan != nil {
			fusedIndexAddrConsumers = plan.fusedIndexAddrConsumers
			fastInstrs = plan.fastInstrs
			fastIndexAddrs = plan.fastIndexAddrs
		}
	instrs:
		for ip := 0; ip < len(fr.block.Instrs); ip++ {
			if err := fr.checkContext(); err != nil {
				return nil, err
			}
			instr := fr.block.Instrs[ip]
			if _, isPhi := instr.(*ssa.Phi); isPhi {
				continue // already handled
			}
			if ip < len(fastIndexAddrs) && fastIndexAddrs[ip].kind != fastIndexNone {
				// Fast IndexAddr combines address calculation with its
				// immediately-following load/store consumer when possible.
				if p.runFastIndexAddr(fr, fastIndexAddrs[ip]) {
					ip++
					continue
				}
			}
			if ip < len(fastInstrs) && fastInstrs[ip].kind != fastNone {
				// Fast instructions are predecoded variants of common SSA
				// operations. They must produce the same continuation signal
				// as visitInstr.
				contState, ret, err = p.runFastInstr(fr, fastInstrs[ip])
				if err != nil {
					return nil, err
				}
				switch contState {
				case contNext:
					continue
				case contJump:
					break instrs // restart outer loop with new fr.block
				case contReturn:
					return ret, nil
				}
			}
			if ip < len(fusedIndexAddrConsumers) {
				if next := fusedIndexAddrConsumers[ip]; next != nil {
					// Generic fused path used when the specific typed fast
					// IndexAddr plan did not apply at runtime.
					indexAddr := instr.(*ssa.IndexAddr)
					fused, err := p.tryRunFusedIndexAddr(fr, indexAddr, next)
					if err != nil {
						return nil, err
					}
					if fused {
						ip++
						continue
					}
				}
			}
			contState, ret, err = p.visitInstr(caller, fr, instr, depth)
			if err != nil {
				return nil, err
			}
			switch contState {
			case contNext:
				continue
			case contJump:
				break instrs // restart outer loop with new fr.block
			case contReturn:
				return ret, nil
			}
		}
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

// runBlockPhis evaluates every Phi at the start of the current block,
// using the prevBlock to choose which edge applies. All Phis are
// computed from a snapshot first, then assigned together — that
// matches Go SSA semantics where Phis run "simultaneously".
func (p *program) runBlockPhis(fr *frame) error {
	if plan := fr.blockPlan(); plan != nil && len(plan.fastPhis) > 0 {
		return p.runFastBlockPhis(fr, plan.fastPhis)
	}
	phiCount := 0
	for _, instr := range fr.block.Instrs {
		if _, ok := instr.(*ssa.Phi); !ok {
			break
		}
		phiCount++
	}
	if phiCount == 0 {
		return nil
	}

	var phiBuf [8]*ssa.Phi
	var valBuf [8]value.Value
	phis := phiBuf[:]
	vals := valBuf[:]
	if phiCount > len(phiBuf) {
		phis = make([]*ssa.Phi, phiCount)
		vals = make([]value.Value, phiCount)
	} else {
		phis = phis[:phiCount]
		vals = vals[:phiCount]
	}

	for idx := 0; idx < phiCount; idx++ {
		instr := fr.block.Instrs[idx]
		phi, ok := instr.(*ssa.Phi)
		if !ok {
			break // Phis are always at block start.
		}
		// Find which predecessor edge we came in from.
		var picked ssa.Value
		for i, pred := range fr.block.Preds {
			if pred == fr.prevBlock {
				picked = phi.Edges[i]
				break
			}
		}
		if picked == nil {
			// Entry block has no Phis to resolve in practice; if we hit
			// this, the SSA is malformed.
			return fmt.Errorf("interp: %s: phi at block %d has no matching predecessor edge", fr.fn.Name(), fr.block.Index)
		}
		v, err := p.readValue(fr, picked)
		if err != nil {
			return err
		}
		phis[idx] = phi
		vals[idx] = v
	}
	for idx, phi := range phis {
		fr.setCell(phi, vals[idx])
	}
	return nil
}
