package benchmarks

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/t04dJ14n9/gig"
	_ "github.com/t04dJ14n9/gig/stdlib/packages"
)

// ============================================================================
// Concurrent Stress Tests: Gig vs Native under heavy goroutine load
// ============================================================================

// ruleEngineSource simulates a realistic rule engine workload:
// parse input, string manipulation, math, conditional logic, stdlib calls.
const ruleEngineSource = `
import (
	"strings"
	"strconv"
	"math"
)

func EvaluateRule(userID int, name string, score float64) string {
	// String processing
	upper := strings.ToUpper(name)
	trimmed := strings.TrimSpace(upper)
	idStr := strconv.Itoa(userID)

	// Math computation
	adjusted := math.Sqrt(score) * 10.0

	// Business logic
	tier := "BRONZE"
	if adjusted > 80.0 {
		tier = "GOLD"
	} else if adjusted > 50.0 {
		tier = "SILVER"
	}

	// String building
	result := trimmed + "#" + idStr + ":" + tier
	if strings.Contains(result, "GOLD") {
		result = result + " [VIP]"
	}
	return result
}
`

// nativeEvaluateRule is the equivalent Go function for comparison.
func nativeEvaluateRule(userID int, name string, score float64) string {
	upper := strings.ToUpper(name)
	trimmed := strings.TrimSpace(upper)
	idStr := strconv.Itoa(userID)

	adjusted := math.Sqrt(score) * 10.0

	tier := "BRONZE"
	if adjusted > 80.0 {
		tier = "GOLD"
	} else if adjusted > 50.0 {
		tier = "SILVER"
	}

	result := trimmed + "#" + idStr + ":" + tier
	if strings.Contains(result, "GOLD") {
		result = result + " [VIP]"
	}
	return result
}

// computeHeavySource simulates a compute-heavy workload.
const computeHeavySource = `
func ComputeHeavy(n int) int {
	sum := 0
	for i := 0; i < n; i++ {
		for j := 0; j < 10; j++ {
			sum += i * j
		}
	}
	return sum
}
`

func nativeComputeHeavy(n int) int {
	sum := 0
	for i := 0; i < n; i++ {
		for j := 0; j < 10; j++ {
			sum += i * j
		}
	}
	return sum
}

// --- Helpers ---

func runConcurrentStress(b *testing.B, concurrency int, opsPerGoroutine int, fn func(goroutineID, opID int) error) {
	b.Helper()

	var totalOps atomic.Int64
	var totalErrors atomic.Int64

	b.ResetTimer()

	var wg sync.WaitGroup
	start := time.Now()

	for g := 0; g < concurrency; g++ {
		wg.Add(1)
		go func(gID int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				if err := fn(gID, i); err != nil {
					totalErrors.Add(1)
				}
				totalOps.Add(1)
			}
		}(g)
	}

	wg.Wait()
	elapsed := time.Since(start)

	b.StopTimer()

	ops := totalOps.Load()
	throughput := float64(ops) / elapsed.Seconds()
	errs := totalErrors.Load()

	b.ReportMetric(throughput, "ops/sec")
	b.ReportMetric(float64(elapsed.Nanoseconds())/float64(ops), "ns/op_actual")
	b.ReportMetric(float64(errs), "errors")

}

// ============================================================================
// Benchmark: Rule Engine Workload (Realistic)
// ============================================================================

func BenchmarkStress_Gig_RuleEngine_100G(b *testing.B) {
	prog, err := gig.Build(ruleEngineSource)
	if err != nil {
		b.Fatal(err)
	}
	// Verify correctness
	result, err := prog.Run("EvaluateRule", 42, " alice ", 81.0)
	if err != nil {
		b.Fatal(err)
	}
	b.Logf("Sample result: %v", result)

	concurrency := 100
	opsPerGoroutine := b.N / concurrency
	if opsPerGoroutine < 1 {
		opsPerGoroutine = 1
	}

	runConcurrentStress(b, concurrency, opsPerGoroutine, func(gID, opID int) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := prog.RunWithContext(ctx, "EvaluateRule", gID*1000+opID, " alice ", float64(50+opID%50))
		return err
	})
}

func BenchmarkStress_Native_RuleEngine_100G(b *testing.B) {
	concurrency := 100
	opsPerGoroutine := b.N / concurrency
	if opsPerGoroutine < 1 {
		opsPerGoroutine = 1
	}

	runConcurrentStress(b, concurrency, opsPerGoroutine, func(gID, opID int) error {
		runtime.KeepAlive(nativeEvaluateRule(gID*1000+opID, " alice ", float64(50+opID%50)))
		return nil
	})
}

func BenchmarkStress_Gig_RuleEngine_500G(b *testing.B) {
	prog, err := gig.Build(ruleEngineSource)
	if err != nil {
		b.Fatal(err)
	}
	concurrency := 500
	opsPerGoroutine := b.N / concurrency
	if opsPerGoroutine < 1 {
		opsPerGoroutine = 1
	}

	runConcurrentStress(b, concurrency, opsPerGoroutine, func(gID, opID int) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := prog.RunWithContext(ctx, "EvaluateRule", gID*1000+opID, " alice ", float64(50+opID%50))
		return err
	})
}

func BenchmarkStress_Native_RuleEngine_500G(b *testing.B) {
	concurrency := 500
	opsPerGoroutine := b.N / concurrency
	if opsPerGoroutine < 1 {
		opsPerGoroutine = 1
	}

	runConcurrentStress(b, concurrency, opsPerGoroutine, func(gID, opID int) error {
		runtime.KeepAlive(nativeEvaluateRule(gID*1000+opID, " alice ", float64(50+opID%50)))
		return nil
	})
}

// ============================================================================
// Benchmark: Compute-Heavy Workload
// ============================================================================

func BenchmarkStress_Gig_Compute_100G(b *testing.B) {
	prog, err := gig.Build(computeHeavySource)
	if err != nil {
		b.Fatal(err)
	}
	concurrency := 100
	opsPerGoroutine := b.N / concurrency
	if opsPerGoroutine < 1 {
		opsPerGoroutine = 1
	}

	runConcurrentStress(b, concurrency, opsPerGoroutine, func(gID, opID int) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := prog.RunWithContext(ctx, "ComputeHeavy", 100)
		return err
	})
}

func BenchmarkStress_Native_Compute_100G(b *testing.B) {
	concurrency := 100
	opsPerGoroutine := b.N / concurrency
	if opsPerGoroutine < 1 {
		opsPerGoroutine = 1
	}

	runConcurrentStress(b, concurrency, opsPerGoroutine, func(gID, opID int) error {
		runtime.KeepAlive(nativeComputeHeavy(100))
		return nil
	})
}

// ============================================================================
// Throughput test: sustained load for 5 seconds
// ============================================================================

func TestStress_Gig_Sustained5s(t *testing.T) {
	prog, err := gig.Build(ruleEngineSource)
	if err != nil {
		t.Fatal(err)
	}
	concurrencyLevels := []int{1, 10, 50, 100, 200, 500}

	for _, concurrency := range concurrencyLevels {
		t.Run(fmt.Sprintf("%dG", concurrency), func(t *testing.T) {
			var ops atomic.Int64
			var errors atomic.Int64
			var maxLatNs atomic.Int64

			duration := 3 * time.Second
			ctx, cancel := context.WithTimeout(context.Background(), duration)
			defer cancel()

			var memBefore, memAfter runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&memBefore)

			var wg sync.WaitGroup
			start := time.Now()

			for g := 0; g < concurrency; g++ {
				wg.Add(1)
				go func(gID int) {
					defer wg.Done()
					i := 0
					for {
						select {
						case <-ctx.Done():
							return
						default:
						}
						opStart := time.Now()
						execCtx, execCancel := context.WithTimeout(context.Background(), 30*time.Second)
						_, err := prog.RunWithContext(execCtx, "EvaluateRule", gID*10000+i, " bob ", float64(30+i%70))
						execCancel()
						lat := time.Since(opStart).Nanoseconds()

						if err != nil {
							errors.Add(1)
						}
						ops.Add(1)
						i++

						// Track max latency
						for {
							cur := maxLatNs.Load()
							if lat <= cur || maxLatNs.CompareAndSwap(cur, lat) {
								break
							}
						}
					}
				}(g)
			}

			wg.Wait()
			elapsed := time.Since(start)

			runtime.ReadMemStats(&memAfter)

			totalOps := ops.Load()
			totalErrors := errors.Load()
			throughput := float64(totalOps) / elapsed.Seconds()
			avgLatUs := float64(elapsed.Nanoseconds()) / float64(totalOps) / 1000.0
			maxLatMs := float64(maxLatNs.Load()) / 1e6
			heapAllocMB := float64(memAfter.TotalAlloc-memBefore.TotalAlloc) / 1024 / 1024
			gcPauses := memAfter.NumGC - memBefore.NumGC

			t.Logf("═══════════════════════════════════════════")
			t.Logf("  Concurrency:  %d goroutines", concurrency)
			t.Logf("  Duration:     %v", elapsed.Round(time.Millisecond))
			t.Logf("  Total Ops:    %d", totalOps)
			t.Logf("  Throughput:   %.0f ops/sec", throughput)
			t.Logf("  Avg Latency:  %.1f μs/op", avgLatUs)
			t.Logf("  Max Latency:  %.2f ms", maxLatMs)
			t.Logf("  Errors:       %d", totalErrors)
			t.Logf("  Heap Alloc:   %.1f MB", heapAllocMB)
			t.Logf("  GC Pauses:    %d", gcPauses)
			t.Logf("═══════════════════════════════════════════")

			if totalErrors > 0 {
				t.Errorf("Got %d errors during stress test", totalErrors)
			}
		})
	}
}
