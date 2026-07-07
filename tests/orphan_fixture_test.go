package tests

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/t04dJ14n9/gig"
)

func TestConcurrentStatefulFixtures(t *testing.T) {
	runOrderedTestSet(t, testSet{src: concurrentStatefulSrc, tests: concurrentStatefulTests}, concurrentStatefulOrder)
}

func TestCornercasesSrcFixtures(t *testing.T) {
	runTestSet(t, testSet{src: cornercasesSrcSrc, tests: cornercasesSrcTests})
}

func TestKnownIssuesFixtures(t *testing.T) {
	runTestSet(t, testSet{src: historicalIssueSrc, tests: knownIssuesTests, buildOpts: []gig.BuildOption{gig.WithAllowPanic()}})
}

func runOrderedTestSet(t *testing.T, set testSet, order []string) {
	t.Helper()
	prog, err := loadProgram(set.src, set.buildOpts...)
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	seen := make(map[string]bool, len(order))
	for _, fullKey := range order {
		tc, ok := set.tests[fullKey]
		if !ok {
			t.Fatalf("ordered testcase %q is missing from test map", fullKey)
		}
		seen[fullKey] = true
		runCorrectnessCase(t, prog, fullKey, tc)
	}
	for fullKey := range set.tests {
		if !seen[fullKey] {
			t.Fatalf("testcase %q is missing from ordered execution list", fullKey)
		}
	}
}

func runCorrectnessCase(t *testing.T, prog *gig.Program, fullKey string, tc testCase) {
	t.Helper()
	t.Run(fullKey, func(t *testing.T) {
		if reason := knownUnsupportedCases[tc.funcName]; reason != "" {
			t.Skip(reason)
		}
		if reason := knownUnrunnableCases[tc.funcName]; reason != "" {
			t.Skip(reason)
		}
		startInterp := time.Now()
		result, err := prog.Run(tc.funcName, cloneTestArgs(tc.args)...)
		interpDuration := time.Since(startInterp)
		if wantErr := expectedRunErrors[tc.funcName]; wantErr != "" {
			if err == nil {
				t.Fatalf("Run succeeded, want error containing %q", wantErr)
			}
			if !strings.Contains(err.Error(), wantErr) {
				t.Fatalf("Run error = %v, want contains %q", err, wantErr)
			}
			return
		}
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
		if check := customResultChecks[tc.funcName]; check != nil {
			check(t, result)
			return
		}
		if tc.native != nil {
			startNative := time.Now()
			expected := callNative(tc.native, cloneTestArgs(tc.args))
			nativeDuration := time.Since(startNative)
			compareCorrectnessResults(t, result, expected)
			ratio := float64(interpDuration) / float64(nativeDuration)
			t.Logf("interp: %v, native(using reflection): %v, ratio: %.1fx", interpDuration, nativeDuration, ratio)
		} else {
			t.Logf("interp: %v (no native comparison for package main source)", interpDuration)
		}
	})
}

func sourceOnlyTests(src string, args map[string][]any) map[string]testCase {
	names, err := exportedFunctionNames(src)
	if err != nil {
		panic(err)
	}
	tests := make(map[string]testCase, len(names))
	for _, name := range names {
		tests[name] = testCase{src: src, funcName: name, args: args[name]}
	}
	return tests
}

func exportedFunctionNames(src string) ([]string, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture.go", src, 0)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || !fn.Name.IsExported() {
			continue
		}
		names = append(names, fn.Name.Name)
	}
	sort.Strings(names)
	return names, nil
}

var concurrentStatefulTests = sourceOnlyTests(concurrentStatefulSrc, map[string][]any{
	"Add":              {2, 3},
	"AppendProtected":  {3},
	"AtomicAdd":        {int64(5)},
	"AtomicSet":        {int64(12)},
	"CASSwap":          {1, 7},
	"ComplexProducer":  {77},
	"MapDelete":        {"alpha"},
	"MapGetProtected":  {"k"},
	"MapLoad":          {"alpha"},
	"MapLoadOrStore":   {"beta", 9},
	"MapPutProtected":  {"k", 11},
	"MapStore":         {"alpha", 7},
	"Multiply":         {4, 5},
	"SetFlag":          {true},
	"SetGlobalString":  {"updated"},
	"SetState":         {42},
	"ValueTypeRWWrite": {33},
})

var concurrentStatefulOrder = []string{
	"IncrementUnprotected",
	"GetUnprotected",
	"IncrementProtected",
	"GetProtected",
	"SumViaChannel",
	"GetGreeting",
	"SetState",
	"GetState",
	"Add",
	"Multiply",
	"ProducerConsumerSum",
	"IncrementA",
	"IncrementB",
	"GetCountA",
	"GetCountB",
	"ValueTypeIncrement",
	"ValueTypeGet",
	"RWMutexWrite",
	"RWMutexRead",
	"OnceInit",
	"WaitGroupSum",
	"MapStore",
	"MapLoad",
	"MapLoadOrStore",
	"MapDelete",
	"NestedLockAB",
	"NestedLockBA",
	"ResetComplexState",
	"ComplexProducer",
	"ComplexConsumer",
	"AtomicSet",
	"AtomicAdd",
	"AtomicGet",
	"NestedGoroutineSum",
	"ResetBuf",
	"AppendProtected",
	"GetBufLen",
	"ResetProtectedMap",
	"MapPutProtected",
	"MapGetProtected",
	"MapLenProtected",
	"BarrierSum",
	"OnceInitA",
	"OnceInitB",
	"DeferInGoroutine",
	"GoroutineWithResult",
	"BidirectionalChannel",
	"MultiChannelMerge",
	"ValueTypeRWWrite",
	"ValueTypeRWRead",
	"SetGlobalString",
	"GetGlobalString",
	"CASIncrement",
	"CASSwap",
	"CASGet",
	"SetFlag",
	"GetFlag",
}

var cornercasesSrcTests = sourceOnlyTests(cornercasesSrcSrc, nil)

var knownIssuesTests = sourceOnlyTests(historicalIssueSrc, nil)
