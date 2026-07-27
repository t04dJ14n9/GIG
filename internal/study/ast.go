package study

import (
	"go/ast"
	"go/token"
	"reflect"
)

func flattenAST(file *ast.File, fset *token.FileSet, tokFile *token.File, source string) ([]ASTNode, map[ast.Node]int) {
	nodes := make([]ASTNode, 0, 64)
	ids := make(map[ast.Node]int)
	stack := make([]int, 0, 16)

	ast.Inspect(file, func(node ast.Node) bool {
		if node == nil {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			return true
		}

		id := len(nodes)
		parent := -1
		if len(stack) > 0 {
			parent = stack[len(stack)-1]
		}
		rng := sourceRange(fset, tokFile, node.Pos(), node.End(), len(source))
		nodes = append(nodes, ASTNode{
			ID:      id,
			Parent:  parent,
			Depth:   len(stack),
			Kind:    reflect.TypeOf(node).Elem().Name(),
			Excerpt: source[rng.Start:rng.End],
			Range:   rng,
		})
		ids[node] = id
		stack = append(stack, id)
		return true
	})

	return nodes, ids
}

func sourceRange(fset *token.FileSet, file *token.File, start, end token.Pos, sourceLen int) SourceRange {
	startOffset := offset(file, start, sourceLen)
	endOffset := offset(file, end, sourceLen)
	if endOffset < startOffset {
		endOffset = startOffset
	}
	startPos := fset.PositionFor(start, false)
	endPos := fset.PositionFor(end, false)
	return SourceRange{
		Start:     startOffset,
		End:       endOffset,
		Line:      startPos.Line,
		Column:    startPos.Column,
		EndLine:   endPos.Line,
		EndColumn: endPos.Column,
	}
}

func offset(file *token.File, pos token.Pos, sourceLen int) int {
	if file == nil || !pos.IsValid() {
		return 0
	}
	n := file.Offset(pos)
	if n < 0 {
		return 0
	}
	if n > sourceLen {
		return sourceLen
	}
	return n
}
