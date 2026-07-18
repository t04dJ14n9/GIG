package study

import (
	"go/ast"
	"go/token"
	"go/types"
	"reflect"
	"sort"
	"strings"

	"golang.org/x/tools/go/ssa"
)

func buildSSA(fset *token.FileSet, tokFile *token.File, file *ast.File, info *types.Info, pkg *types.Package, nodes []ASTNode, sourceLen int) []Function {
	program := ssa.NewProgram(fset, ssa.SanityCheckFunctions)
	for _, imported := range pkg.Imports() {
		program.CreatePackage(imported, nil, nil, true)
	}
	ssaPackage := program.CreatePackage(pkg, []*ast.File{file}, info, false)
	ssaPackage.Build()

	var roots []*ssa.Function
	for _, member := range ssaPackage.Members {
		if function, ok := member.(*ssa.Function); ok {
			roots = append(roots, function)
		}
	}
	functions := collectFunctions(roots)
	result := make([]Function, 0, len(functions))
	for _, function := range functions {
		result = append(result, presentFunction(function, fset, tokFile, nodes, sourceLen))
	}
	return result
}

func collectFunctions(roots []*ssa.Function) []*ssa.Function {
	seen := make(map[*ssa.Function]bool)
	var all []*ssa.Function
	var visit func(*ssa.Function)
	visit = func(function *ssa.Function) {
		if function == nil || seen[function] {
			return
		}
		seen[function] = true
		all = append(all, function)
		for _, anonymous := range function.AnonFuncs {
			visit(anonymous)
		}
	}
	for _, root := range roots {
		visit(root)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].String() < all[j].String() })
	return all
}

func presentFunction(function *ssa.Function, fset *token.FileSet, tokFile *token.File, nodes []ASTNode, sourceLen int) Function {
	var raw strings.Builder
	_, _ = function.WriteTo(&raw)
	presented := Function{Name: function.Name(), Signature: function.Signature.String(), Raw: raw.String()}
	for _, block := range function.Blocks {
		item := BasicBlock{Index: block.Index, Comment: block.Comment}
		for _, pred := range block.Preds {
			item.Preds = append(item.Preds, pred.Index)
		}
		for _, succ := range block.Succs {
			item.Succs = append(item.Succs, succ.Index)
		}
		for _, instruction := range block.Instrs {
			item.Instructions = append(item.Instructions, presentInstruction(instruction, fset, tokFile, nodes, sourceLen))
		}
		presented.Blocks = append(presented.Blocks, item)
	}
	return presented
}

func presentInstruction(instruction ssa.Instruction, fset *token.FileSet, tokFile *token.File, nodes []ASTNode, sourceLen int) Instruction {
	typeOf := reflect.TypeOf(instruction)
	kind := typeOf.Name()
	if typeOf.Kind() == reflect.Pointer {
		kind = typeOf.Elem().Name()
	}
	presented := Instruction{Kind: kind, Text: instruction.String(), ASTID: -1}
	if value, ok := instruction.(ssa.Value); ok {
		presented.Result = value.Name()
		if value.Type() != nil {
			presented.Type = value.Type().String()
		}
	}
	for _, operand := range instruction.Operands(nil) {
		if operand == nil || *operand == nil {
			continue
		}
		value := *operand
		label := value.Name()
		if label == "" {
			label = value.String()
		}
		if value.Type() != nil {
			label += ": " + value.Type().String()
		}
		presented.Operands = append(presented.Operands, label)
	}
	if instruction.Pos().IsValid() {
		position := offset(tokFile, instruction.Pos(), sourceLen)
		best := -1
		bestWidth := sourceLen + 1
		for i := range nodes {
			node := &nodes[i]
			if node.Range.Start <= position && position < node.Range.End {
				if width := node.Range.End - node.Range.Start; width < bestWidth {
					best = i
					bestWidth = width
				}
			}
		}
		if best >= 0 {
			presented.ASTID = nodes[best].ID
			presented.Range = nodes[best].Range
		} else {
			presented.Range = sourceRange(fset, tokFile, instruction.Pos(), instruction.Pos(), sourceLen)
		}
	}
	return presented
}
