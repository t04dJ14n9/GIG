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

func TestAnalyzeTypesExposeConstantsAndObjectIdentity(t *testing.T) {
	t.Parallel()

	const source = `package main

const folded = 2 + 3

func add(x int) int {
	return x + folded
}
`
	result := Analyze(context.Background(), source)
	if len(result.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics)
	}

	foundConstant := false
	objects := make(map[string]bool)
	for _, fact := range result.Types {
		if strings.ReplaceAll(fact.Name, " ", "") == "2+3" && fact.Value == "5" {
			foundConstant = true
		}
		if fact.Name == "x" && (fact.Kind == "definition" || fact.Kind == "use") {
			objects[fact.Object] = true
		}
	}
	if !foundConstant {
		t.Fatalf("constant expression fact with exact value 5 not found: %#v", result.Types)
	}
	if len(objects) != 1 {
		t.Fatalf("x definition/use objects = %#v, want one shared identity", objects)
	}
}

func TestAnalyzeSSABuildsBranchAndPhi(t *testing.T) {
	t.Parallel()

	const source = `package main

func choose(cond bool) int {
	x := 1
	if cond {
		x = 2
	} else {
		x = 3
	}
	return x
}
`
	result := Analyze(context.Background(), source)
	if len(result.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %#v", result.Diagnostics)
	}

	var foundIf, foundPhi bool
	for _, fn := range result.Functions {
		if fn.Name != "choose" {
			continue
		}
		for _, block := range fn.Blocks {
			for _, instruction := range block.Instructions {
				foundIf = foundIf || instruction.Kind == "If"
				foundPhi = foundPhi || instruction.Kind == "Phi"
			}
		}
	}
	if !foundIf || !foundPhi {
		t.Fatalf("choose instructions missing If/Phi: If=%v Phi=%v functions=%#v", foundIf, foundPhi, result.Functions)
	}
}

func TestAnalyzeErrorsPreserveEarlierStages(t *testing.T) {
	t.Parallel()

	t.Run("syntax", func(t *testing.T) {
		result := Analyze(context.Background(), "package main\nfunc broken( {\n")
		if len(result.Tokens) == 0 {
			t.Fatal("syntax error discarded tokens")
		}
		if !hasDiagnostic(result.Diagnostics, "parse") {
			t.Fatalf("parse diagnostic not found: %#v", result.Diagnostics)
		}
	})

	t.Run("type", func(t *testing.T) {
		const source = "package main\nfunc broken() int { return missing }\n"
		result := Analyze(context.Background(), source)
		if len(result.AST) == 0 {
			t.Fatal("type error discarded AST")
		}
		if len(result.Functions) != 0 {
			t.Fatalf("type-invalid source produced SSA: %#v", result.Functions)
		}
		if !hasDiagnostic(result.Diagnostics, "type") {
			t.Fatalf("type diagnostic not found: %#v", result.Diagnostics)
		}
	})
}

func hasDiagnostic(diagnostics []Diagnostic, phase string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Phase == phase {
			return true
		}
	}
	return false
}
