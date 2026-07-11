package interp

import (
	"fmt"
	"go/constant"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/ssa"

	"github.com/t04dJ14n9/gig/value"
)

type fastInstrKind uint8

const (
	fastNone fastInstrKind = iota
	fastIntBinOp
	fastIf
	fastJump
)

type fastIntRefKind uint8

const (
	fastIntSlot fastIntRefKind = iota
	fastIntConst
)

type fastIntRef struct {
	kind fastIntRefKind
	slot int
	val  int64
}

func (r fastIntRef) read(fr *frame) int64 {
	if r.kind == fastIntConst {
		return r.val
	}
	return fr.cellStorage[r.slot].Value.Int()
}

type fastPhi struct {
	dst   int
	edges []fastIntRef
}

type fastInstr struct {
	kind     fastInstrKind
	op       token.Token
	dst      int
	x        fastIntRef
	y        fastIntRef
	condSlot int
}

type fastIndexKind uint8

const (
	fastIndexNone fastIndexKind = iota
	fastIndexLoad
	fastIndexStore
)

type fastIndexAddr struct {
	kind      fastIndexKind
	sliceSlot int
	index     fastIntRef
	dst       int
	val       fastIntRef
}

type fastOpKind uint8

const (
	fastOpInstr fastOpKind = iota
)

type fastOp struct {
	kind  fastOpKind
	instr fastInstr
}

func compileFastPhis(block *ssa.BasicBlock, slots map[ssa.Value]int) []fastPhi {
	phiCount := 0
	for _, instr := range block.Instrs {
		if _, ok := instr.(*ssa.Phi); !ok {
			break
		}
		phiCount++
	}
	if phiCount == 0 {
		return nil
	}
	phis := make([]fastPhi, 0, phiCount)
	for i := 0; i < phiCount; i++ {
		phi := block.Instrs[i].(*ssa.Phi)
		if !isPlainIntType(phi.Type()) {
			return nil
		}
		dst, ok := slots[phi]
		if !ok {
			return nil
		}
		edges := make([]fastIntRef, len(phi.Edges))
		for edgeIdx, edge := range phi.Edges {
			ref, ok := fastIntRefFor(edge, slots)
			if !ok {
				return nil
			}
			edges[edgeIdx] = ref
		}
		phis = append(phis, fastPhi{dst: dst, edges: edges})
	}
	return phis
}

func compileFastIndexAddr(indexAddr *ssa.IndexAddr, consumer ssa.Instruction, slots map[ssa.Value]int) (fastIndexAddr, bool) {
	if !isPlainIntSliceType(indexAddr.X.Type()) || !fusableIndexAddrConsumer(indexAddr, consumer) {
		return fastIndexAddr{}, false
	}
	sliceSlot, ok := slots[indexAddr.X]
	if !ok {
		return fastIndexAddr{}, false
	}
	index, ok := fastIntRefFor(indexAddr.Index, slots)
	if !ok {
		return fastIndexAddr{}, false
	}
	switch instr := consumer.(type) {
	case *ssa.UnOp:
		dst, ok := slots[instr]
		if !ok || !isPlainIntType(instr.Type()) {
			return fastIndexAddr{}, false
		}
		return fastIndexAddr{kind: fastIndexLoad, sliceSlot: sliceSlot, index: index, dst: dst}, true
	case *ssa.Store:
		val, ok := fastIntRefFor(instr.Val, slots)
		if !ok {
			return fastIndexAddr{}, false
		}
		return fastIndexAddr{kind: fastIndexStore, sliceSlot: sliceSlot, index: index, val: val}, true
	}
	return fastIndexAddr{}, false
}

func compileFastInstr(instr ssa.Instruction, slots map[ssa.Value]int) (fastInstr, bool) {
	switch x := instr.(type) {
	case *ssa.BinOp:
		return compileFastBinOp(x, slots)
	case *ssa.If:
		cond, ok := slots[x.Cond]
		if !ok || !isPlainBoolType(x.Cond.Type()) {
			return fastInstr{}, false
		}
		return fastInstr{kind: fastIf, condSlot: cond}, true
	case *ssa.Jump:
		return fastInstr{kind: fastJump}, true
	}
	return fastInstr{}, false
}

func compileFastBlockOps(block *ssa.BasicBlock, plan *blockPlan) []fastOp {
	if plan == nil {
		return nil
	}
	ops := make([]fastOp, 0, len(block.Instrs))
	for i, instr := range block.Instrs {
		switch instr.(type) {
		case *ssa.Phi, *ssa.DebugRef:
			continue
		}
		if i < len(plan.fastInstrs) && plan.fastInstrs[i].kind != fastNone {
			ops = append(ops, fastOp{kind: fastOpInstr, instr: plan.fastInstrs[i]})
			continue
		}
		return nil
	}
	if len(ops) == 0 {
		return nil
	}
	last := ops[len(ops)-1]
	if last.kind != fastOpInstr || (last.instr.kind != fastIf && last.instr.kind != fastJump) {
		return nil
	}
	return ops
}

func compileFastBinOp(instr *ssa.BinOp, slots map[ssa.Value]int) (fastInstr, bool) {
	x, ok := fastIntRefFor(instr.X, slots)
	if !ok {
		return fastInstr{}, false
	}
	y, ok := fastIntRefFor(instr.Y, slots)
	if !ok {
		return fastInstr{}, false
	}
	dst, ok := slots[instr]
	if !ok {
		return fastInstr{}, false
	}
	if isPlainIntType(instr.Type()) {
		switch instr.Op {
		case token.ADD, token.SUB, token.MUL, token.QUO, token.REM:
			return fastInstr{kind: fastIntBinOp, op: instr.Op, dst: dst, x: x, y: y}, true
		}
	}
	if isPlainBoolType(instr.Type()) {
		switch instr.Op {
		case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
			return fastInstr{kind: fastIntBinOp, op: instr.Op, dst: dst, x: x, y: y}, true
		}
	}
	return fastInstr{}, false
}

func fastIntRefFor(v ssa.Value, slots map[ssa.Value]int) (fastIntRef, bool) {
	if c, ok := v.(*ssa.Const); ok {
		n, ok := intConstValue(c)
		if !ok {
			return fastIntRef{}, false
		}
		return fastIntRef{kind: fastIntConst, val: n}, true
	}
	if !isPlainIntType(v.Type()) {
		return fastIntRef{}, false
	}
	slot, ok := slots[v]
	if !ok {
		return fastIntRef{}, false
	}
	return fastIntRef{kind: fastIntSlot, slot: slot}, true
}

func intConstValue(c *ssa.Const) (int64, bool) {
	if c == nil || c.Value == nil || c.Value.Kind() != constant.Int {
		return 0, false
	}
	if !isPlainIntType(c.Type()) {
		return 0, false
	}
	n, ok := constant.Int64Val(c.Value)
	return n, ok
}

func isPlainIntType(t types.Type) bool {
	b, ok := t.(*types.Basic)
	return ok && b.Kind() == types.Int
}

func isPlainBoolType(t types.Type) bool {
	b, ok := t.(*types.Basic)
	return ok && b.Kind() == types.Bool
}

func (p *program) runFastBlockPhis(fr *frame, phis []fastPhi) error {
	edge := -1
	for i, pred := range fr.block.Preds {
		if pred == fr.prevBlock {
			edge = i
			break
		}
	}
	if edge < 0 {
		return fmt.Errorf("interp: %s: phi at block %d has no matching predecessor edge", fr.fn.Name(), fr.block.Index)
	}

	var valBuf [8]int64
	vals := valBuf[:]
	if len(phis) > len(valBuf) {
		vals = make([]int64, len(phis))
	} else {
		vals = vals[:len(phis)]
	}
	for i, phi := range phis {
		vals[i] = phi.edges[edge].read(fr)
	}
	for i, phi := range phis {
		fr.setFastIntSlot(phi.dst, vals[i])
	}
	return nil
}

func (p *program) runFastIndexAddr(fr *frame, instr fastIndexAddr) bool {
	s, ok := fr.cellStorage[instr.sliceSlot].Value.IntSlice()
	if !ok {
		return false
	}
	idx := int(instr.index.read(fr))
	switch instr.kind {
	case fastIndexLoad:
		fr.setFastIntSlot(instr.dst, int64(s[idx]))
		return true
	case fastIndexStore:
		s[idx] = int(instr.val.read(fr))
		return true
	}
	return false
}

func (p *program) runFastBlock(fr *frame, ops []fastOp) (continuation, []value.Value, error) {
	if err := fr.checkContext(); err != nil {
		return contNext, nil, err
	}
	for _, op := range ops {
		switch op.kind {
		case fastOpInstr:
			contState, ret, err := p.runFastInstr(fr, op.instr)
			if err != nil || contState != contNext {
				return contState, ret, err
			}
		default:
			return contNext, nil, fmt.Errorf("interp: unsupported fast op kind %d", op.kind)
		}
	}
	return contNext, nil, fmt.Errorf("interp: fast block for %s ended without control transfer", fr.fn.Name())
}

func (p *program) runFastInstr(fr *frame, instr fastInstr) (continuation, []value.Value, error) {
	switch instr.kind {
	case fastIntBinOp:
		x := instr.x.read(fr)
		y := instr.y.read(fr)
		switch instr.op {
		case token.ADD:
			fr.setFastIntSlot(instr.dst, x+y)
		case token.SUB:
			fr.setFastIntSlot(instr.dst, x-y)
		case token.MUL:
			fr.setFastIntSlot(instr.dst, x*y)
		case token.QUO:
			fr.setFastIntSlot(instr.dst, x/y)
		case token.REM:
			fr.setFastIntSlot(instr.dst, x%y)
		case token.EQL:
			fr.setFastBoolSlot(instr.dst, x == y)
		case token.NEQ:
			fr.setFastBoolSlot(instr.dst, x != y)
		case token.LSS:
			fr.setFastBoolSlot(instr.dst, x < y)
		case token.LEQ:
			fr.setFastBoolSlot(instr.dst, x <= y)
		case token.GTR:
			fr.setFastBoolSlot(instr.dst, x > y)
		case token.GEQ:
			fr.setFastBoolSlot(instr.dst, x >= y)
		default:
			return contNext, nil, fmt.Errorf("interp: unsupported fast int op %s", instr.op)
		}
		return contNext, nil, nil
	case fastIf:
		idx := 1
		if fr.readFastBoolSlot(instr.condSlot) {
			idx = 0
		}
		fr.prevBlock, fr.block = fr.block, fr.block.Succs[idx]
		return contJump, nil, nil
	case fastJump:
		fr.prevBlock, fr.block = fr.block, fr.block.Succs[0]
		return contJump, nil, nil
	}
	return contNext, nil, fmt.Errorf("interp: unsupported fast instruction kind %d", instr.kind)
}

func (fr *frame) setFastIntSlot(slot int, n int64) {
	fr.cellStorage[slot].Value = value.MakeInt(n)
}

func (fr *frame) setFastBoolSlot(slot int, b bool) {
	fr.cellStorage[slot].Value = value.MakeBool(b)
}

func (fr *frame) readFastBoolSlot(slot int) bool {
	return fr.cellStorage[slot].Value.Bool()
}
