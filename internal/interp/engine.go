// engine.go is the default interp.Engine implementation. It builds
// Programs from frontend Units and exposes the per-call entry point.
//
// Phase 6 vertical slice: this engine runs interpreted Go code limited
// to scalar arithmetic, control flow, function calls, local Alloc/Store,
// and Phi merges. Composite types, closures, defer/panic/recover,
// goroutines, and host calls are added in subsequent slices.
package interp

import (
	"context"
	"fmt"
	"reflect"
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
		fset:      unit.FileSet(),
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
	// package-level initialisation are honoured. The vertical slice
	// has no globals-with-bodies, so this is currently a no-op for the
	// programs we test, but the call site is kept honest.
	if err := p.runInit(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

const defaultMaxDepth = 1024

// program is the running Program. Mutable package globals live directly in
// globals, allocated once at construction time (matching gofun and Go
// semantics). Addressability for locals and composite data lives inside
// reflected pointer values rather than in a second storage wrapper.
type program struct {
	ssaPkg      *ssa.Package
	fset        any // token.FileSet, kept abstract here.
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
// Phase 6 vertical slice has nothing meaningful to put through init,
// but a missing function is also fine: SSA only emits init when there
// is something to do.
func (p *program) runInit(ctx context.Context) error {
	fn := p.ssaPkg.Func("init")
	if fn == nil {
		return nil
	}
	_, err := p.callSSA(ctx, nil, fn, nil, nil, 0)
	return err
}

// typeResolver implements value.TypeResolver by translating types.Type
// into reflect.Type. It supports the full Go type system that the
// interpreter operates on: basic kinds, named types, pointers, slices,
// arrays, maps, channels, structs, interfaces, function signatures,
// and tuples (returned as a synthetic struct).
//
// The cache is keyed by types.Type identity, not by t.String(): two
// distinct named types declared in different scopes (e.g. two
// independent `type Inner struct{...}` blocks inside different
// functions) can share a string representation but produce different
// reflect.Types.
//
// Recursive types (`type Node struct { Next *Node }`) cannot be built
// with reflect.StructOf because Go's runtime forbids self-referential
// reflect.Type construction. We detect recursion via a per-resolution
// "in-flight" set, and substitute interface{} for the back-edge so
// interpretation can continue. The cost: recursive fields lose static
// typing inside the interpreter; the interpreter compensates by going
// through reflect.Value at every access.
// typeResolver maps go/types.Type values to reflect.Type instances. It
// is shared by all components that need a runtime view of an SSA-typed
// value: the interpreter's value reads/writes, the host-call boundary
// arg packers, and the cell allocator. Resolution is memoised because
// SSA reuses *types.Type pointers across an entire program — a single
// types.Type → reflect.Type mapping can fire millions of times.
//
// `srcPkgPath` is the interpreted package's import path. It's used to
// distinguish source-declared named types (which may need synthesised
// identity tagging — see the *types.Named case in ResolveType) from
// host named types (which must round-trip through `host.Environment`).
type typeResolver struct {
	mu         sync.RWMutex
	cache      map[types.Type]reflect.Type
	env        host.Environment
	srcPkgPath string
}

func newTypeResolver(env host.Environment, srcPkgPath string) *typeResolver {
	return &typeResolver{
		cache:      map[types.Type]reflect.Type{},
		env:        env,
		srcPkgPath: srcPkgPath,
	}
}

func (r *typeResolver) ResolveType(t types.Type) (reflect.Type, error) {
	return r.resolveType(t, nil)
}

// resolveType resolves t to a reflect.Type. inFlight tracks the types
// currently being built on this resolution stack so recursive types
// (e.g. `type Node struct{ Next *Node }`) can be broken with an
// interface{} placeholder. It is threaded through the recursion as a
// parameter rather than stored on the resolver so that concurrent
// resolutions from interpreted goroutines never share it — the shared
// cache below is idempotent, but a shared inFlight set would let one
// goroutine observe another's half-built type and hand back a bogus
// interface{} placeholder (see the closure-signature race that made
// reflect.MakeFunc panic under concurrent `go` statements).
func (r *typeResolver) resolveType(t types.Type, inFlight map[types.Type]bool) (reflect.Type, error) {
	if t == nil {
		return nil, fmt.Errorf("interp: nil type")
	}
	r.mu.RLock()
	rt, ok := r.cache[t]
	r.mu.RUnlock()
	if ok {
		return rt, nil
	}
	if inFlight[t] {
		return reflect.TypeOf((*any)(nil)).Elem(), nil
	}
	if r.env != nil {
		if rt, ok := r.env.LookupReflectType(t); ok {
			r.mu.Lock()
			r.cache[t] = rt
			r.mu.Unlock()
			return rt, nil
		}
	}
	if inFlight == nil {
		inFlight = map[types.Type]bool{}
	}
	inFlight[t] = true
	rt, err := r.build(t, inFlight)
	delete(inFlight, t)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.cache[t] = rt
	r.mu.Unlock()
	return rt, nil
}

// build constructs the reflect.Type from a types.Type. Recursive
// types (mutually-referencing structs) are handled by inserting a
// placeholder before recursing, but for the common testdata cases a
// non-cyclic walk is enough.
func (r *typeResolver) build(t types.Type, inFlight map[types.Type]bool) (reflect.Type, error) {
	if b, ok := t.(*types.Basic); ok {
		if rt := basicReflectType(b.Kind()); rt != nil {
			return rt, nil
		}
	}
	switch t := t.(type) {
	case *types.Basic:
		if rt := basicReflectType(t.Kind()); rt != nil {
			return rt, nil
		}
		// types.Invalid (kind 0) and types.UnsafePointer surface here.
		// Most often we hit Invalid for synthetic SSA types like the
		// iterator type produced by *ssa.Range. Use the empty
		// interface as a safe placeholder; callers that try to
		// concretely use the resulting reflect.Type will fail loudly,
		// but ones that just need a slot (e.g. Range iterator value storage)
		// still work.
		return reflect.TypeOf((*any)(nil)).Elem(), nil
	case *types.Named:
		under, err := r.resolveType(t.Underlying(), inFlight)
		if err != nil {
			return nil, err
		}
		// Two interpreted named types with structurally identical
		// underlyings (e.g. `type A struct{v int}` and
		// `type B struct{v int}`) collapse to the same reflect.Type if
		// we just hand back the underlying. That breaks method dispatch:
		// `lookupInterpretedMethod` would not be able to tell `*A.Foo`
		// from `*B.Foo`. To preserve identity per Go's spec we tag
		// field 0 with a per-named-type marker; reflect's type identity
		// is tag-sensitive but tags are invisible to fmt's %v / %+v and
		// to the encoding/json marshaller, so user-visible behaviour
		// remains equivalent to the underlying type.
		//
		// Only apply this to types declared in the interpreted source
		// package — host types (e.g. binary.littleEndian) must round-
		// trip through reflect with their exact runtime identity, or
		// host code that compares struct types (encoding/binary,
		// reflect.DeepEqual) breaks.
		if r.isInterpretedNamedType(t) {
			if _, ok := t.Underlying().(*types.Struct); ok && under.Kind() == reflect.Struct && under.NumField() > 0 {
				fields := make([]reflect.StructField, under.NumField())
				marker := `gig:"` + sanitizeNamedTypeName(t) + `"`
				for i := 0; i < under.NumField(); i++ {
					fields[i] = under.Field(i)
					if i == 0 {
						existing := string(fields[i].Tag)
						if existing == "" {
							fields[i].Tag = reflect.StructTag(marker)
						} else {
							fields[i].Tag = reflect.StructTag(existing + " " + marker)
						}
					}
				}
				return reflect.StructOf(fields), nil
			}
		}
		return under, nil
	case *types.Alias:
		// Aliases (Go 1.22+) are transparent — just resolve the target.
		return r.resolveType(types.Unalias(t), inFlight)
	case *types.Pointer:
		// Recursion guard at the pointer level: if the pointer's elem
		// is currently being built (e.g. *Node where Node is the type
		// we're constructing), return interface{} for the whole
		// pointer. This means the recursive field is typed `any` in
		// the synthetic struct, which accepts any concrete pointer at
		// runtime and avoids the
		// "reflect.Set: *struct{...} not assignable to *interface{}"
		// problem.
		r.mu.RLock()
		_, cached := r.cache[t.Elem()]
		r.mu.RUnlock()
		if !cached && inFlight[t.Elem()] {
			return reflect.TypeOf((*any)(nil)).Elem(), nil
		}
		elem, err := r.resolveType(t.Elem(), inFlight)
		if err != nil {
			return nil, err
		}
		return reflect.PointerTo(elem), nil
	case *types.Slice:
		elem, err := r.resolveType(t.Elem(), inFlight)
		if err != nil {
			return nil, err
		}
		return reflect.SliceOf(elem), nil
	case *types.Array:
		elem, err := r.resolveType(t.Elem(), inFlight)
		if err != nil {
			return nil, err
		}
		return reflect.ArrayOf(int(t.Len()), elem), nil
	case *types.Map:
		key, err := r.resolveType(t.Key(), inFlight)
		if err != nil {
			return nil, err
		}
		val, err := r.resolveType(t.Elem(), inFlight)
		if err != nil {
			return nil, err
		}
		return reflect.MapOf(key, val), nil
	case *types.Chan:
		elem, err := r.resolveType(t.Elem(), inFlight)
		if err != nil {
			return nil, err
		}
		var dir reflect.ChanDir
		switch t.Dir() {
		case types.SendOnly:
			dir = reflect.SendDir
		case types.RecvOnly:
			dir = reflect.RecvDir
		default:
			dir = reflect.BothDir
		}
		return reflect.ChanOf(dir, elem), nil
	case *types.Struct:
		fields := make([]reflect.StructField, t.NumFields())
		for i := range fields {
			f := t.Field(i)
			ft, err := r.resolveType(f.Type(), inFlight)
			if err != nil {
				return nil, err
			}
			name := f.Name()
			if name == "" || !exportedName(name) {
				// reflect.StructOf requires exported field names. We
				// uppercase non-exported fields so the synthetic type
				// works; access still goes through SSA Field index, so
				// the rename is invisible to interpreted code.
				name = "F_" + name
			}
			fields[i] = reflect.StructField{
				Name: name,
				Type: ft,
				Tag:  reflect.StructTag(t.Tag(i)),
			}
		}
		return reflect.StructOf(fields), nil
	case *types.Interface:
		// Empty interface is the canonical "any" target.
		return reflect.TypeOf((*any)(nil)).Elem(), nil
	case *types.Signature:
		// Synthesise a func type. Receiver folds into params.
		ins := make([]reflect.Type, 0, t.Params().Len())
		if recv := t.Recv(); recv != nil {
			rt, err := r.resolveType(recv.Type(), inFlight)
			if err != nil {
				return nil, err
			}
			ins = append(ins, rt)
		}
		for i := 0; i < t.Params().Len(); i++ {
			pt, err := r.resolveType(t.Params().At(i).Type(), inFlight)
			if err != nil {
				return nil, err
			}
			ins = append(ins, pt)
		}
		outs := make([]reflect.Type, t.Results().Len())
		for i := range outs {
			ot, err := r.resolveType(t.Results().At(i).Type(), inFlight)
			if err != nil {
				return nil, err
			}
			outs[i] = ot
		}
		return reflect.FuncOf(ins, outs, t.Variadic()), nil
	case *types.Tuple:
		// Tuples appear as Call.Type() for multi-return functions. We
		// represent the tuple as a synthetic struct so reflect.Zero
		// works; ssa.Extract reads individual elements by index, so
		// the struct shape matches.
		fields := make([]reflect.StructField, t.Len())
		for i := range fields {
			ft, err := r.resolveType(t.At(i).Type(), inFlight)
			if err != nil {
				return nil, err
			}
			fields[i] = reflect.StructField{
				Name: fmt.Sprintf("F%d", i),
				Type: ft,
			}
		}
		return reflect.StructOf(fields), nil
	}
	return nil, fmt.Errorf("interp: unsupported type %T (%s)", t, t)
}

func exportedName(s string) bool {
	if s == "" {
		return false
	}
	c := s[0]
	return c >= 'A' && c <= 'Z'
}

func basicReflectType(k types.BasicKind) reflect.Type {
	switch k {
	case types.Bool, types.UntypedBool:
		return reflect.TypeOf(false)
	case types.Int, types.UntypedInt:
		return reflect.TypeOf(int(0))
	case types.Int8:
		return reflect.TypeOf(int8(0))
	case types.Int16:
		return reflect.TypeOf(int16(0))
	case types.Int32, types.UntypedRune:
		return reflect.TypeOf(int32(0))
	case types.Int64:
		return reflect.TypeOf(int64(0))
	case types.Uint:
		return reflect.TypeOf(uint(0))
	case types.Uint8:
		return reflect.TypeOf(uint8(0))
	case types.Uint16:
		return reflect.TypeOf(uint16(0))
	case types.Uint32:
		return reflect.TypeOf(uint32(0))
	case types.Uint64, types.Uintptr:
		return reflect.TypeOf(uint64(0))
	case types.Float32:
		return reflect.TypeOf(float32(0))
	case types.Float64, types.UntypedFloat:
		return reflect.TypeOf(float64(0))
	case types.Complex64:
		return reflect.TypeOf(complex64(0))
	case types.Complex128, types.UntypedComplex:
		return reflect.TypeOf(complex128(0))
	case types.String, types.UntypedString:
		return reflect.TypeOf("")
	}
	return nil
}

// sanitizeNamedTypeName produces an identifier-safe form of a
// *types.Named's full path so it can be used as a reflect struct field
// name. Field names must start with a Unicode letter; we map dots,
// slashes, and other non-identifier runes to underscores.
func sanitizeNamedTypeName(t *types.Named) string {
	obj := t.Obj()
	var pkg string
	if p := obj.Pkg(); p != nil {
		pkg = p.Path()
	}
	full := pkg + "." + obj.Name()
	out := make([]byte, 0, len(full))
	for i := 0; i < len(full); i++ {
		c := full[i]
		switch {
		case c >= 'A' && c <= 'Z',
			c >= 'a' && c <= 'z',
			c >= '0' && c <= '9',
			c == '_':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

// isInterpretedNamedType reports whether t is a *types.Named declared
// in the interpreted source package. Source-declared named types need
// per-name reflect identity (see *types.Named in ResolveType); host
// named types must keep their exact runtime identity.
func (r *typeResolver) isInterpretedNamedType(t *types.Named) bool {
	obj := t.Obj()
	if obj == nil {
		return false
	}
	pkg := obj.Pkg()
	if pkg == nil {
		// Built-in named types (error, comparable). Host-side.
		return false
	}
	// Local declarations inside an interpreted function come back with
	// pkg.Path() == r.srcPkgPath (or empty string for unnamed test
	// files). Treat both as source-declared.
	return pkg.Path() == r.srcPkgPath || pkg.Path() == ""
}
