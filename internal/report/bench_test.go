package report

import (
	"io"
	"sync/atomic"
	"testing"
)

// devNullWriter discards every write but counts bytes so the
// benchmark exercises the bufio path. Avoids the per-process file
// open required by /dev/null on some sandboxes.
type devNullWriter struct{ bytes atomic.Uint64 }

func (d *devNullWriter) Write(p []byte) (int, error) {
	d.bytes.Add(uint64(len(p)))
	return len(p), nil
}

var _ io.Writer = (*devNullWriter)(nil)

// BenchmarkRecord_serial measures the single-goroutine producer cost
// of one cache.hit event end-to-end (Record + drain + bufio write).
// Uses WithBlockOnFull so the drain stays caught up and we measure
// steady-state throughput, not drop-rate.
func BenchmarkRecord_serial(b *testing.B) {
	w := NewWriter(&devNullWriter{}, WithBlockOnFull(), WithQueueSize(1024))
	defer w.Close()
	e := TargetResult{Status: "ok", CacheHit: true, Project: "apps/my-service", Target: "build", DurationMs: 342}
	b.ResetTimer()
	for range b.N {
		if err := Record(w, e); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRecord_serial_dropOnFull measures the producer cost under
// the default drop+count policy. The drain may fall behind and drops
// will accumulate; ns/op should still be very low because the hot
// path is a non-blocking select.
func BenchmarkRecord_serial_dropOnFull(b *testing.B) {
	w := NewWriter(&devNullWriter{}, WithQueueSize(1024))
	defer w.Close()
	e := TargetResult{Status: "ok", CacheHit: true, Project: "apps/my-service", Target: "build", DurationMs: 342}
	b.ResetTimer()
	for range b.N {
		_ = Record(w, e)
	}
}

// BenchmarkRecord_parallel measures contention behaviour under
// b.RunParallel. Demonstrates the win of an async writer over the
// previous Mutex-around-encode design.
func BenchmarkRecord_parallel(b *testing.B) {
	w := NewWriter(&devNullWriter{}, WithBlockOnFull(), WithQueueSize(8192))
	defer w.Close()
	e := TargetResult{Status: "ok", CacheHit: true, Project: "apps/my-service", Target: "build", DurationMs: 342}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := Record(w, e); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkRecord_mixed records a realistic ratio of cache vs graph
// events to surface any per-type cost imbalance.
func BenchmarkRecord_mixed(b *testing.B) {
	w := NewWriter(&devNullWriter{}, WithBlockOnFull(), WithQueueSize(8192))
	defer w.Close()
	hit := TargetResult{Status: "ok", CacheHit: true, Project: "a", Target: "build", DurationMs: 1}
	miss := TargetResult{Status: "ok", Project: "a", Target: "build", DurationMs: 1}
	gq := GraphQuery{Op: "affected", Nodes: 100, Seeds: 3, ResultCount: 5, DurationMs: 1}
	flk := VolatilityCall{Project: "a", Target: "test", Status: "retried_volatile", Attempts: 2}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		switch i % 10 {
		case 0, 1, 2, 3, 4, 5, 6:
			_ = Record(w, hit)
		case 7:
			_ = Record(w, miss)
		case 8:
			_ = Record(w, gq)
		case 9:
			_ = Record(w, flk)
		}
	}
}

// BenchmarkRecord_filtered measures the hot path when a filter denies
// the event. Should be the cheapest case: a single map lookup, no
// channel send.
func BenchmarkRecord_filtered(b *testing.B) {
	f, _ := ParseFilter([]string{"+cache.hit"})
	w := NewWriter(&devNullWriter{}, WithBlockOnFull(), WithFilter(f))
	defer w.Close()
	e := GraphQuery{Op: "affected", Nodes: 100}
	b.ResetTimer()
	for range b.N {
		_ = Record(w, e)
	}
}
