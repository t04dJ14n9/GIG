package interp

import (
	"errors"
	"go/types"
	"testing"

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
