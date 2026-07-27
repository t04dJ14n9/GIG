package tests

import (
	"sync"
	"testing"

	"github.com/t04dJ14n9/gig"
	_ "github.com/t04dJ14n9/gig/stdlib/packages"
)

// TestValueTypeMutexSingleCall verifies value-type mutex works for a single
// interpreted call.
func TestValueTypeMutexSingleCall(t *testing.T) {
	src := `
package main

import "sync"

var mu sync.Mutex
var counter int

func IncrementAndGet() int {
	mu.Lock()
	counter = counter + 1
	val := counter
	mu.Unlock()
	return val
}
`
	prog, err := gig.Build(src)
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	result, err := prog.Run("IncrementAndGet")
	if err != nil {
		t.Fatalf("IncrementAndGet error: %v", err)
	}
	got := toInt64(result)
	if got != 1 {
		t.Fatalf("got %d, want 1", got)
	}
	t.Logf("Value-type Mutex: Lock/Unlock works correctly")
}

// TestValueTypeMutexSequentialGlobalState verifies value-type mutex
// accumulates state across sequential calls.
func TestValueTypeMutexSequentialGlobalState(t *testing.T) {
	src := `
package main

import "sync"

var mu sync.Mutex
var counter int

func IncrementAndGet() int {
	mu.Lock()
	counter = counter + 1
	val := counter
	mu.Unlock()
	return val
}
`
	prog, err := gig.Build(src)
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	for i := 1; i <= 10; i++ {
		result, err := prog.Run("IncrementAndGet")
		if err != nil {
			t.Fatalf("call %d error: %v", i, err)
		}
		got := toInt64(result)
		if got != int64(i) {
			t.Fatalf("call %d: got %d, want %d", i, got, i)
		}
	}
	t.Logf("Value-type Mutex (sequential): 10 increments correct")
}

// TestValueTypeMutexConcurrentExact verifies value-type sync.Mutex provides
// real mutual exclusion under concurrent access — the EXACT same behavior
// as `var mu *sync.Mutex = &sync.Mutex{}`. This is the KEY test proving
// the fix stores a heap-allocated object, not per-call copies.
func TestValueTypeMutexConcurrentExact(t *testing.T) {
	src := `
package main

import "sync"

var mu sync.Mutex
var counter int

func Increment() int {
	mu.Lock()
	counter = counter + 1
	val := counter
	mu.Unlock()
	return val
}

func GetCounter() int {
	mu.Lock()
	val := counter
	mu.Unlock()
	return val
}
`
	prog, err := gig.Build(src)
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	const numGoroutines = 50
	const callsPerGoroutine = 20
	totalCalls := numGoroutines * callsPerGoroutine

	var wg sync.WaitGroup
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < callsPerGoroutine; j++ {
				_, err := prog.Run("Increment")
				if err != nil {
					t.Errorf("Increment error: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	result, err := prog.Run("GetCounter")
	if err != nil {
		t.Fatalf("GetCounter error: %v", err)
	}
	got := toInt64(result)
	if got != int64(totalCalls) {
		t.Fatalf("counter = %d, want exactly %d (mutex must prevent lost updates)", got, totalCalls)
	}
	t.Logf("Value-type Mutex (concurrent): counter = %d (exact, mutex works!)", got)
}

// TestValueTypeMapGlobal verifies that value-type sync.Map global works.
func TestValueTypeMapGlobal(t *testing.T) {
	src := `
package main

import "sync"

var m sync.Map

func StoreAndLoad() int {
	m.Store("key", 42)
	v, ok := m.Load("key")
	if ok && v == 42 {
		return 1
	}
	return 0
}
`
	prog, err := gig.Build(src)
	if err != nil {
		t.Fatalf("Build error: %v", err)
	}
	result, err := prog.Run("StoreAndLoad")
	if err != nil {
		t.Fatalf("StoreAndLoad error: %v", err)
	}
	got := toInt64(result)
	if got != 1 {
		t.Fatalf("got %d, want 1", got)
	}
	t.Logf("Value-type sync.Map: Store/Load works correctly")
}
