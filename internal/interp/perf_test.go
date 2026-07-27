package interp

import (
	"context"
	"testing"

	"github.com/t04dJ14n9/gig/internal/frontend"
	"github.com/t04dJ14n9/gig/value"
)

func measureProgramAllocs(t *testing.T, src, function string, runs int, check func(*testing.T, []value.Value)) float64 {
	t.Helper()
	ctx := context.Background()
	unit, err := frontend.NewBuilder().Build(ctx, frontend.Source{Content: src}, stubEnv{}, frontend.Config{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	prog, err := NewEngine().NewProgram(ctx, unit, stubEnv{}, Config{})
	if err != nil {
		t.Fatalf("NewProgram: %v", err)
	}
	warm, err := prog.Call(ctx, function, nil)
	if err != nil {
		t.Fatalf("warm Call: %v", err)
	}
	check(t, warm)

	return testing.AllocsPerRun(runs, func() {
		got, err := prog.Call(ctx, function, nil)
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
		check(t, got)
	})
}

func TestInterpArithmeticLoopAllocationsAreBounded(t *testing.T) {
	const src = `
func ArithmeticSum() int {
	sum := 0
	for i := 1; i <= 1000; i++ {
		sum += i
	}
	return sum
}
`
	allocs := measureProgramAllocs(t, src, "ArithmeticSum", 20, func(t *testing.T, got []value.Value) {
		t.Helper()
		expectInt(t, got, 500500)
	})
	if allocs > 100 {
		t.Fatalf("ArithmeticSum allocs/run = %.0f, want <= 100", allocs)
	}
}

func TestInterpIntSliceLoopAllocationsAreBounded(t *testing.T) {
	const src = `
func BubbleSort() int {
	s := make([]int, 100)
	for i := 0; i < 100; i++ {
		s[i] = 100 - i
	}
	n := len(s)
	for i := 0; i < n-1; i++ {
		for j := 0; j < n-1-i; j++ {
			if s[j] > s[j+1] {
				tmp := s[j]
				s[j] = s[j+1]
				s[j+1] = tmp
			}
		}
	}
	return s[0] + s[99]
}
`
	allocs := measureProgramAllocs(t, src, "BubbleSort", 10, func(t *testing.T, got []value.Value) {
		t.Helper()
		expectInt(t, got, 101)
	})
	if allocs > 500 {
		t.Fatalf("BubbleSort allocs/run = %.0f, want <= 500", allocs)
	}
}

func TestInterpClosureCallAllocationsAreBounded(t *testing.T) {
	const src = `
func ClosureCalls() int {
	sum := 0
	adder := func(x int) int {
		sum = sum + x
		return sum
	}
	for i := 0; i < 1000; i++ {
		adder(i)
	}
	return sum
}
`
	allocs := measureProgramAllocs(t, src, "ClosureCalls", 10, func(t *testing.T, got []value.Value) {
		t.Helper()
		expectInt(t, got, 499500)
	})
	// Go 1.23's closure/reflect allocation accounting is a little higher,
	// especially under CI's race+coverage mode. Keep the guard loose enough for
	// the supported CI matrix while still catching regressions back to the
	// pre-direct-call path.
	if allocs > 6000 {
		t.Fatalf("ClosureCalls allocs/run = %.0f, want <= 6000", allocs)
	}
}

func TestInterpDirectCallScratchAllocationsAreBounded(t *testing.T) {
	const src = `
func Echo(x int) int { return x }

func DirectCalls() int {
	sum := 0
	for i := 0; i < 100; i++ {
		sum += Echo(i)
	}
	return sum
}
`
	allocs := measureProgramAllocs(t, src, "DirectCalls", 20, func(t *testing.T, got []value.Value) {
		t.Helper()
		expectInt(t, got, 4950)
	})
	t.Logf("DirectCalls allocs/run = %.0f", allocs)
	// Keep this proportional guard loose enough for toolchain accounting
	// differences while rejecting one heap allocation for each local argument
	// and result scratch slot at every direct interpreted call.
	if allocs > 120 {
		t.Fatalf("DirectCalls allocs/run = %.0f, want <= 120", allocs)
	}
}

func TestInterpBuiltinLoopAllocationsAreBounded(t *testing.T) {
	const src = `
func BuiltinLoop() int {
	values := make([]int, 3)
	total := 0
	for i := 0; i < 1000; i++ {
		total += len(values)
	}
	return total
}
`
	allocs := measureProgramAllocs(t, src, "BuiltinLoop", 20, func(t *testing.T, got []value.Value) {
		t.Helper()
		expectInt(t, got, 3000)
	})
	t.Logf("BuiltinLoop allocs/run = %.0f", allocs)
	if allocs > 200 {
		t.Fatalf("BuiltinLoop allocs/run = %.0f, want <= 200", allocs)
	}
}
