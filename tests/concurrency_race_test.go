package tests

import (
	"testing"

	"github.com/t04dJ14n9/gig"
)

// These tests are regressions for two interpreter concurrency bugs that
// only surfaced under load (and under `go test -race`), where a single
// slow/unlucky schedule made CI hang until the 10-minute timeout:
//
//  1. typeResolver kept its recursion guard (inFlight) on the shared
//     resolver. Two interpreted goroutines resolving the same uncached
//     type concurrently could see each other's in-progress marker and
//     get back an interface{} placeholder, making reflect.MakeFunc panic
//     on a non-func type. The guard is now stack-local per resolution.
//
//  2. The frame that was panicking was tracked in a single field on the
//     shared *program. Concurrent interpreted panic/recover clobbered it,
//     so recover() cleared the wrong frame and goroutines died without
//     doing their work. The panicking frame is now reached via the
//     threaded caller, which is per-goroutine.
//
// Run with -race and a high iteration count to give the scheduler enough
// chances to interleave.

// nestedGoroutineSrc spawns nested goroutines that all send on one
// buffered channel; the receiver blocks forever if any inner goroutine
// is dropped (bug 1).
const nestedGoroutineSrc = `package main

import "sync"

func NestedGoroutineSum() int {
	ch := make(chan int, 50)
	var wg sync.WaitGroup
	wg.Add(5)
	for i := 0; i < 5; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				go func() {
					ch <- 1
				}()
			}
		}()
	}
	wg.Wait()
	sum := 0
	for i := 0; i < 50; i++ {
		sum += <-ch
	}
	return sum
}
`

// concurrentPanicSrc runs many goroutines that each panic and recover
// independently; a corrupted shared panic frame makes a recover() miss,
// so a goroutine dies without sending and the receiver blocks (bug 2).
const concurrentPanicSrc = `package main

import "sync"

func ConcurrentRecover() int {
	ch := make(chan int, 100)
	var wg sync.WaitGroup
	wg.Add(100)
	for i := 0; i < 100; i++ {
		go func(n int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					ch <- 1
				}
			}()
			if n%2 == 0 {
				panic("boom")
			}
			ch <- 1
		}(i)
	}
	wg.Wait()
	sum := 0
	for i := 0; i < 100; i++ {
		sum += <-ch
	}
	return sum
}
`

func TestConcurrentNestedGoroutines(t *testing.T) {
	prog, err := gig.Build(nestedGoroutineSrc)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	const iters = 500
	for i := 0; i < iters; i++ {
		res, err := prog.Run("NestedGoroutineSum")
		if err != nil {
			t.Fatalf("iter %d: run error: %v", i, err)
		}
		if n, _ := res.(int); n != 50 {
			t.Fatalf("iter %d: got %d, want 50", i, n)
		}
	}
}

func TestConcurrentPanicRecover(t *testing.T) {
	prog, err := gig.Build(concurrentPanicSrc, gig.WithAllowPanic())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	const iters = 300
	for i := 0; i < iters; i++ {
		res, err := prog.Run("ConcurrentRecover")
		if err != nil {
			t.Fatalf("iter %d: run error: %v", i, err)
		}
		if n, _ := res.(int); n != 100 {
			t.Fatalf("iter %d: got %d, want 100", i, n)
		}
	}
}
