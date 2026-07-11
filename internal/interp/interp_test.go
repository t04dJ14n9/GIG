package interp

import (
	"context"
	"go/types"
	"reflect"
	"strings"
	"testing"

	"github.com/t04dJ14n9/gig/host"
	"github.com/t04dJ14n9/gig/internal/frontend"
	"github.com/t04dJ14n9/gig/value"
)

// stubEnv is a minimal host.Environment for end-to-end tests. Same
// shape as the one in internal/frontend/builder_test.go. Sufficient for
// programs that only touch the universe block (int, bool, len, etc.).
type stubEnv struct{}

func (stubEnv) Import(path string) (*types.Package, error) {
	return nil, &importError{path: path}
}
func (stubEnv) AutoImport(string) (host.Import, bool)                             { return host.Import{}, false }
func (stubEnv) LookupFunc(string, string) (host.Function, bool)                   { return nil, false }
func (stubEnv) LookupVar(string, string) (host.Variable, bool)                    { return nil, false }
func (stubEnv) LookupConst(string, string) (host.Constant, bool)                  { return nil, false }
func (stubEnv) LookupType(string, string) (host.Type, bool)                       { return nil, false }
func (stubEnv) LookupReflectType(types.Type) (reflect.Type, bool)                 { return nil, false }
func (stubEnv) LookupMethod(string, string) (host.Method, bool)                   { return nil, false }
func (stubEnv) LookupInterfaceProxy(*types.Interface) (host.InterfaceProxy, bool) { return nil, false }

type importError struct{ path string }

func (e *importError) Error() string { return "stubEnv: cannot import " + e.path }

// runProgram is the test helper: source -> Unit -> Program -> Call. It
// returns the result slice exactly as Program.Call produced it.
func runProgram(t *testing.T, src, fn string, args ...any) []value.Value {
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
	c := value.DefaultConverter()
	vals := make([]value.Value, len(args))
	for i, a := range args {
		v, err := c.FromAny(a)
		if err != nil {
			t.Fatalf("FromAny(%v): %v", a, err)
		}
		vals[i] = v
	}
	results, err := prog.Call(ctx, fn, vals)
	if err != nil {
		t.Fatalf("Call %s: %v", fn, err)
	}
	return results
}

// expectInt asserts the program returned a single int with value want.
func expectInt(t *testing.T, results []value.Value, want int64) {
	t.Helper()
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Kind() != value.KindInt {
		t.Fatalf("expected KindInt, got %s", results[0].Kind())
	}
	if results[0].Int() != want {
		t.Fatalf("expected %d, got %d", want, results[0].Int())
	}
}

// --- Arithmetic and comparisons --------------------------------------------

func TestInterp_AddInts(t *testing.T) {
	const src = `func Add(a, b int) int { return a + b }`
	expectInt(t, runProgram(t, src, "Add", 3, 4), 7)
	expectInt(t, runProgram(t, src, "Add", -1, 1), 0)
	expectInt(t, runProgram(t, src, "Add", 100, 200), 300)
}

func TestInterp_AllArithOps(t *testing.T) {
	const src = `
func Add(a, b int) int { return a + b }
func Sub(a, b int) int { return a - b }
func Mul(a, b int) int { return a * b }
func Quo(a, b int) int { return a / b }
func Rem(a, b int) int { return a % b }
func And(a, b int) int { return a & b }
func Or (a, b int) int { return a | b }
func Xor(a, b int) int { return a ^ b }
`
	cases := []struct {
		fn   string
		a, b int
		want int64
	}{
		{"Add", 5, 3, 8},
		{"Sub", 5, 3, 2},
		{"Mul", 5, 3, 15},
		{"Quo", 7, 2, 3},
		{"Rem", 7, 2, 1},
		{"And", 0xF0, 0x0F, 0x00},
		{"Or", 0xF0, 0x0F, 0xFF},
		{"Xor", 0xFF, 0x0F, 0xF0},
	}
	for _, tc := range cases {
		t.Run(tc.fn, func(t *testing.T) {
			expectInt(t, runProgram(t, src, tc.fn, tc.a, tc.b), tc.want)
		})
	}
}

func TestInterp_Comparisons(t *testing.T) {
	const src = `
func LessThan(a, b int) bool { return a < b }
func GreaterEq(a, b int) bool { return a >= b }
func EqualTo  (a, b int) bool { return a == b }
`
	expect := func(t *testing.T, results []value.Value, want bool) {
		t.Helper()
		if results[0].Kind() != value.KindBool {
			t.Fatalf("not bool")
		}
		if results[0].Bool() != want {
			t.Fatalf("got %v want %v", results[0].Bool(), want)
		}
	}
	expect(t, runProgram(t, src, "LessThan", 1, 2), true)
	expect(t, runProgram(t, src, "LessThan", 2, 1), false)
	expect(t, runProgram(t, src, "GreaterEq", 5, 5), true)
	expect(t, runProgram(t, src, "EqualTo", 7, 7), true)
	expect(t, runProgram(t, src, "EqualTo", 7, 8), false)
}

func TestInterp_UnaryNeg(t *testing.T) {
	const src = `func Neg(a int) int { return -a }`
	expectInt(t, runProgram(t, src, "Neg", 5), -5)
	expectInt(t, runProgram(t, src, "Neg", -10), 10)
}

func TestInterp_BoolOps(t *testing.T) {
	const src = `
func And(a, b bool) bool { return a && b }
func Or (a, b bool) bool { return a || b }
func Not(a bool) bool { return !a }
`
	check := func(name string, args []any, want bool) {
		t.Helper()
		r := runProgram(t, src, name, args...)
		if r[0].Bool() != want {
			t.Fatalf("%s%v = %v, want %v", name, args, r[0].Bool(), want)
		}
	}
	check("And", []any{true, true}, true)
	check("And", []any{true, false}, false)
	check("Or", []any{false, true}, true)
	check("Or", []any{false, false}, false)
	check("Not", []any{true}, false)
	check("Not", []any{false}, true)
}

// --- Control flow -----------------------------------------------------------

func TestInterp_IfElse(t *testing.T) {
	const src = `
func Max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
`
	expectInt(t, runProgram(t, src, "Max", 3, 7), 7)
	expectInt(t, runProgram(t, src, "Max", 9, 2), 9)
	expectInt(t, runProgram(t, src, "Max", 5, 5), 5)
}

func TestInterp_ForLoop(t *testing.T) {
	const src = `
func Sum(n int) int {
	s := 0
	for i := 1; i <= n; i++ {
		s = s + i
	}
	return s
}
`
	expectInt(t, runProgram(t, src, "Sum", 10), 55) // 1+2+...+10
	expectInt(t, runProgram(t, src, "Sum", 0), 0)
	expectInt(t, runProgram(t, src, "Sum", 1), 1)
}

func TestInterp_PhisUsePredecessorSnapshot(t *testing.T) {
	const src = `
func SwapLoop(n int) int {
	a, b := 1, 2
	for i := 0; i < n; i++ { a, b = b, a }
	return a*10 + b
}`
	expectInt(t, runProgram(t, src, "SwapLoop", 1), 21)
	expectInt(t, runProgram(t, src, "SwapLoop", 2), 12)
}

func TestInterp_ManyPhisUseOneSnapshot(t *testing.T) {
	const src = `
func Rotate9(n int) int {
	a, b, c, d, e, f, g, h, j := 1, 2, 3, 4, 5, 6, 7, 8, 9
	for k := 0; k < n; k++ {
		a, b, c, d, e, f, g, h, j = b, c, d, e, f, g, h, j, a
	}
	return a*100000000 + b*10000000 + c*1000000 + d*100000 +
		e*10000 + f*1000 + g*100 + h*10 + j
}`
	expectInt(t, runProgram(t, src, "Rotate9", 1), 234567891)
}

func TestInterp_AllocInLoopProducesFreshAddress(t *testing.T) {
	const src = `
func FreshAlloc() int {
	ps := make([]*int, 3)
	for i := 0; i < 3; i++ { x := i + 1; ps[i] = &x }
	*ps[0] = 9
	return *ps[1]*10 + *ps[2]
}`
	expectInt(t, runProgram(t, src, "FreshAlloc"), 23)
}

func TestInterp_UnifiedPlanPreservesMixedOperationOrder(t *testing.T) {
	const src = `
func Mixed(n int) int {
	s := make([]int, 2)
	x := n + 1
	s[0] = x
	wide := int64(s[0])
	s[1] = int(wide) + 2
	return s[0]*10 + s[1]
}`
	expectInt(t, runProgram(t, src, "Mixed", 3), 46)
}

func TestInterp_IndexAddrEscapesToCallee(t *testing.T) {
	const src = `
func bump(p *int) { *p = *p + 1 }
func EscapingIndexAddr() int {
	s := make([]int, 1)
	s[0] = 41
	bump(&s[0])
	return s[0]
}`
	expectInt(t, runProgram(t, src, "EscapingIndexAddr"), 42)
}

func TestInterp_NestedLoops(t *testing.T) {
	const src = `
func MultiplyAll(n int) int {
	total := 0
	for i := 1; i <= n; i++ {
		for j := 1; j <= n; j++ {
			total = total + i*j
		}
	}
	return total
}
`
	// sum_{i,j=1..n} i*j == (sum_{i=1..n} i)^2
	// for n=4: (1+2+3+4)^2 = 100
	expectInt(t, runProgram(t, src, "MultiplyAll", 4), 100)
}

// --- Recursion -------------------------------------------------------------

func TestInterp_FibRecursive(t *testing.T) {
	const src = `
func Fib(n int) int {
	if n < 2 {
		return n
	}
	return Fib(n-1) + Fib(n-2)
}
`
	expectInt(t, runProgram(t, src, "Fib", 0), 0)
	expectInt(t, runProgram(t, src, "Fib", 1), 1)
	expectInt(t, runProgram(t, src, "Fib", 10), 55)
	expectInt(t, runProgram(t, src, "Fib", 15), 610)
}

func TestInterp_Factorial(t *testing.T) {
	const src = `
func Fact(n int) int {
	if n <= 1 {
		return 1
	}
	return n * Fact(n-1)
}
`
	expectInt(t, runProgram(t, src, "Fact", 0), 1)
	expectInt(t, runProgram(t, src, "Fact", 5), 120)
	expectInt(t, runProgram(t, src, "Fact", 10), 3628800)
}

// --- Convert / type widths -------------------------------------------------

func TestInterp_TypeConvert(t *testing.T) {
	const src = `
func ToInt8(x int) int8 { return int8(x) }
`
	results := runProgram(t, src, "ToInt8", 200)
	v := results[0]
	if v.Kind() != value.KindInt {
		t.Fatalf("expected KindInt, got %s", v.Kind())
	}
	if v.SizeTag() != value.Size8 {
		t.Fatalf("expected Size8, got %d", v.SizeTag())
	}
	// 200 truncates to int8 -> -56
	if v.Int() != -56 {
		t.Fatalf("expected -56, got %d", v.Int())
	}
}

// --- Errors / call depth ---------------------------------------------------

func TestInterp_DivideByZero(t *testing.T) {
	const src = `func Div(a, b int) int { return a / b }`
	ctx := context.Background()
	unit, err := frontend.NewBuilder().Build(ctx, frontend.Source{Content: src}, stubEnv{}, frontend.Config{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	prog, err := NewEngine().NewProgram(ctx, unit, stubEnv{}, Config{})
	if err != nil {
		t.Fatalf("NewProgram: %v", err)
	}
	c := value.DefaultConverter()
	a, _ := c.FromAny(10)
	b, _ := c.FromAny(0)
	_, err = prog.Call(ctx, "Div", []value.Value{a, b})
	if err == nil {
		t.Fatal("expected divide-by-zero error")
	}
	if !strings.Contains(err.Error(), "divide by zero") {
		t.Fatalf("error should mention divide by zero, got: %v", err)
	}
}

func TestInterp_FunctionNotFound(t *testing.T) {
	const src = `func A() int { return 1 }`
	ctx := context.Background()
	unit, _ := frontend.NewBuilder().Build(ctx, frontend.Source{Content: src}, stubEnv{}, frontend.Config{})
	prog, _ := NewEngine().NewProgram(ctx, unit, stubEnv{}, Config{})
	_, err := prog.Call(ctx, "Missing", nil)
	if err == nil {
		t.Fatal("expected error for missing function")
	}
}

func TestInterp_ArgCountMismatch(t *testing.T) {
	const src = `func A(x, y int) int { return x + y }`
	ctx := context.Background()
	unit, _ := frontend.NewBuilder().Build(ctx, frontend.Source{Content: src}, stubEnv{}, frontend.Config{})
	prog, _ := NewEngine().NewProgram(ctx, unit, stubEnv{}, Config{})
	c := value.DefaultConverter()
	a, _ := c.FromAny(1)
	_, err := prog.Call(ctx, "A", []value.Value{a})
	if err == nil {
		t.Fatal("expected error for arg count mismatch")
	}
}

// --- Multi-function programs -----------------------------------------------

func TestInterp_FunctionCallChain(t *testing.T) {
	const src = `
func Inc(x int) int { return x + 1 }
func Double(x int) int { return x * 2 }
func IncThenDouble(x int) int { return Double(Inc(x)) }
`
	expectInt(t, runProgram(t, src, "IncThenDouble", 5), 12) // (5+1)*2
}

func TestInterp_Recursion_DeepEnough(t *testing.T) {
	const src = `
func CountDown(n int) int {
	if n == 0 {
		return 0
	}
	return CountDown(n-1)
}
`
	// 100 iterations is well below the 1024 default cap.
	expectInt(t, runProgram(t, src, "CountDown", 100), 0)
}

func TestInterp_RecursionLimit(t *testing.T) {
	const src = `
func Deep(n int) int {
	return Deep(n+1)
}
`
	ctx := context.Background()
	unit, _ := frontend.NewBuilder().Build(ctx, frontend.Source{Content: src}, stubEnv{}, frontend.Config{})
	prog, err := NewEngine().NewProgram(ctx, unit, stubEnv{}, Config{MaxDepth: 32})
	if err != nil {
		t.Fatalf("NewProgram: %v", err)
	}
	c := value.DefaultConverter()
	a, _ := c.FromAny(0)
	_, err = prog.Call(ctx, "Deep", []value.Value{a})
	if err == nil {
		t.Fatal("expected max-depth error")
	}
	if !strings.Contains(err.Error(), "max call depth") {
		t.Fatalf("expected max call depth error, got: %v", err)
	}
}
