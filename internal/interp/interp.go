// Package interp is the direct SSA interpreter. It walks the SSA tree
// produced by frontend, mapping each ssa.Value to a *Cell and dispatching
// on instruction type. There is no bytecode, no opcode table, and no VM
// pool; see docs/PLAN.md for rationale.
//
// Phase 1 ships only the type and interface declarations. The instruction
// dispatch, frame execution loop, defer/panic/recover, and goroutines all
// land in Phase 6.
package interp

import (
	"context"
	"go/types"

	"github.com/t04dJ14n9/gig/host"
	"github.com/t04dJ14n9/gig/internal/frontend"
	"github.com/t04dJ14n9/gig/value"
)

// Config bundles the toggles the interp engine honours.
//
// The interpreter has a single global state model: package-level
// globals are allocated once when the Program is built, init() runs
// once, and every Call observes/mutates the same globals. This matches
// real Go semantics and gofun's behaviour. Callers that want isolation
// between requests should compile a fresh Program per request rather
// than asking the interpreter to reset.
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

// Cell is the interpreter's mutable storage unit. Every ssa.Value that
// can be read or written points to a Cell; this is what gives the
// interpreter addressable semantics without putting mutability into
// value.Value itself.
type Cell struct {
	Name  string
	Type  types.Type
	Value value.Value
}

// frame is the per-call activation record. It is unexported because
// nothing outside interp constructs one; users go through Engine.
// Concrete fields are defined in frame.go.
