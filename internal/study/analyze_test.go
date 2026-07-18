package study

import (
	"context"
	"strings"
	"testing"
)

func TestAnalyzeTokensUseParserFilePositions(t *testing.T) {
	t.Parallel()

	const source = "package main\n\nfunc add(a, b int) int { return a + b }\n"
	result := Analyze(context.Background(), source)

	var funcToken *Token
	for i := range result.Tokens {
		tok := &result.Tokens[i]
		if tok.Kind == "func" {
			funcToken = tok
			break
		}
	}
	if funcToken == nil {
		t.Fatal("func token not found")
	}
	if got, want := funcToken.Range.Start, 14; got != want {
		t.Fatalf("func token offset = %d, want %d", got, want)
	}
	if got, want := funcToken.Range.Line, 3; got != want {
		t.Fatalf("func token line = %d, want %d", got, want)
	}
	if got, want := funcToken.Range.Column, 1; got != want {
		t.Fatalf("func token column = %d, want %d", got, want)
	}

	for _, tok := range result.Tokens {
		if tok.Range.Start < 0 || tok.Range.End < tok.Range.Start || tok.Range.End > len(source) {
			t.Fatalf("invalid range for token %#v", tok)
		}
		if got := source[tok.Range.Start:tok.Range.End]; got != tok.Text {
			t.Fatalf("token range slices %q, token text is %q", got, tok.Text)
		}
	}
}

func TestAnalyzeASTRespectsBinaryPrecedence(t *testing.T) {
	t.Parallel()

	const source = `package main

func calc(a, b, c int) int {
	return a + b*c
}
`
	result := Analyze(context.Background(), source)

	var outer, inner *ASTNode
	for i := range result.AST {
		node := &result.AST[i]
		if node.Kind != "BinaryExpr" {
			continue
		}
		switch strings.ReplaceAll(node.Excerpt, " ", "") {
		case "a+b*c":
			outer = node
		case "b*c":
			inner = node
		}
	}
	if outer == nil || inner == nil {
		t.Fatalf("BinaryExpr nodes not found: outer=%v inner=%v AST=%#v", outer, inner, result.AST)
	}
	if inner.Parent != outer.ID {
		t.Fatalf("b*c parent = %d, want outer BinaryExpr ID %d", inner.Parent, outer.ID)
	}
	if inner.Depth != outer.Depth+1 {
		t.Fatalf("b*c depth = %d, want %d", inner.Depth, outer.Depth+1)
	}
}
