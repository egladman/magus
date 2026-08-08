//go:build !buzz_safe && !buzz_unsafe

package vm

import "testing"

// The heap only ever grows today, so live and peak agree - but the peak is tracked
// separately on purpose (see gHeapPeak) so this diagnostic survives a future
// compaction pass. Asserting the relationship rather than either number keeps the
// test honest through that change.
func TestHeapStatsGrowsAndRecordsAPeak(t *testing.T) {
	before := ReadHeapStats().Objects
	const n = 1000
	for range n {
		gHeapAlloc(&strObj{})
	}
	st := ReadHeapStats()
	after, peak := st.Objects, st.Peak

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
	before := ReadHeapStats().Objects
	gHeapAlloc(&strObj{})
	small := ReadHeapStats().Objects
	gHeapAlloc(&listObj{})
	large := ReadHeapStats().Objects

	if small-before != 1 || large-small != 1 {
		t.Fatalf("each object must count once regardless of shape: %d -> %d -> %d", before, small, large)
	}
}

// The daemon case. One process serves many invocations against a heap that never
// shrinks, so without a rebase the first run's peak is reported against every
// later run and the attribution names a magusfile that finished hours ago.
func TestResetHeapStatsRebasesThePeak(t *testing.T) {
	for range 5000 {
		gHeapAlloc(&strObj{})
	}
	busy := ReadHeapStats()
	if busy.Peak < 5000 {
		t.Fatalf("expected a peak of at least 5000 after allocating, got %d", busy.Peak)
	}

	ResetHeapStats()
	quiet := ReadHeapStats()
	if quiet.Peak != 0 {
		t.Fatalf("a rebased peak must start at 0, got %d", quiet.Peak)
	}
	if quiet.Objects < busy.Objects {
		t.Fatalf("the live count is a property of the process and must NOT be rebased: %d -> %d",
			busy.Objects, quiet.Objects)
	}

	for range 100 {
		gHeapAlloc(&strObj{})
	}
	after := ReadHeapStats()
	if after.Peak < 100 || after.Peak >= busy.Peak {
		t.Fatalf("the second invocation must report its own peak (~100), not the first's (%d), got %d",
			busy.Peak, after.Peak)
	}
}
