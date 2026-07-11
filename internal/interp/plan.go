package interp

import (
	"fmt"
	"go/constant"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/ssa"

	"github.com/t04dJ14n9/gig/value"
)

const missingCell = -1

type planKind uint8

const (
	planGeneric planKind = iota
	planIntBinOp
	planIf
	planJump
	planIntIndexLoad
	planIntIndexStore
)

type valueRef struct {
	cell  int
	value ssa.Value
}

type intRef struct {
	cell       int
	constant   int64
	isConstant bool
}

type phiPlan struct {
	dst   int
	edges []valueRef
}

type plannedOp struct {
	kind             planKind
	instr, consumer  ssa.Instruction
	x, y             intRef
	dst, cond, slice int
	index, stored    intRef
	op               token.Token
}

type blockPlan struct {
	phis []phiPlan
	ops  []plannedOp
}

func collectFrameValues(fn *ssa.Function) ([]ssa.Value, map[ssa.Value]int) {
	values := make([]ssa.Value, 0, len(fn.Params)+len(fn.FreeVars)+len(fn.Locals))
	index := make(map[ssa.Value]int)
	add := func(v ssa.Value) {
		if v == nil {
			return
		}
		if _, exists := index[v]; exists {
			return
		}
		index[v] = len(values)
		values = append(values, v)
	}
	for _, v := range fn.Params {
		add(v)
	}
	for _, v := range fn.FreeVars {
		add(v)
	}
	for _, v := range fn.Locals {
		add(v)
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if v, ok := instr.(ssa.Value); ok {
				add(v)
			}
		}
	}
	return values, index
}

func compileBlockPlan(block *ssa.BasicBlock, cells map[ssa.Value]int) blockPlan {
	plan := blockPlan{
		phis: nil,
		ops:  make([]plannedOp, 0, len(block.Instrs)),
	}

	firstOp := 0
	for firstOp < len(block.Instrs) {
		phi, ok := block.Instrs[firstOp].(*ssa.Phi)
		if !ok {
			break
		}
		plan.phis = append(plan.phis, compilePhiPlan(phi, cells))
		firstOp++
	}

	for i := firstOp; i < len(block.Instrs); i++ {
		instr := block.Instrs[i]
		if _, ok := instr.(*ssa.DebugRef); ok {
			continue
		}
		if indexAddr, ok := instr.(*ssa.IndexAddr); ok {
			consumerIndex := i + 1
			for consumerIndex < len(block.Instrs) {
				if _, ok := block.Instrs[consumerIndex].(*ssa.DebugRef); !ok {
					break
				}
				consumerIndex++
			}
			if consumerIndex < len(block.Instrs) {
				if op, ok := compileIntIndexPair(indexAddr, block.Instrs[consumerIndex], cells); ok {
					plan.ops = append(plan.ops, op)
					i = consumerIndex
					continue
				}
			}
		}
		plan.ops = append(plan.ops, compilePlannedOp(instr, cells))
	}
	return plan
}

func compilePhiPlan(phi *ssa.Phi, cells map[ssa.Value]int) phiPlan {
	plan := phiPlan{
		dst:   missingCell,
		edges: make([]valueRef, len(phi.Edges)),
	}
	if dst, ok := cells[phi]; ok {
		plan.dst = dst
	}
	for i, edge := range phi.Edges {
		ref := valueRef{cell: missingCell, value: edge}
		if cell, ok := cells[edge]; ok {
			ref.cell = cell
			ref.value = nil
		}
		plan.edges[i] = ref
	}
	return plan
}

func compilePlannedOp(instr ssa.Instruction, cells map[ssa.Value]int) plannedOp {
	switch x := instr.(type) {
	case *ssa.BinOp:
		if op, ok := compileIntBinOp(x, cells); ok {
			return op
		}
	case *ssa.If:
		if cond, ok := cells[x.Cond]; ok && isPlainBoolType(x.Cond.Type()) {
			op := emptyPlannedOp(planIf, instr)
			op.cond = cond
			return op
		}
	case *ssa.Jump:
		return emptyPlannedOp(planJump, instr)
	}
	return emptyPlannedOp(planGeneric, instr)
}

func compileIntBinOp(instr *ssa.BinOp, cells map[ssa.Value]int) (plannedOp, bool) {
	op := emptyPlannedOp(planIntBinOp, instr)
	x, ok := intRefFor(instr.X, cells)
	if !ok {
		return op, false
	}
	y, ok := intRefFor(instr.Y, cells)
	if !ok {
		return op, false
	}
	dst, ok := cells[instr]
	if !ok {
		return op, false
	}

	supported := false
	if isPlainIntType(instr.Type()) {
		switch instr.Op {
		case token.ADD, token.SUB, token.MUL, token.QUO, token.REM:
			supported = true
		}
	} else if isPlainBoolType(instr.Type()) {
		switch instr.Op {
		case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
			supported = true
		}
	}
	if !supported {
		return op, false
	}

	op.x = x
	op.y = y
	op.dst = dst
	op.op = instr.Op
	return op, true
}

func compileIntIndexPair(indexAddr *ssa.IndexAddr, consumer ssa.Instruction, cells map[ssa.Value]int) (plannedOp, bool) {
	op := emptyPlannedOp(planGeneric, indexAddr)
	if !fusableIndexAddrConsumer(indexAddr, consumer) || !isPlainIntSliceType(indexAddr.X.Type()) {
		return op, false
	}

	slice, ok := cells[indexAddr.X]
	if !ok {
		return op, false
	}
	index, ok := intRefFor(indexAddr.Index, cells)
	if !ok {
		return op, false
	}

	switch instr := consumer.(type) {
	case *ssa.UnOp:
		dst, ok := cells[instr]
		if !ok || !isPlainIntType(instr.Type()) {
			return op, false
		}
		op = emptyPlannedOp(planIntIndexLoad, indexAddr)
		op.dst = dst
	case *ssa.Store:
		stored, ok := intRefFor(instr.Val, cells)
		if !ok {
			return op, false
		}
		op = emptyPlannedOp(planIntIndexStore, indexAddr)
		op.stored = stored
	default:
		return op, false
	}
	op.consumer = consumer
	op.slice = slice
	op.index = index
	return op, true
}

func emptyPlannedOp(kind planKind, instr ssa.Instruction) plannedOp {
	emptyInt := intRef{cell: missingCell, constant: 0, isConstant: false}
	return plannedOp{
		kind:     kind,
		instr:    instr,
		consumer: nil,
		x:        emptyInt,
		y:        emptyInt,
		dst:      missingCell,
		cond:     missingCell,
		slice:    missingCell,
		index:    emptyInt,
		stored:   emptyInt,
		op:       token.ILLEGAL,
	}
}

func intRefFor(v ssa.Value, cells map[ssa.Value]int) (intRef, bool) {
	ref := intRef{cell: missingCell, constant: 0, isConstant: false}
	if c, ok := v.(*ssa.Const); ok {
		n, ok := intConstValue(c)
		if !ok {
			return ref, false
		}
		ref.constant = n
		ref.isConstant = true
		return ref, true
	}
	if !isPlainIntType(v.Type()) {
		return ref, false
	}
	cell, ok := cells[v]
	if !ok {
		return ref, false
	}
	ref.cell = cell
	return ref, true
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

func (r intRef) read(fr *frame) int64 {
	if r.isConstant {
		return r.constant
	}
	return fr.values[r.cell].Int()
}

func (p *program) runPlannedOp(caller *frame, fr *frame, op plannedOp, depth int) (continuation, []value.Value, error) {
	switch op.kind {
	case planGeneric:
		return p.visitInstr(caller, fr, op.instr, depth)
	case planIntBinOp:
		x := op.x.read(fr)
		y := op.y.read(fr)
		switch op.op {
		case token.ADD:
			fr.values[op.dst] = value.MakeInt(x + y)
		case token.SUB:
			fr.values[op.dst] = value.MakeInt(x - y)
		case token.MUL:
			fr.values[op.dst] = value.MakeInt(x * y)
		case token.QUO:
			fr.values[op.dst] = value.MakeInt(x / y)
		case token.REM:
			fr.values[op.dst] = value.MakeInt(x % y)
		case token.EQL:
			fr.values[op.dst] = value.MakeBool(x == y)
		case token.NEQ:
			fr.values[op.dst] = value.MakeBool(x != y)
		case token.LSS:
			fr.values[op.dst] = value.MakeBool(x < y)
		case token.LEQ:
			fr.values[op.dst] = value.MakeBool(x <= y)
		case token.GTR:
			fr.values[op.dst] = value.MakeBool(x > y)
		case token.GEQ:
			fr.values[op.dst] = value.MakeBool(x >= y)
		default:
			return contNext, nil, fmt.Errorf("interp: unsupported planned int op %s", op.op)
		}
		return contNext, nil, nil
	case planIf:
		succ := 1
		if fr.values[op.cond].Bool() {
			succ = 0
		}
		fr.prevBlock, fr.block = fr.block, fr.block.Succs[succ]
		return contJump, nil, nil
	case planJump:
		fr.prevBlock, fr.block = fr.block, fr.block.Succs[0]
		return contJump, nil, nil
	case planIntIndexLoad, planIntIndexStore:
		s, ok := fr.values[op.slice].IntSlice()
		if !ok {
			cont, results, err := p.visitInstr(caller, fr, op.instr, depth)
			if err != nil || cont != contNext {
				return cont, results, err
			}
			return p.visitInstr(caller, fr, op.consumer, depth)
		}
		idx := int(op.index.read(fr))
		if op.kind == planIntIndexLoad {
			fr.values[op.dst] = value.MakeInt(int64(s[idx]))
		} else {
			s[idx] = int(op.stored.read(fr))
		}
		return contNext, nil, nil
	default:
		return contNext, nil, fmt.Errorf("interp: unsupported plan kind %d", op.kind)
	}
}

func (p *program) runBlockPhis(fr *frame, phis []phiPlan) error {
	if len(phis) == 0 {
		return nil
	}

	edge := missingCell
	for i, pred := range fr.block.Preds {
		if pred == fr.prevBlock {
			edge = i
			break
		}
	}
	if edge == missingCell {
		return fmt.Errorf("interp: %s: phi at block %d has no matching predecessor edge", fr.fn.Name(), fr.block.Index)
	}

	var stagedBuf [8]value.Value
	staged := stagedBuf[:]
	if len(phis) > len(stagedBuf) {
		staged = make([]value.Value, len(phis))
	} else {
		staged = staged[:len(phis)]
	}
	for i, phi := range phis {
		if phi.dst == missingCell || edge >= len(phi.edges) {
			return fmt.Errorf("interp: %s: invalid phi plan at block %d", fr.fn.Name(), fr.block.Index)
		}
		ref := phi.edges[edge]
		if ref.cell != missingCell {
			staged[i] = fr.values[ref.cell]
			continue
		}
		v, err := p.readValue(fr, ref.value)
		if err != nil {
			return err
		}
		staged[i] = v
	}
	for i, phi := range phis {
		fr.values[phi.dst] = staged[i]
	}
	return nil
}
