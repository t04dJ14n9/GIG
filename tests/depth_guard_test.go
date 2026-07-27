package tests

import (
	"strings"
	"testing"

	"github.com/t04dJ14n9/gig"
	"github.com/t04dJ14n9/gig/importer"
)

// The interpreter bounds recursion with a call-depth cap so runaway
// interpreted code returns an error instead of exhausting the host
// goroutine stack (an unrecoverable fatal error, not a panic). The cap
// must hold on every path that re-enters interpreted code, not just
// direct static calls.

func runExpectingDepthError(t *testing.T, src string, opts ...gig.BuildOption) {
	t.Helper()
	prog, err := gig.Build(src, opts...)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	_, err = prog.Run("Main")
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
`)
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
`)
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
`)
}

// A synchronous host callback crosses reflect.MakeFunc before re-entering
// interpreted code. That wrapper must preserve the active call depth instead
// of treating every callback invocation as a new top-level execution.
func TestDepthGuardHostCallbackRecursion(t *testing.T) {
	registry := importer.NewRegistry()
	registry.RegisterPackage("example/callback", "callback").AddFunction(
		"Call",
		func(fn func() int) int { return fn() },
		"",
	)
	runExpectingDepthError(t, `
package main

import "example/callback"

func Rec(n int) int {
	if n == 1050 {
		return n
	}
	return callback.Call(func() int { return Rec(n + 1) })
}

func Main() int { return Rec(0) }
`, gig.WithRegistry(registry))
}
