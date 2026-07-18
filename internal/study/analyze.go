package study

import (
	"context"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/scanner"
	"go/token"
	"go/types"
	"sort"
	"strings"
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

	var ids map[ast.Node]int
	result.AST, ids = flattenAST(file, fset, tokFile, source)
	if err := ctx.Err(); err != nil {
		result.Diagnostics = append(result.Diagnostics, Diagnostic{Phase: "context", Message: err.Error()})
		return result
	}

	info, pkg, facts, typeDiags, ok := checkTypes(fset, file, ids, result.AST)
	result.Types = facts
	result.Diagnostics = append(result.Diagnostics, typeDiags...)
	if !ok {
		return result
	}
	if err := ctx.Err(); err != nil {
		result.Diagnostics = append(result.Diagnostics, Diagnostic{Phase: "context", Message: err.Error()})
		return result
	}

	result.Functions = buildSSA(fset, tokFile, file, info, pkg, result.AST, len(source))
	return result
}

func checkTypes(fset *token.FileSet, file *ast.File, ids map[ast.Node]int, nodes []ASTNode) (*types.Info, *types.Package, []TypeFact, []Diagnostic, bool) {
	info := &types.Info{
		Types:      make(map[ast.Expr]types.TypeAndValue),
		Defs:       make(map[*ast.Ident]types.Object),
		Uses:       make(map[*ast.Ident]types.Object),
		Implicits:  make(map[ast.Node]types.Object),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
		Scopes:     make(map[ast.Node]*types.Scope),
		Instances:  make(map[*ast.Ident]types.Instance),
	}
	pkg := types.NewPackage("study", file.Name.Name)
	var diagnostics []Diagnostic
	config := &types.Config{
		Importer: importer.Default(),
		Sizes:    &types.StdSizes{WordSize: 8, MaxAlign: 8},
		Error: func(err error) {
			diagnostics = append(diagnostics, typeDiagnostic(fset, err))
		},
	}
	err := types.NewChecker(config, fset, pkg, info).Files([]*ast.File{file})
	facts := collectTypeFacts(info, ids, nodes)
	return info, pkg, facts, diagnostics, err == nil
}

func typeDiagnostic(fset *token.FileSet, err error) Diagnostic {
	diagnostic := Diagnostic{Phase: "type", Message: err.Error()}
	if typed, ok := err.(types.Error); ok {
		pos := fset.PositionFor(typed.Pos, false)
		diagnostic.Message = typed.Msg
		diagnostic.Line = pos.Line
		diagnostic.Column = pos.Column
	}
	return diagnostic
}

func collectTypeFacts(info *types.Info, ids map[ast.Node]int, nodes []ASTNode) []TypeFact {
	facts := make([]TypeFact, 0, len(info.Types)+len(info.Defs)+len(info.Uses))
	add := func(node ast.Node, fact TypeFact) {
		id, ok := ids[node]
		if !ok || id < 0 || id >= len(nodes) {
			return
		}
		fact.ASTID = id
		fact.Name = nodes[id].Excerpt
		fact.Range = nodes[id].Range
		facts = append(facts, fact)
	}
	for expr, tv := range info.Types {
		fact := TypeFact{Kind: "expression"}
		if tv.Type != nil {
			fact.Type = tv.Type.String()
		}
		if tv.Value != nil {
			fact.Value = tv.Value.ExactString()
		}
		add(expr, fact)
	}
	for ident, object := range info.Defs {
		if object != nil {
			add(ident, TypeFact{Kind: "definition", Type: object.Type().String(), Object: object.String()})
		}
	}
	for ident, object := range info.Uses {
		if object != nil {
			add(ident, TypeFact{Kind: "use", Type: object.Type().String(), Object: object.String()})
		}
	}
	for expr, selection := range info.Selections {
		add(expr, TypeFact{Kind: "selection", Type: selection.Type().String(), Object: selection.String()})
	}
	for ident, instance := range info.Instances {
		add(ident, TypeFact{Kind: "instance", Type: instance.Type.String(), Object: typeArgsString(instance.TypeArgs)})
	}
	sort.Slice(facts, func(i, j int) bool {
		if facts[i].Range.Start != facts[j].Range.Start {
			return facts[i].Range.Start < facts[j].Range.Start
		}
		if facts[i].Kind != facts[j].Kind {
			return facts[i].Kind < facts[j].Kind
		}
		return facts[i].Name < facts[j].Name
	})
	return facts
}

func typeArgsString(arguments *types.TypeList) string {
	if arguments == nil || arguments.Len() == 0 {
		return ""
	}
	items := make([]string, arguments.Len())
	for i := range arguments.Len() {
		items[i] = arguments.At(i).String()
	}
	return "[" + strings.Join(items, ", ") + "]"
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
