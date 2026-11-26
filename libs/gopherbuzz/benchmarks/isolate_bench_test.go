package benchmarks

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
)

// BenchmarkIsolateScaling runs N independent sessions on N goroutines. Perfect
// isolation holds wall-ns/run flat as N grows; contention shows as growth.
func BenchmarkIsolateScaling(b *testing.B) {
	ctx := context.Background()
	for _, n := range []int{1, 2, 4, 8} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			var total time.Duration
			for range b.N {
				sessions := make([]*buzz.Session, n)
				for i := range sessions {
					sessions[i] = buzz.NewSession(ctx, buzz.WithEmbedded())
				}
				errs := make([]error, n)
				var wg sync.WaitGroup
				start := time.Now()
				for i := range sessions {
					wg.Add(1)
					go func() {
						defer wg.Done()
						errs[i] = sessions[i].Exec(ctx, workSource)
					}()
				}
				wg.Wait()
				total += time.Since(start)
				for _, err := range errs {
					if err != nil {
						b.Fatal(err)
					}
				}
			}
			b.ReportMetric(float64(total.Nanoseconds())/float64(b.N), "wall-ns/run")
			b.ReportMetric(float64(runtime.GOMAXPROCS(0)), "gomaxprocs")
		})
	}
}

// BenchmarkIsolateMemory reports the heap one live session holds, before and
// after running the script, averaged over 64 sessions.
func BenchmarkIsolateMemory(b *testing.B) {
	ctx := context.Background()
	const count = 64
	var fresh, ran float64
	for range b.N {
		held := make([]*buzz.Session, count)
		base := heapInUse()
		for i := range held {
			held[i] = buzz.NewSession(ctx, buzz.WithEmbedded())
		}
		afterNew := heapInUse()
		for _, s := range held {
			if err := s.Exec(ctx, workSource); err != nil {
				b.Fatal(err)
			}
		}
		afterRun := heapInUse()
		fresh += float64(afterNew-base) / count
		ran += float64(afterRun-base) / count
		runtime.KeepAlive(held)
	}
	b.ReportMetric(fresh/float64(b.N), "fresh-B/session")
	b.ReportMetric(ran/float64(b.N), "ran-B/session")
}
