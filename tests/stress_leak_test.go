// Package tests - stress_leak_test.go
//
// Long-running stress benchmarks to detect memory leaks in concurrent gig programs.
// Run with: go test ./tests/ -bench BenchmarkStress_MemoryLeak -benchtime 30m -timeout 6h -v
//
// These are benchmarks (not tests) so they don't run during `go test ./...`.
package tests

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/t04dJ14n9/gig"
	_ "github.com/t04dJ14n9/gig/stdlib/packages"
)

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

// runStressMemoryLeak is the core stress test logic, used by all benchmark variants.
// duration controls how long the test runs.
func runStressMemoryLeak(b *testing.B, duration time.Duration) {
	b.Helper()

	b.Logf("Starting %v memory leak stress benchmark", duration)

	// Build program once
	prog, err := gig.Build(ruleEngineSource)
	if err != nil {
		b.Fatalf("Build error: %v", err)
	}
	// Memory tracking
	var memStats runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&memStats)

	// Baseline measurements
	baselineHeapAlloc := memStats.HeapAlloc
	baselineHeapSys := memStats.HeapSys
	baselineGoroutines := runtime.NumGoroutine()

	b.Logf("══════════════════════════════════════════════════════════════")
	b.Logf("BASELINE MEASUREMENTS")
	b.Logf("══════════════════════════════════════════════════════════════")
	b.Logf("  HeapAlloc:    %d MB", baselineHeapAlloc/1024/1024)
	b.Logf("  HeapSys:      %d MB", baselineHeapSys/1024/1024)
	b.Logf("  Goroutines:   %d", baselineGoroutines)
	b.Logf("══════════════════════════════════════════════════════════════")

	// Create log file for memory tracking
	logFile, err := os.Create("stress_memory_leak.log")
	if err != nil {
		b.Fatalf("Failed to create log file: %v", err)
	}
	defer logFile.Close()

	// Write header to log file
	fmt.Fprintf(logFile, "Timestamp,HeapAllocMB,HeapSysMB,HeapInUseMB,HeapIdleMB,StackInUseMB,NumGC,Goroutines,TotalOps,OngoingOps,Errors\n")

	// Test configuration
	concurrency := 20 // Reduced from 50 to lower mutex contention
	reportInterval := 1 * time.Minute
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	// Metrics
	var totalOps atomic.Int64
	var ongoingOps atomic.Int64
	var errors atomic.Int64

	// Start worker goroutines
	var wg sync.WaitGroup
	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()

	// Channel for graceful shutdown
	stopCh := make(chan struct{})

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			opCounter := 0
			for {
				select {
				case <-workerCtx.Done():
					return
				case <-stopCh:
					return
				default:
					ongoingOps.Add(1)
					opCounter++

					// Execute with timeout to prevent stuck operations
					execCtx, execCancel := context.WithTimeout(context.Background(), 5*time.Second)
					_, err := prog.RunWithContext(execCtx, "EvaluateRule", workerID*100000+opCounter, " test_user ", float64(25+opCounter%50))
					execCancel()

					if err != nil {
						errors.Add(1)
					}
					totalOps.Add(1)
					ongoingOps.Add(-1)
				}
			}
		}(i)
	}

	// Monitor goroutine - runs every minute
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		ticker := time.NewTicker(reportInterval)
		defer ticker.Stop()

		startTime := time.Now()
		iteration := 0

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				iteration++
				runtime.GC()
				runtime.ReadMemStats(&memStats)

				elapsed := time.Since(startTime)
				heapAllocMB := float64(memStats.HeapAlloc) / 1024 / 1024
				heapSysMB := float64(memStats.HeapSys) / 1024 / 1024
				heapInUseMB := float64(memStats.HeapInuse) / 1024 / 1024
				heapIdleMB := float64(memStats.HeapIdle) / 1024 / 1024
				stackInUseMB := float64(memStats.StackInuse) / 1024 / 1024
				numGC := memStats.NumGC
				goroutines := runtime.NumGoroutine()

				ops := totalOps.Load()
				errCount := errors.Load()
				ongoing := ongoingOps.Load()

				// Calculate growth rate
				heapGrowthMB := float64(memStats.HeapAlloc-baselineHeapAlloc) / 1024 / 1024
				heapGrowthPercent := float64(memStats.HeapAlloc-baselineHeapAlloc) / float64(baselineHeapAlloc) * 100

				// Log to file
				fmt.Fprintf(logFile, "%s,%.2f,%.2f,%.2f,%.2f,%.2f,%d,%d,%d,%d,%d\n",
					time.Now().Format(time.RFC3339),
					heapAllocMB, heapSysMB, heapInUseMB, heapIdleMB,
					stackInUseMB, numGC, goroutines, ops, ongoing, errCount)

				// Log to test output
				b.Logf("══════════════════════════════════════════════════════════════")
				b.Logf("ITERATION %d | Elapsed: %v", iteration, elapsed.Round(time.Second))
				b.Logf("══════════════════════════════════════════════════════════════")
				b.Logf("  HeapAlloc:    %.2f MB (growth: %.2f MB, %.1f%%)", heapAllocMB, heapGrowthMB, heapGrowthPercent)
				b.Logf("  HeapSys:      %.2f MB", heapSysMB)
				b.Logf("  HeapInUse:    %.2f MB", heapInUseMB)
				b.Logf("  HeapIdle:     %.2f MB", heapIdleMB)
				b.Logf("  StackInUse:   %.2f MB", stackInUseMB)
				b.Logf("  NumGC:        %d", numGC)
				b.Logf("  Goroutines:   %d (baseline: %d)", goroutines, baselineGoroutines)
				b.Logf("  TotalOps:     %d", ops)
				b.Logf("  OngoingOps:   %d", ongoing)
				b.Logf("  Errors:       %d", errCount)
				b.Logf("══════════════════════════════════════════════════════════════")

				// Leak detection thresholds
				// After 30 minutes, if heap has grown more than 100MB, warn
				if elapsed > 30*time.Minute && heapGrowthMB > 100 {
					b.Logf("⚠️  WARNING: Potential memory leak detected!")
					b.Logf("   Heap has grown %.2f MB in %v", heapGrowthMB, elapsed)
				}

				// If heap growth exceeds 200MB, fail the test
				if heapGrowthMB > 200 {
					b.Errorf("🚨 CRITICAL: Memory leak detected! Heap grew %.2f MB", heapGrowthMB)
				}

				// Check for goroutine leak
				// Expected goroutines: baseline + workers + monitor (up to 3)
				expectedGoroutines := baselineGoroutines + concurrency + 3
				// Only warn if goroutines exceed expected count significantly
				if goroutines > expectedGoroutines+10 {
					b.Errorf("🚨 CRITICAL: Goroutine leak detected! %d goroutines (expected max %d)", goroutines, expectedGoroutines)
				}
			}
		}
	}()

	// Wait for test duration
	<-ctx.Done()

	// Graceful shutdown
	b.Logf("Stopping workers...")
	close(stopCh) // Signal workers to stop

	// Give workers time to finish current operations
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		b.Logf("All workers stopped gracefully")
	case <-time.After(30 * time.Second):
		b.Logf("Timeout waiting for workers, forcing shutdown")
		workerCancel()
		wg.Wait()
	}

	<-monitorDone

	// Final measurement
	runtime.GC()
	runtime.ReadMemStats(&memStats)

	b.Logf("\n")
	b.Logf("══════════════════════════════════════════════════════════════")
	b.Logf("FINAL REPORT")
	b.Logf("══════════════════════════════════════════════════════════════")
	b.Logf("  Duration:          %v", duration)
	b.Logf("  Total Operations:  %d", totalOps.Load())
	b.Logf("  Errors:            %d", errors.Load())
	b.Logf("  Ops/Second:        %.0f", float64(totalOps.Load())/duration.Seconds())
	b.Logf("────────────────────────────────────────────────────────────────")
	b.Logf("  Baseline HeapAlloc:  %d MB", baselineHeapAlloc/1024/1024)
	b.Logf("  Final HeapAlloc:     %d MB", memStats.HeapAlloc/1024/1024)
	b.Logf("  Heap Growth:         %.2f MB", float64(memStats.HeapAlloc-baselineHeapAlloc)/1024/1024)
	b.Logf("────────────────────────────────────────────────────────────────")
	b.Logf("  Baseline Goroutines: %d", baselineGoroutines)
	b.Logf("  Final Goroutines:    %d", runtime.NumGoroutine())
	b.Logf("  Goroutine Growth:    %d", runtime.NumGoroutine()-baselineGoroutines)
	b.Logf("────────────────────────────────────────────────────────────────")
	b.Logf("  NumGC:               %d", memStats.NumGC)
	b.Logf("  GCSys:               %d MB", memStats.GCSys/1024/1024)
	b.Logf("══════════════════════════════════════════════════════════════")
	b.Logf("Log file saved to: stress_memory_leak.log")

	// Determine pass/fail
	heapGrowthMB := float64(memStats.HeapAlloc-baselineHeapAlloc) / 1024 / 1024
	goroutineGrowth := runtime.NumGoroutine() - baselineGoroutines

	if heapGrowthMB > 50 {
		b.Errorf("FAIL: Heap growth %.2f MB exceeds 50 MB threshold", heapGrowthMB)
	}

	if goroutineGrowth > 5 {
		b.Errorf("FAIL: Goroutine growth %d exceeds 5 threshold", goroutineGrowth)
	}

	if heapGrowthMB <= 50 && goroutineGrowth <= 5 {
		b.Logf("✅ PASS: No memory leaks detected!")
	}
}

// ============================================================================
// Benchmark variants (only run with -bench flag)
// ============================================================================

// BenchmarkStress_MemoryLeak runs a 5-hour memory leak stress test.
// Usage: go test ./tests/ -bench BenchmarkStress_MemoryLeak$ -benchtime 1x -timeout 6h -v
func BenchmarkStress_MemoryLeak(b *testing.B) {
	durationStr := os.Getenv("STRESS_DURATION")
	duration := 5 * time.Hour
	if durationStr != "" {
		if d, err := time.ParseDuration(durationStr); err == nil {
			duration = d
		}
	}
	for i := 0; i < b.N; i++ {
		runStressMemoryLeak(b, duration)
	}
}

// BenchmarkStress_MemoryLeak_Short runs a 30-minute memory leak stress test.
// Usage: go test ./tests/ -bench BenchmarkStress_MemoryLeak_Short -benchtime 1x -timeout 1h -v
func BenchmarkStress_MemoryLeak_Short(b *testing.B) {
	for i := 0; i < b.N; i++ {
		runStressMemoryLeak(b, 30*time.Minute)
	}
}

// BenchmarkStress_MemoryLeak_Quick runs a 5-minute memory leak stress test.
// Usage: go test ./tests/ -bench BenchmarkStress_MemoryLeak_Quick -benchtime 1x -timeout 10m -v
func BenchmarkStress_MemoryLeak_Quick(b *testing.B) {
	for i := 0; i < b.N; i++ {
		runStressMemoryLeak(b, 5*time.Minute)
	}
}
