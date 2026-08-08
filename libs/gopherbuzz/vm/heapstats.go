//go:build !buzz_safe && !buzz_unsafe

package vm

import "sync/atomic"

// HeapStats is a snapshot of the global object heap.
type HeapStats struct {
	// Objects is the live object count.
	Objects int
	// Peak is the high-water count since the last ResetHeapStats, or since the
	// process started when it has never been called.
	Peak int
}

// ReadHeapStats returns the heap's live and peak object counts.
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
func ReadHeapStats() HeapStats {
	s := gHeapPtr.Load()
	live := 0
	if s != nil {
		live = len(*s)
	}
	peak := int(gHeapPeak.Load()) - int(gHeapBase.Load())
	if peak < 0 {
		peak = 0
	}
	return HeapStats{Objects: live, Peak: peak}
}

// ResetHeapStats rebases the peak and clears the growth attribution, so the next
// report describes only what follows.
//
// The daemon is one process serving many invocations against a heap that never
// shrinks. Without a rebase the first run's peak is reported against every later
// run, and the attribution names a magusfile that finished hours ago - the
// diagnostic would confidently accuse the wrong file. Callers rebase at the start
// of an invocation.
//
// The live object count is deliberately NOT rebased: it is a property of the
// process, and a daemon whose heap is already enormous should say so.
func ResetHeapStats() {
	gHeapBase.Store(gHeapPeak.Load())
	resetHeapAttr()
}

// gHeapPeak is the heap's high-water object count, maintained by gHeapAlloc under
// gHeapMu. gHeapBase is what ResetHeapStats subtracts so a peak describes one
// invocation rather than the life of a daemon. Tracked separately from the
// live length because the two agree only until something reclaims: the planned
// compaction pass would make them diverge exactly when the peak is the number
// worth reporting.
var (
	gHeapPeak atomic.Int64
	gHeapBase atomic.Int64
)
