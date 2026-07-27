// engine.go is the default interp.Engine implementation. It builds
// Programs from frontend Units — allocating globals and running init()
// once at construction — and exposes Call, the per-invocation entry
// point that resolves a function by name and runs it.
package interp

import (
	"context"
	"fmt"
	"sync"

	"go/types"

	"golang.org/x/tools/go/ssa"

	"github.com/t04dJ14n9/gig/host"
	"github.com/t04dJ14n9/gig/internal/frontend"
	"github.com/t04dJ14n9/gig/value"
)

// NewEngine returns the default Engine. It is stateless and safe for
// concurrent use; per-program state lives in the Program returned by
// NewProgram.
func NewEngine() Engine { return defaultEngine{} }

type defaultEngine struct{}

func (defaultEngine) NewProgram(ctx context.Context, unit frontend.Unit, env host.Environment, cfg Config) (Program, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if unit == nil {
		return nil, fmt.Errorf("interp: nil Unit")
	}
	if unit.Package() == nil {
		return nil, fmt.Errorf("interp: Unit has no SSA package")
	}
	maxDepth := cfg.MaxDepth
	if maxDepth <= 0 {
		maxDepth = defaultMaxDepth
	}
	p := &program{
		ssaPkg:    unit.Package(),
		env:       env,
		converter: value.DefaultConverter(),
		resolver:  newTypeResolver(env, unit.Package().Pkg.Path()),
		globals:   map[*ssa.Global]value.Value{},
		maxDepth:  maxDepth,
	}
	if err := p.allocateGlobals(); err != nil {
		return nil, err
	}
	// init() runs once at construction so the Go semantics of
	// package-level initialisation are honoured: global initialisers
	// and init() bodies execute before the first Call.
	if err := p.runInit(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

const defaultMaxDepth = 1024

// program is the running Program. Mutable package globals live directly in
// globals, allocated once at construction time as in compiled Go.
// Addressability for locals and composite data lives inside reflected
// pointer values rather than in a second storage wrapper.
type program struct {
	ssaPkg      *ssa.Package
	env         host.Environment
	converter   value.Converter
	resolver    *typeResolver
	globalsMu   sync.RWMutex
	globals     map[*ssa.Global]value.Value
	maxDepth    int
	hostFuncs   sync.Map // map[*ssa.Function]host.Function
	hostMethods sync.Map // map[hostMethodCacheKey]host.Method or missingHostMethod
	layouts     sync.Map // map[*ssa.Function]*frameLayout
}

// Call resolves the named function and runs it with the given args.
// args are converted to value.Value-shape values by the caller; this
// method does not look at any. The return is the SSA function's
// result tuple, flattened to a slice.
//
// Panics that propagate out of the interpreted call (after all defer
// chains have been consulted) are caught here and surfaced as errors
// to the embedder.
func (p *program) Call(ctx context.Context, name string, args []value.Value) (results []value.Value, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	fn := p.ssaPkg.Func(name)
	if fn == nil {
		return nil, fmt.Errorf("interp: function %q not found", name)
	}
	if got, want := len(args), len(fn.Params); got != want {
		return nil, fmt.Errorf("interp: %q expects %d args, got %d", name, want, got)
	}
	defer func() {
		if re := recover(); re != nil {
			err = fmt.Errorf("interpreter panic: %v", re)
			results = nil
		}
	}()
	results, err = p.callSSA(ctx, nil, fn, args, nil, 0)
	return results, err
}

// allocateGlobals walks every Global in the SSA package and stores its zero
// value directly. The interpreter replaces that value when it sees `Store` to
// a *ssa.Global.
func (p *program) allocateGlobals() error {
	for _, mem := range p.ssaPkg.Members {
		g, ok := mem.(*ssa.Global)
		if !ok {
			continue
		}
		// Globals carry pointer types; their target is g.Type().Underlying().(*types.Pointer).Elem().
		ptr, ok := g.Type().Underlying().(*types.Pointer)
		if !ok {
			return fmt.Errorf("interp: global %s does not have pointer type", g.Name())
		}
		zero, err := p.converter.Zero(ptr.Elem(), p.resolver)
		if err != nil {
			return fmt.Errorf("interp: zero global %s: %w", g.Name(), err)
		}
		p.globals[g] = zero
	}
	return nil
}

// runInit invokes the package's init() function once at construction.
// A missing function is fine: SSA only emits init when there is
// something to do.
func (p *program) runInit(ctx context.Context) error {
	fn := p.ssaPkg.Func("init")
	if fn == nil {
		return nil
	}
	_, err := p.callSSA(ctx, nil, fn, nil, nil, 0)
	return err
}
