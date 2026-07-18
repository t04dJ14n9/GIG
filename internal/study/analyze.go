package study

import (
	"context"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
)

const analysisFilename = "study.go"

// Analyze runs as many source-analysis phases as possible and returns partial
// results alongside diagnostics when a later phase fails.
func Analyze(ctx context.Context, source string) Result {
	result := Result{Source: source}
	if err := ctx.Err(); err != nil {
		result.Diagnostics = append(result.Diagnostics, Diagnostic{Phase: "context", Message: err.Error()})
		return result
	}

	fset := token.NewFileSet()
	file, parseErr := parser.ParseFile(fset, analysisFilename, source,
		parser.AllErrors|parser.ParseComments|parser.SkipObjectResolution)
	tokFile := parsedTokenFile(fset, file)
	result.Tokens, result.Diagnostics = scanSource(fset, tokFile, source, result.Diagnostics)
	result.Diagnostics = appendScannerDiagnostics(result.Diagnostics, "parse", parseErr)
	if file == nil || tokFile == nil {
		return result
	}

	result.AST, _ = flattenAST(file, fset, tokFile, source)
	return result
}

func parsedTokenFile(fset *token.FileSet, file *ast.File) *token.File {
	if file != nil && file.Pos().IsValid() {
		if f := fset.File(file.Pos()); f != nil {
			return f
		}
	}
	var found *token.File
	fset.Iterate(func(file *token.File) bool {
		found = file
		return false
	})
	return found
}

func scanSource(fset *token.FileSet, file *token.File, source string, diags []Diagnostic) ([]Token, []Diagnostic) {
	if file == nil {
		return nil, diags
	}
	var s scanner.Scanner
	s.Init(file, []byte(source), func(pos token.Position, message string) {
		diags = append(diags, Diagnostic{Phase: "scan", Message: message, Line: pos.Line, Column: pos.Column})
	}, scanner.ScanComments)

	tokens := make([]Token, 0, 64)
	for {
		pos, kind, literal := s.Scan()
		if kind == token.EOF {
			break
		}
		text := literal
		label := kind.String()
		if text == "" {
			text = kind.String()
		}
		if kind == token.SEMICOLON && literal == "\n" {
			text = ""
			label = "semicolon (inserted)"
		}
		end := token.Pos(int(pos) + len(text))
		tokens = append(tokens, Token{Kind: label, Text: text, Range: sourceRange(fset, file, pos, end, len(source))})
	}
	return tokens, diags
}

func appendScannerDiagnostics(dst []Diagnostic, phase string, err error) []Diagnostic {
	if err == nil {
		return dst
	}
	if list, ok := err.(scanner.ErrorList); ok {
		for _, item := range list {
			dst = append(dst, Diagnostic{Phase: phase, Message: item.Msg, Line: item.Pos.Line, Column: item.Pos.Column})
		}
		return dst
	}
	return append(dst, Diagnostic{Phase: phase, Message: err.Error()})
}
