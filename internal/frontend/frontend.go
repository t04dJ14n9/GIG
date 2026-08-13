// Package frontend turns Go source into SSA. It owns parse, type-check,
// validation (banned imports, panic policy), auto-package insertion,
// and SSA construction. It produces no bytecode.
package frontend

import (
	"context"
	"go/token"

	"golang.org/x/tools/go/ssa"

	"github.com/t04dJ14n9/gig/diag"
	"github.com/t04dJ14n9/gig/host"
)

// Source identifies what to compile. PackagePath is "main" for the
// common single-file case.
type Source struct {
	Filename    string
	Content     string
	PackagePath string
}

// PanicPolicy controls how panic() in interpreted code is handled.
type PanicPolicy int

const (
	// PanicReject rejects programs that mention panic at compile time
	// (current default behaviour of gig.WithAllowPanic absent).
	PanicReject PanicPolicy = iota
	// PanicAllow lets panic() through to the interpreter.
	PanicAllow
)

// Config bundles the toggles the frontend honours. Defaults are
// the legacy gig defaults (banned imports = unsafe/reflect, panic
// rejected) so existing behaviour can be preserved.
type Config struct {
	BannedImports []string
	Panic         PanicPolicy
	AutoImport    bool
}

// Builder is the only entry point this package exposes. The default
// implementation lives in builder.go (Phase 4).
type Builder interface {
	Build(ctx context.Context, src Source, env host.Environment, cfg Config) (Unit, error)
}

// Unit is the artefact handed to interp.Engine.NewProgram. It carries
// the SSA package, the FileSet for diagnostic positions, and the
// non-fatal diagnostics raised during construction.
type Unit interface {
	Package() *ssa.Package
	FileSet() *token.FileSet
	Diagnostics() []diag.Diagnostic
}
