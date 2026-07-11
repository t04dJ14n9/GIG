package interp

import "golang.org/x/tools/go/ssa"

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
