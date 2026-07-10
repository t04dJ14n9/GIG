// builder.go is the default frontend.Builder implementation. It runs
// parse, validation, type-check, and SSA construction in sequence,
// returning a Unit on success. Diagnostics from go/types are collected
// and surfaced through Unit.Diagnostics().
//
// The pipeline is:
//
//  1. Auto-wrap with "package main" when missing.
//  2. parser.ParseFile  (go/parser).
//  3. Reject banned imports (unsafe, reflect).
//  4. Reject panic() if cfg.Panic == PanicReject.
//  5. Auto-import registered packages by identifier scan.
//  6. types.Config.Check  (go/types) — host.Environment is the Importer.
//  7. ssautil.BuildPackage — produces the SSA package.
package frontend

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/ssa"

	"github.com/t04dJ14n9/gig/diag"
	"github.com/t04dJ14n9/gig/host"
)

// DefaultBannedImports is the security baseline matching legacy gig.
var DefaultBannedImports = []string{"unsafe", "reflect"}

// NewBuilder returns the default Builder. It is stateless and safe for
// concurrent use.
func NewBuilder() Builder { return defaultBuilder{} }

type defaultBuilder struct{}

// Build runs the pipeline. ctx is honoured between phases (callers can
// cancel). On error, the returned Unit may be nil; on success the Unit
// always has a non-nil SSA package.
func (defaultBuilder) Build(ctx context.Context, src Source, env host.Environment, cfg Config) (Unit, error) {
	if env == nil {
		return nil, fmt.Errorf("frontend: nil host.Environment")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	source := wrapPackageMain(src.Content)
	filename := src.Filename
	if filename == "" {
		filename = "main.go"
	}
	pkgPath := src.PackagePath
	if pkgPath == "" {
		pkgPath = "main"
	}

	fset := token.NewFileSet()
	file, err := parseSourceFile(fset, filename, source)
	if err != nil {
		return nil, fmt.Errorf("frontend: parse: %w", err)
	}

	banned := cfg.BannedImports
	if banned == nil {
		banned = DefaultBannedImports
	}
	if d := checkBannedImports(fset, file, banned); d != nil {
		return nil, d
	}
	if cfg.Panic == PanicReject {
		if d := checkBannedPanic(fset, file); d != nil {
			return nil, d
		}
	}
	if cfg.AutoImport {
		injectAutoImports(file, env)
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var diags []diag.Diagnostic
	typeCfg := &types.Config{
		Importer: env,
		Sizes:    &types.StdSizes{WordSize: 8, MaxAlign: 8},
		Error: func(err error) {
			diags = append(diags, typesErrorToDiag(err))
		},
	}
	info := newTypesInfo()
	pkg := types.NewPackage(pkgPath, file.Name.Name)

	if err := types.NewChecker(typeCfg, fset, pkg, info).Files([]*ast.File{file}); err != nil {
		if len(diags) > 0 {
			return nil, &BuildError{Diags: diags}
		}
		return nil, fmt.Errorf("frontend: typecheck: %w", err)
	}

	// Reject interpreted struct types flowing into host functions
	// expecting a non-empty interface parameter. The interpreter does
	// not synthesise host-interface proxies for interpreted types
	// (G_iface_ban); detecting this at the frontend gives the user a
	// clear, deterministic error instead of a runtime reflect panic.
	if d := checkHostInterfaceBoundary(fset, file, info, pkg); d != nil {
		return nil, d
	}

	prog := ssa.NewProgram(fset, ssa.SanityCheckFunctions|ssa.BareInits)
	for _, imp := range pkg.Imports() {
		prog.CreatePackage(imp, nil, nil, true)
	}
	ssaPkg := prog.CreatePackage(pkg, []*ast.File{file}, info, false)
	if ssaPkg == nil {
		return nil, fmt.Errorf("frontend: SSA package creation failed")
	}
	ssaPkg.Build()

	return &unit{
		ssaPkg: ssaPkg,
		fset:   fset,
		diags:  diags,
	}, nil
}

// --- Unit -------------------------------------------------------------------

type unit struct {
	ssaPkg *ssa.Package
	fset   *token.FileSet
	diags  []diag.Diagnostic
}

func (u *unit) Package() *ssa.Package          { return u.ssaPkg }
func (u *unit) FileSet() *token.FileSet        { return u.fset }
func (u *unit) Diagnostics() []diag.Diagnostic { return u.diags }

// BuildError aggregates multiple type-check diagnostics so callers can see
// every problem, not just the first.
type BuildError struct {
	Diags []diag.Diagnostic
}

func (e *BuildError) Error() string {
	if len(e.Diags) == 0 {
		return "frontend: build failed"
	}
	parts := make([]string, len(e.Diags))
	for i, d := range e.Diags {
		parts[i] = d.Error()
	}
	return "frontend: " + strings.Join(parts, "; ")
}

// --- helpers ----------------------------------------------------------------

func parseSourceFile(fset *token.FileSet, filename, source string) (*ast.File, error) {
	const mode = parser.AllErrors | parser.ParseComments | parser.SkipObjectResolution
	return parser.ParseFile(fset, filename, source, mode)
}

func wrapPackageMain(src string) string {
	src = strings.TrimSpace(src)
	if !hasPackageClause(src) {
		src = "package main\n\n" + src
	}
	return src
}

func hasPackageClause(src string) bool {
	if src == "" {
		return false
	}
	file, err := parser.ParseFile(
		token.NewFileSet(),
		"",
		src,
		parser.PackageClauseOnly|parser.SkipObjectResolution,
	)
	return err == nil && file != nil && file.Name != nil
}

func checkBannedImports(fset *token.FileSet, file *ast.File, banned []string) error {
	bannedSet := make(map[string]struct{}, len(banned))
	for _, b := range banned {
		bannedSet[b] = struct{}{}
	}
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if _, ok := bannedSet[path]; ok {
			pos := fset.Position(imp.Pos())
			return fmt.Errorf("frontend: import of %q is banned (%s)", path, pos)
		}
	}
	return nil
}

func checkBannedPanic(fset *token.FileSet, file *ast.File) error {
	var panicPos token.Pos
	ast.Inspect(file, func(n ast.Node) bool {
		if panicPos.IsValid() {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "panic" {
			panicPos = ident.Pos()
			return false
		}
		return true
	})
	if panicPos.IsValid() {
		pos := fset.Position(panicPos)
		return fmt.Errorf("frontend: panic() not allowed (%s); set Config.Panic = PanicAllow", pos)
	}
	return nil
}

// injectAutoImports adds import statements for unresolved identifiers
// that env.AutoImport claims belong to a known package. It mirrors
// gofun's autoImport plus the legacy gig behaviour: the host gets to
// say "ident X means package P" and we splice the import in.
func injectAutoImports(file *ast.File, env host.Environment) {
	already := make(map[string]bool)
	for _, imp := range file.Imports {
		already[strings.Trim(imp.Path.Value, `"`)] = true
	}
	used := make(map[string]string) // path -> name
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if imp, ok := env.AutoImport(ident.Name); ok {
			used[imp.Path] = imp.Name
		}
		return true
	})
	for path, name := range used {
		if already[path] {
			continue
		}
		spec := &ast.ImportSpec{
			Path: &ast.BasicLit{Kind: token.STRING, Value: `"` + path + `"`},
		}
		decl := &ast.GenDecl{Tok: token.IMPORT, Specs: []ast.Spec{spec}}
		file.Decls = append([]ast.Decl{decl}, file.Decls...)
		file.Imports = append(file.Imports, spec)
		already[path] = true
		_ = name // currently unused; preserved for future named-import support
	}
}

func typesErrorToDiag(err error) diag.Diagnostic {
	var te types.Error
	if errors.As(err, &te) {
		return diag.Diagnostic{
			Severity: diag.SeverityError,
			Pos:      te.Fset.Position(te.Pos),
			Message:  te.Msg,
		}
	}
	return diag.Diagnostic{
		Severity: diag.SeverityError,
		Message:  err.Error(),
	}
}

func newTypesInfo() *types.Info {
	return &types.Info{
		Types:      make(map[ast.Expr]types.TypeAndValue),
		Defs:       make(map[*ast.Ident]types.Object),
		Uses:       make(map[*ast.Ident]types.Object),
		Implicits:  make(map[ast.Node]types.Object),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
		Scopes:     make(map[ast.Node]*types.Scope),
	}
}
