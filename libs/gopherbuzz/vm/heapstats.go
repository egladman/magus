package vm

import "sync/atomic"

// HeapStats reports the size of the global object heap: how many objects it holds,
// and the high-water mark it has reached.
//
// This is the number that explains a class of failure nothing else can. Every heap
// object a Buzz program allocates - every string, list, map and object instance -
// is appended to one process-wide slice and NEVER removed (see the "Global heap
// table" note in value_nanbox.go: objects are pinned for the process lifetime).
// That is a deliberate trade for short-lived sessions, and it means a program can
// consume unbounded memory without any single value being large and without the Go
// heap profile pointing anywhere useful.
//
// The failure that motivated exporting it: a magusfile helper built a string with
// `kept = kept + line` over a 30,803-line file. Each concatenation allocated a new
// string that then survived to the end of the run. Output was 2.1MB; peak RSS was
// 13.1GB, which killed a 16GB CI runner outright. Nothing in the log named Buzz -
// the shard simply stopped, and the visible suspects (a race-enabled `go test`,
// the runner itself) were all innocent and all measured before the real cause was
// found. An object count would have said so immediately.
//
// Objects, not bytes: the slice holds interfaces to values of many shapes, and
// summing their true sizes would mean walking every one on every call. The count
// is O(1), and the SHAPE of its growth is what diagnoses this - a program whose
// object count climbs with its input size is quadratic in memory regardless of what
// each object weighs.
//
// Safe to call from any goroutine, including while a program runs.
func HeapStats() (objects int, peak int) {
	s := gHeapPtr.Load()
	if s == nil {
		return 0, int(gHeapPeak.Load())
	}
	return len(*s), int(gHeapPeak.Load())
}

// gHeapPeak is the high-water mark of the heap's object count.
//
// Tracked separately from the live length because the length only ever grows, so
// the two agree today - and a future compaction pass (the "M5 can add safe-point
// compaction" note on the heap) would make them diverge exactly when the peak is
// the number worth reporting. Recording it now means the diagnostic keeps working
// through that change rather than quietly starting to under-report.
var gHeapPeak atomic.Int64

// recordHeapPeak updates the high-water mark. Called under gHeapMu from
// gHeapAlloc, so it needs no compare-and-swap loop of its own.
func recordHeapPeak(n int) {
	if int64(n) > gHeapPeak.Load() {
		gHeapPeak.Store(int64(n))
	}
}
