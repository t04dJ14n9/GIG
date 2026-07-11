package interp

import (
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
}

func (f *recordingDirectFunction) CallDirect([]value.Value) ([]value.Value, bool, error) {
	f.directCalls++
	return []value.Value{f.directResult}, f.handled, nil
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
}
