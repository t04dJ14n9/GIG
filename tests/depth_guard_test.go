package tests

import (
	"strings"
	"testing"

	"github.com/t04dJ14n9/gig"
)

// The interpreter bounds recursion with a call-depth cap so runaway
// interpreted code returns an error instead of exhausting the host
// goroutine stack (an unrecoverable fatal error, not a panic). The cap
// must hold on every path that re-enters interpreted code, not just
// direct static calls.

func runExpectingDepthError(t *testing.T, src, entry string) {
	t.Helper()
	prog, err := gig.Build(src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	_, err = prog.Run(entry)
	if err == nil {
		t.Fatal("Run succeeded, want max call depth error")
	}
	if !strings.Contains(err.Error(), "max call depth") {
		t.Fatalf("Run error = %v, want max call depth error", err)
	}
}

func TestDepthGuardDirectRecursion(t *testing.T) {
	runExpectingDepthError(t, `
func Rec(n int) int { return Rec(n + 1) }
func Main() int { return Rec(0) }
`, "Main")
}

// Recursion through an interface method used to bypass the guard: the
// invoke path re-entered callSSA with depth 0, so the counter reset on
// every hop and the host process died of stack overflow.
func TestDepthGuardInterfaceMethodRecursion(t *testing.T) {
	runExpectingDepthError(t, `
type Looper interface{ Loop(n int) int }
type impl struct{}
func (impl) Loop(n int) int {
	var l Looper = impl{}
	return l.Loop(n + 1)
}
func Main() int {
	var l Looper = impl{}
	return l.Loop(0)
}
`, "Main")
}

// Deferred calls also re-enter interpreted code; a defer chain that
// keeps deferring must trip the same guard.
func TestDepthGuardDeferredCallRecursion(t *testing.T) {
	runExpectingDepthError(t, `
func Rec(n int) int {
	defer Rec(n + 1)
	return n
}
func Main() int { return Rec(0) }
`, "Main")
}
