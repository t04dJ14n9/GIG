package benchmarks

import (
	"context"
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

const extremeRuleSource = `
import (
	"strings"
	"strconv"
	"math"
)

func EvaluateRule(userID int, name string, score float64) string {
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
`

func nativeRule(userID int, name string, score float64) string {
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

func runStressLevel(concurrency int, duration time.Duration, fn func(gID, i int) error) (totalOps int64, totalErrors int64, heapMB float64, gcPauses uint32) {
	var ops atomic.Int64
	var errors atomic.Int64

	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	var memBefore, memAfter runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&memBefore)

	var wg sync.WaitGroup
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
				if err := fn(gID, i); err != nil {
					errors.Add(1)
				}
				ops.Add(1)
				i++
			}
		}(g)
	}
	wg.Wait()

	runtime.ReadMemStats(&memAfter)
	return ops.Load(), errors.Load(),
		float64(memAfter.TotalAlloc-memBefore.TotalAlloc) / 1024 / 1024,
		memAfter.NumGC - memBefore.NumGC
}

func TestExtremeStress(t *testing.T) {
	prog, err := gig.Build(extremeRuleSource)
	if err != nil {
		t.Fatal(err)
	}
	r, err := prog.Run("EvaluateRule", 1, " alice ", 81.0)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Correctness: %v", r)

	levels := []int{1, 10, 100, 500, 1000, 2000, 5000, 10000}
	duration := 3 * time.Second
	rounds := 3

	// Collect stats: [level][round]
	type stats struct {
		throughput float64
		avgLatUs   float64
		errors     int64
		heapMB     float64
		gcPauses   uint32
	}

	gigStats := make([][]stats, len(levels))
	for i := range gigStats {
		gigStats[i] = make([]stats, rounds)
	}

	t.Logf("Running %d rounds × %d levels...", rounds, len(levels))

	for round := 0; round < rounds; round++ {
		for li, concurrency := range levels {
			ops, errs, heap, gc := runStressLevel(concurrency, duration, func(gID, i int) error {
				execCtx, execCancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer execCancel()
				_, err := prog.RunWithContext(execCtx, "EvaluateRule", gID*10000+i, " bob ", float64(30+i%70))
				return err
			})
			tp := float64(ops) / duration.Seconds()
			gigStats[li][round] = stats{
				throughput: tp,
				avgLatUs:   1e6 / tp * float64(concurrency),
				errors:     errs,
				heapMB:     heap,
				gcPauses:   gc,
			}
		}
	}

	// Print Gig results (median of 3 rounds)
	t.Logf("")
	t.Logf("╔══════════════════════════════════════════════════════════════════════════════╗")
	t.Logf("║              GIG EXTREME STRESS TEST (%d rounds, %v/round)              ║", rounds, duration)
	t.Logf("║  Workload: rule engine (strings + math + stdlib)  |  CPU: %2d cores        ║", runtime.NumCPU())
	t.Logf("╠══════════╦═══════════════╦════════════╦════════╦══════════╦════════════════╣")
	t.Logf("║ Goroutin ║  Throughput   ║  Avg Lat   ║ Errors ║ Heap(MB) ║   GC Pauses    ║")
	t.Logf("╠══════════╬═══════════════╬════════════╬════════╬══════════╬════════════════╣")

	for li, concurrency := range levels {
		// Take median throughput
		var tps, lats []float64
		var totalErrs int64
		var totalHeap float64
		var totalGC uint32
		for round := 0; round < rounds; round++ {
			s := gigStats[li][round]
			tps = append(tps, s.throughput)
			lats = append(lats, s.avgLatUs)
			totalErrs += s.errors
			totalHeap += s.heapMB
			totalGC += s.gcPauses
		}
		// Simple median: sort 3 values, pick middle
		medTP := median3(tps[0], tps[1], tps[2])
		medLat := median3(lats[0], lats[1], lats[2])
		avgHeap := totalHeap / float64(rounds)
		avgGC := float64(totalGC) / float64(rounds)

		t.Logf("║ %8d ║ %11.0f/s ║ %8.1f μs ║ %6d ║ %6.0f MB ║ %14.0f ║",
			concurrency, medTP, medLat, totalErrs, avgHeap, avgGC)

		if totalErrs > 0 {
			t.Errorf("  ⚠ %d errors at concurrency=%d", totalErrs, concurrency)
		}
	}
	t.Logf("╚══════════╩═══════════════╩════════════╩════════╩══════════╩════════════════╝")

	// Native baseline (just throughput, no latency comparison)
	nativeStats := make([][]stats, 4)
	nativeLevels := []int{1, 100, 1000, 10000}
	for i := range nativeStats {
		nativeStats[i] = make([]stats, rounds)
	}

	for round := 0; round < rounds; round++ {
		for li, concurrency := range nativeLevels {
			ops, _, heap, gc := runStressLevel(concurrency, duration, func(gID, i int) error {
				_ = nativeRule(gID*10000+i, " bob ", float64(30+i%70))
				return nil
			})
			tp := float64(ops) / duration.Seconds()
			nativeStats[li][round] = stats{
				throughput: tp,
				heapMB:     heap,
				gcPauses:   gc,
			}
		}
	}

	t.Logf("")
	t.Logf("╔══════════════════════════════════════════════════════════════════════════════╗")
	t.Logf("║                    NATIVE GO BASELINE (throughput only)                     ║")
	t.Logf("╠══════════╦═══════════════╦══════════╦════════════════╗")
	t.Logf("║ Goroutin ║  Throughput   ║ Heap(MB) ║   GC Pauses    ║")
	t.Logf("╠══════════╬═══════════════╬══════════╬════════════════╣")

	for li, concurrency := range nativeLevels {
		var tps []float64
		var totalHeap float64
		var totalGC uint32
		for round := 0; round < rounds; round++ {
			s := nativeStats[li][round]
			tps = append(tps, s.throughput)
			totalHeap += s.heapMB
			totalGC += s.gcPauses
		}
		medTP := median3(tps[0], tps[1], tps[2])
		avgHeap := totalHeap / float64(rounds)
		avgGC := float64(totalGC) / float64(rounds)

		t.Logf("║ %8d ║ %11.0f/s ║ %6.0f MB ║ %14.0f ║",
			concurrency, medTP, avgHeap, avgGC)
	}
	t.Logf("╚══════════╩═══════════════╩══════════╩════════════════╝")

	// Ratio summary
	t.Logf("")
	t.Logf("Throughput ratio (Native / Gig):")
	for li, concurrency := range nativeLevels {
		var gigTP float64
		for gli, gc := range levels {
			if gc == concurrency {
				var tps []float64
				for round := 0; round < rounds; round++ {
					tps = append(tps, gigStats[gli][round].throughput)
				}
				gigTP = median3(tps[0], tps[1], tps[2])
				break
			}
		}
		if gigTP > 0 {
			var ntps []float64
			for round := 0; round < rounds; round++ {
				ntps = append(ntps, nativeStats[li][round].throughput)
			}
			nativeTP := median3(ntps[0], ntps[1], ntps[2])
			t.Logf("  %5dG: Native %.0f/s vs Gig %.0f/s = %.1fx", concurrency, nativeTP, gigTP, nativeTP/gigTP)
		}
	}
}

func median3(a, b, c float64) float64 {
	if a > b {
		a, b = b, a
	}
	if b > c {
		b = c
	}
	if a > b {
		b = a
	}
	return b
}
