// Package gig provides a Go interpreter backed by an SSA tree-walking
// engine. Embed Go source in a Go application for rule engines,
// scripting, and embedded logic.
//
// # Pipeline
//
//	Go source -> go/parser -> go/types -> go/ssa -> direct SSA interpreter
//
// # Quick start
//
//	prog, err := gig.Build(`
//		func Add(a, b int) int { return a + b }
//	`)
//	if err != nil { panic(err) }
//	result, _ := prog.Run("Add", 1, 2) // 3
package gig

import (
	"context"
	"fmt"
	"time"

	"github.com/t04dJ14n9/gig/host"
	"github.com/t04dJ14n9/gig/importer"
	"github.com/t04dJ14n9/gig/internal/frontend"
	"github.com/t04dJ14n9/gig/internal/interp"
	"github.com/t04dJ14n9/gig/value"
)

// DefaultTimeout is the default execution timeout for Run.
const DefaultTimeout = 10 * time.Second

// ErrTimeout is returned when execution times out.
var ErrTimeout = context.DeadlineExceeded

// buildConfig holds internal configuration parsed from BuildOption values.
type buildConfig struct {
	registry   importer.PackageRegistry
	allowPanic bool
}

// BuildOption configures the behaviour of Build.
type BuildOption func(*buildConfig)

// WithRegistry sets a custom PackageRegistry for resolving external packages.
func WithRegistry(r importer.PackageRegistry) BuildOption {
	return func(c *buildConfig) { c.registry = r }
}

// WithAllowPanic allows the use of panic() in interpreted code.
// By default panic() is rejected at compile time. When enabled,
// panic/recover/defer work as in standard Go.
func WithAllowPanic() BuildOption {
	return func(c *buildConfig) { c.allowPanic = true }
}

// Program represents a compiled Go program ready for execution.
type Program struct {
	prog       interp.Program
	allowPanic bool
}

// Build compiles Go source code into a Program.
func Build(sourceCode string, opts ...BuildOption) (*Program, error) {
	cfg := buildConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.registry == nil {
		cfg.registry = importer.GlobalRegistry()
	}

	ctx := context.Background()
	env := host.FromRegistry(cfg.registry)
	fcfg := frontend.Config{AutoImport: true}
	if cfg.allowPanic {
		fcfg.Panic = frontend.PanicAllow
	}
	unit, err := frontend.NewBuilder().Build(ctx, frontend.Source{Content: sourceCode}, env, fcfg)
	if err != nil {
		return nil, err
	}
	prog, err := interp.NewEngine().NewProgram(ctx, unit, env, interp.Config{})
	if err != nil {
		return nil, err
	}
	return &Program{prog: prog, allowPanic: cfg.allowPanic}, nil
}

// Close releases resources associated with the Program.
// The v2 interpreter has no global registries to unwind; this is a
// no-op kept for source compatibility.
func (p *Program) Close() {}

// Run executes a function with the default timeout.
func (p *Program) Run(funcName string, params ...any) (any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancel()
	return p.run(ctx, funcName, params...)
}

// RunWithContext executes a function with the given context. Cancellation is
// observed by interpreter-owned execution, including blocking channel
// operations. Registered host functions must implement their own cancellation,
// normally through an explicit context.Context argument.
func (p *Program) RunWithContext(ctx context.Context, funcName string, params ...any) (any, error) {
	return p.run(ctx, funcName, params...)
}

// run is the shared dispatch path. It converts arguments, calls the
// interpreter program, and unwraps results to the legacy any/[]any/nil
// shape Run/RunWithContext are documented to return.
func (p *Program) run(ctx context.Context, funcName string, params ...any) (a any, err error) {
	defer func() {
		if re := recover(); re != nil {
			err = fmt.Errorf("interpreter panic: %v", re)
		}
	}()
	conv := value.DefaultConverter()
	args := make([]value.Value, len(params))
	for i, p := range params {
		v, ferr := conv.FromAny(p)
		if ferr != nil {
			return nil, fmt.Errorf("gig: convert arg %d: %w", i, ferr)
		}
		args[i] = v
	}
	results, err := p.prog.Call(ctx, funcName, args)
	if err != nil {
		return nil, err
	}
	switch len(results) {
	case 0:
		return nil, nil //nolint:nilnil // Zero-result interpreted functions return no value and no error.
	case 1:
		out, cerr := conv.ToAny(results[0])
		if cerr != nil {
			return nil, cerr
		}
		return out, nil
	default:
		out := make([]any, len(results))
		for i, r := range results {
			any_, cerr := conv.ToAny(r)
			if cerr != nil {
				return nil, cerr
			}
			out[i] = any_
		}
		return out, nil
	}
}

// NewSandboxRegistry creates a fresh, empty PackageRegistry for sandboxed execution.
func NewSandboxRegistry() importer.PackageRegistry {
	return importer.NewRegistry()
}

// RegisterPackage registers an external package for use in interpreted code.
func RegisterPackage(path, name string) *importer.ExternalPackage {
	return importer.RegisterPackage(path, name)
}

// GetPackageByPath returns a registered package by import path.
func GetPackageByPath(path string) *importer.ExternalPackage {
	return importer.GetPackageByPath(path)
}

// GetPackageByName returns a registered package by name.
func GetPackageByName(name string) *importer.ExternalPackage {
	return importer.GetPackageByName(name)
}

// GetAllPackages returns all registered packages.
func GetAllPackages() map[string]*importer.ExternalPackage {
	return importer.GetAllPackages()
}
