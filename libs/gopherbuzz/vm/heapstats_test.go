package vm

import "testing"

// The heap only ever grows today, so live and peak agree - but the peak is tracked
// separately on purpose (see gHeapPeak) so this diagnostic survives a future
// compaction pass. Asserting the relationship rather than either number keeps the
// test honest through that change.
func TestHeapStatsGrowsAndRecordsAPeak(t *testing.T) {
	before, _ := HeapStats()
	const n = 1000
	for range n {
		gHeapAlloc(&strObj{})
	}
	after, peak := HeapStats()

	if got := after - before; got < n {
		t.Fatalf("allocated %d objects but the count rose by %d (%d -> %d)", n, got, before, after)
	}
	if peak < after {
		t.Fatalf("peak %d is below the live count %d; the high-water mark is not being recorded", peak, after)
	}
}

// An object count is the whole point: it must not depend on how big each value is.
// A magusfile that concatenates its way to 13GB does it with many small strings, not
// one large one, so a size-based reading would have looked unremarkable throughout.
func TestHeapStatsCountsObjectsNotBytes(t *testing.T) {
	before, _ := HeapStats()
	gHeapAlloc(&strObj{})
	small, _ := HeapStats()
	gHeapAlloc(&listObj{})
	large, _ := HeapStats()

	if small-before != 1 || large-small != 1 {
		t.Fatalf("each object must count once regardless of shape: %d -> %d -> %d", before, small, large)
	}
}
