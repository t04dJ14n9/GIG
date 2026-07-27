package interp

import (
	"context"
	"errors"
	"go/token"
	"go/types"
	"reflect"
	"testing"

	"golang.org/x/tools/go/ssa"

	"github.com/t04dJ14n9/gig/host"
	"github.com/t04dJ14n9/gig/value"
)

type recordingHostFunction struct {
	calls  int
	result value.Value
}

func (f *recordingHostFunction) Name() string                { return "recording" }
func (f *recordingHostFunction) Signature() *types.Signature { return nil }
func (f *recordingHostFunction) Call([]value.Value) ([]value.Value, error) {
	f.calls++
	return []value.Value{f.result}, nil
}

type recordingDirectFunction struct {
	*recordingHostFunction

	directCalls  int
	handled      bool
	directResult value.Value
	directErr    error
}

func (f *recordingDirectFunction) CallDirect([]value.Value) ([]value.Value, bool, error) {
	f.directCalls++
	if f.directErr != nil {
		return nil, f.handled, f.directErr
	}
	return []value.Value{f.directResult}, f.handled, nil
}

type recordingHostMethod struct {
	calls  int
	result value.Value
}

func (m *recordingHostMethod) Name() string                { return "recording" }
func (m *recordingHostMethod) Receiver() types.Type        { return nil }
func (m *recordingHostMethod) Signature() *types.Signature { return nil }
func (m *recordingHostMethod) Call(value.Value, []value.Value) ([]value.Value, error) {
	m.calls++
	return []value.Value{m.result}, nil
}

type recordingDirectMethod struct {
	*recordingHostMethod

	directCalls  int
	handled      bool
	directResult value.Value
	directErr    error
}

type hostFallbackReceiver struct{}

type methodFallbackEnv struct {
	stubEnv

	methodName string
	method     host.Method
}

func (e methodFallbackEnv) LookupMethod(_ string, methodName string) (host.Method, bool) {
	if e.method != nil && methodName == e.methodName {
		return e.method, true
	}
	return nil, false
}

func newBodylessHostFunction(t *testing.T, name string) (*ssa.Package, *ssa.Function) {
	t.Helper()
	pkg := types.NewPackage("example/fallback", "fallback")
	sig := types.NewSignatureType(nil, nil, nil, types.NewTuple(), types.NewTuple(), false)
	obj := types.NewFunc(token.NoPos, pkg, name, sig)
	if previous := pkg.Scope().Insert(obj); previous != nil {
		t.Fatalf("insert function %s: existing object %v", name, previous)
	}
	pkg.MarkComplete()
	ssaProg := ssa.NewProgram(token.NewFileSet(), ssa.SanityCheckFunctions)
	ssaPkg := ssaProg.CreatePackage(pkg, nil, nil, true)
	ssaPkg.Build()
	fn := ssaPkg.Func(name)
	if fn == nil {
		t.Fatalf("SSA package has no function %s", name)
	}
	return ssaPkg, fn
}

func (m *recordingDirectMethod) CallDirect(value.Value, []value.Value) (value.Value, bool, error) {
	m.directCalls++
	return m.directResult, m.handled, m.directErr
}

func TestCallResolvedHostFuncSelectsOnePath(t *testing.T) {
	t.Run("direct handled", func(t *testing.T) {
		fn := &recordingDirectFunction{
			recordingHostFunction: &recordingHostFunction{result: value.MakeInt(9)},
			handled:               true,
			directResult:          value.MakeInt(7),
		}
		got, err := callResolvedHostFunc(fn, nil)
		if err != nil {
			t.Fatalf("callResolvedHostFunc: %v", err)
		}
		if got[0].Int() != 7 || fn.directCalls != 1 || fn.calls != 0 {
			t.Fatalf("result=%v direct=%d generic=%d", got, fn.directCalls, fn.calls)
		}
	})
	t.Run("direct declined", func(t *testing.T) {
		fn := &recordingDirectFunction{
			recordingHostFunction: &recordingHostFunction{result: value.MakeInt(9)},
			handled:               false,
			directResult:          value.MakeInt(7),
		}
		got, err := callResolvedHostFunc(fn, nil)
		if err != nil {
			t.Fatalf("callResolvedHostFunc: %v", err)
		}
		if got[0].Int() != 9 || fn.directCalls != 1 || fn.calls != 1 {
			t.Fatalf("result=%v direct=%d generic=%d", got, fn.directCalls, fn.calls)
		}
	})
	t.Run("direct error", func(t *testing.T) {
		wantErr := errors.New("direct function failed")
		fn := &recordingDirectFunction{
			recordingHostFunction: &recordingHostFunction{result: value.MakeInt(9)},
			directErr:             wantErr,
		}
		_, err := callResolvedHostFunc(fn, nil)
		if !errors.Is(err, wantErr) {
			t.Fatalf("error=%v, want %v", err, wantErr)
		}
		if fn.directCalls != 1 || fn.calls != 0 {
			t.Fatalf("direct=%d generic=%d", fn.directCalls, fn.calls)
		}
	})
}

func TestCallResolvedHostMethodSelectsOnePath(t *testing.T) {
	t.Run("direct handled", func(t *testing.T) {
		method := &recordingDirectMethod{
			recordingHostMethod: &recordingHostMethod{result: value.MakeInt(9)},
			handled:             true,
			directResult:        value.MakeInt(7),
		}
		got, err := callResolvedHostMethod(method, value.MakeInt(1), nil)
		if err != nil {
			t.Fatalf("callResolvedHostMethod: %v", err)
		}
		if got[0].Int() != 7 || method.directCalls != 1 || method.calls != 0 {
			t.Fatalf("result=%v direct=%d generic=%d", got, method.directCalls, method.calls)
		}
	})
	t.Run("direct declined", func(t *testing.T) {
		method := &recordingDirectMethod{
			recordingHostMethod: &recordingHostMethod{result: value.MakeInt(9)},
			directResult:        value.MakeInt(7),
		}
		got, err := callResolvedHostMethod(method, value.MakeInt(1), nil)
		if err != nil {
			t.Fatalf("callResolvedHostMethod: %v", err)
		}
		if got[0].Int() != 9 || method.directCalls != 1 || method.calls != 1 {
			t.Fatalf("result=%v direct=%d generic=%d", got, method.directCalls, method.calls)
		}
	})
	t.Run("direct error", func(t *testing.T) {
		wantErr := errors.New("direct method failed")
		method := &recordingDirectMethod{
			recordingHostMethod: &recordingHostMethod{result: value.MakeInt(9)},
			directErr:           wantErr,
		}
		got, err := callResolvedHostMethod(method, value.MakeInt(1), nil)
		if !errors.Is(err, wantErr) {
			t.Fatalf("error=%v, want %v", err, wantErr)
		}
		if got != nil || method.directCalls != 1 || method.calls != 0 {
			t.Fatalf("result=%v direct=%d generic=%d", got, method.directCalls, method.calls)
		}
	})
}

func TestCallHostFuncMethodCompatibilityFallbackPreservesInvocationErrors(t *testing.T) {
	ssaPkg, fn := newBodylessHostFunction(t, "Explode")
	receiver, err := value.DefaultConverter().FromReflect(reflect.ValueOf(hostFallbackReceiver{}))
	if err != nil {
		t.Fatalf("convert receiver: %v", err)
	}

	t.Run("resolved method error", func(t *testing.T) {
		wantErr := errors.New("direct method failed through compatibility fallback")
		method := &recordingDirectMethod{
			recordingHostMethod: &recordingHostMethod{result: value.MakeInt(9)},
			directErr:           wantErr,
		}
		env := methodFallbackEnv{methodName: fn.Name(), method: method}
		prog := &program{
			ssaPkg:    ssaPkg,
			env:       env,
			converter: value.DefaultConverter(),
			resolver:  newTypeResolver(env, ssaPkg.Pkg.Path()),
		}

		_, err := prog.callHostFunc(context.Background(), nil, fn, []value.Value{receiver}, 0)
		if !errors.Is(err, wantErr) {
			t.Fatalf("callHostFunc error = %v, want original error %v", err, wantErr)
		}
		if method.directCalls != 1 || method.calls != 0 {
			t.Fatalf("direct=%d generic=%d, want 1/0", method.directCalls, method.calls)
		}
	})

	t.Run("resolved method lookup-miss-shaped error", func(t *testing.T) {
		wantErr := &methodNotFoundError{
			method:   "Nested",
			receiver: reflect.TypeOf(hostFallbackReceiver{}),
		}
		method := &recordingDirectMethod{
			recordingHostMethod: &recordingHostMethod{result: value.MakeInt(9)},
			directErr:           wantErr,
		}
		env := methodFallbackEnv{methodName: fn.Name(), method: method}
		prog := &program{
			ssaPkg:    ssaPkg,
			env:       env,
			converter: value.DefaultConverter(),
			resolver:  newTypeResolver(env, ssaPkg.Pkg.Path()),
		}

		_, err := prog.callHostFunc(context.Background(), nil, fn, []value.Value{receiver}, 0)
		if !errors.Is(err, wantErr) {
			t.Fatalf("callHostFunc error = %v, want resolved method error %v", err, wantErr)
		}
		if method.directCalls != 1 || method.calls != 0 {
			t.Fatalf("direct=%d generic=%d, want 1/0", method.directCalls, method.calls)
		}
	})

	t.Run("genuine method miss", func(t *testing.T) {
		env := methodFallbackEnv{}
		prog := &program{
			ssaPkg:    ssaPkg,
			env:       env,
			converter: value.DefaultConverter(),
			resolver:  newTypeResolver(env, ssaPkg.Pkg.Path()),
		}

		_, err := prog.callHostFunc(context.Background(), nil, fn, []value.Value{receiver}, 0)
		const want = "interp: host function example/fallback.Explode not found"
		if err == nil || err.Error() != want {
			t.Fatalf("callHostFunc error = %v, want %q", err, want)
		}
	})
}
