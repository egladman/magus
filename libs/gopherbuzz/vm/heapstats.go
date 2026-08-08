//go:build !buzz_safe && !buzz_unsafe

package vm

import "sync/atomic"

// HeapStats reports the global heap's live object count and its high-water mark.
//
// Every heap object a Buzz program allocates goes on one process-wide slice and
// is never removed (see the heap table note in value_nanbox.go). A program can
// therefore exhaust a machine with no single value being large and nothing in a
// Go heap profile pointing anywhere useful. One magusfile built a string with
// `kept = kept + line` over 30,803 lines: 2.1MB of output, 13.1GB of pinned
// intermediates, one dead 16GB runner.
//
// Objects rather than bytes, because the count is O(1) and the shape of its
// growth is what diagnoses this. A program whose object count tracks its input
// size is quadratic in memory whatever each object weighs.
//
// Safe to call from any goroutine.
func HeapStats() (objects int, peak int) {
	s := gHeapPtr.Load()
	if s == nil {
		return 0, int(gHeapPeak.Load())
	}
	return len(*s), int(gHeapPeak.Load())
}

// gHeapPeak is the heap's high-water object count. Tracked separately from the
// live length because the two agree only until something reclaims: the planned
// compaction pass would make them diverge exactly when the peak is the number
// worth reporting.
var gHeapPeak atomic.Int64

// recordHeapPeak updates the high-water mark. gHeapAlloc calls it under gHeapMu,
// so it needs no compare-and-swap of its own.
func recordHeapPeak(n int) {
	if int64(n) > gHeapPeak.Load() {
		gHeapPeak.Store(int64(n))
	}
}
