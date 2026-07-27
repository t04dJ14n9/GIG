// Package interp is the direct SSA interpreter. It walks the SSA tree
// produced by frontend, mapping each ssa.Value through a shared layout index
// to a compact frame-local value slot and dispatching on instruction type.
// Mutable values live directly in frame or global storage; addressability is
// carried by reflected pointer values stored in those slots.
// There is no bytecode, no opcode table, and no VM pool; see docs/PLAN.md for
// rationale.
//
// This file holds the package's public surface: the Engine and Program
// interfaces plus their Config. Execution lives in frame.go (dispatch
// loop), plan.go (per-block execution plans), and ops.go (instruction
// handlers).
package interp

import (
	"context"

	"github.com/t04dJ14n9/gig/host"
	"github.com/t04dJ14n9/gig/internal/frontend"
	"github.com/t04dJ14n9/gig/value"
)

// Config bundles the toggles the interp engine honours.
//
// The interpreter has a single global state model: package-level
// globals are allocated once when the Program is built, init() runs
// once, and every Call observes/mutates the same globals — exactly as
// in a compiled Go program. Callers that want isolation between
// requests should compile a fresh Program per request rather than
// asking the interpreter to reset.
type Config struct {
	MaxDepth int
}

// Engine constructs Programs from compiled Units.
type Engine interface {
	NewProgram(ctx context.Context, unit frontend.Unit, env host.Environment, cfg Config) (Program, error)
}

// Program is the executable form of a Unit. Call returns []value.Value
// (zero, one, or many results); the public gig.Program wraps this and
// converts to []any.
type Program interface {
	Call(ctx context.Context, name string, args []value.Value) ([]value.Value, error)
}

// frame is the per-call activation record. It is unexported because
// nothing outside interp constructs one; users go through Engine.
// Concrete fields are defined in frame.go.
