//go:build !buzz_safe && !buzz_unsafe

package vm

import "testing"

// Live and peak agree until an owner release lowers the live count, and the peak
// is what a diagnostic reports after that. Asserting the relationship rather
// than either number keeps the test honest either side of a release.
func TestHeapStatsGrowsAndRecordsAPeak(t *testing.T) {
	before := ReadHeapStats().Objects
	const n = 1000
	for range n {
		gHeapAlloc(&strObj{}, nil)
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
	gHeapAlloc(&strObj{}, nil)
	small := ReadHeapStats().Objects
	gHeapAlloc(&listObj{}, nil)
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
		gHeapAlloc(&strObj{}, nil)
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
		gHeapAlloc(&strObj{}, nil)
	}
	after := ReadHeapStats()
	if after.Peak < 100 || after.Peak >= busy.Peak {
		t.Fatalf("the second invocation must report its own peak (~100), not the first's (%d), got %d",
			busy.Peak, after.Peak)
	}
}

// A released owner hands its slots back: the live count drops and the next
// allocation reuses a slot instead of growing the table.
func TestOwnerReleaseFreesAndReusesSlots(t *testing.T) {
	a := new(Owner)
	before := ReadHeapStats().Objects
	idx := gHeapAlloc(&listObj{}, a)
	if got := ReadHeapStats().Objects; got != before+1 {
		t.Fatalf("live count after one owned allocation: %d, want %d", got, before+1)
	}
	a.Release()
	if got := ReadHeapStats().Objects; got != before {
		t.Fatalf("live count after release: %d, want %d", got, before)
	}
	if raceEnabled {
		if again := gHeapAlloc(&listObj{}, nil); again == idx {
			t.Fatalf("the race build recycled released slot %d; it must stay poisoned", idx)
		}
		return
	}
	if gHeapGet(idx) != nil {
		t.Fatalf("slot %d still holds its object after release", idx)
	}
	if again := gHeapAlloc(&listObj{}, nil); again != idx {
		t.Fatalf("next allocation took slot %d; want the released slot %d", again, idx)
	}
}

// Claim takes what no owner holds and stops at what one does, so releasing the
// adopter leaves the other owner's values intact.
func TestOwnerClaimStopsAtAnotherOwner(t *testing.T) {
	owner, adopter := new(Owner), new(Owner)
	inner := encodeHeap(tagList, gHeapAlloc(&listObj{}, owner))
	outer := ListValue([]Value{inner})
	adopter.Claim(outer)
	adopter.Release()
	if !freed(outer) {
		t.Fatal("the adopted list survived its adopter's release")
	}
	if _, ok := nanboxObj(inner).(*listObj); !ok {
		t.Fatal("the adopter released a slot another owner owns")
	}
	owner.Release()
}

// A member stored on a claimed map after the claim is unowned until something
// hands it over: MapSet does so at once, and a Release given the map as a root
// finds what reached the map another way.
func TestOwnerTakesMembersAttachedAfterTheClaim(t *testing.T) {
	var a Owner
	m := NewMap()
	a.Claim(m)
	viaSet, viaWalk := ListValue(nil), ListValue(nil)
	m.MapSet("set", viaSet)
	if gHeapOwner[uint64(viaSet)&idxMaskHeap] != &a {
		t.Fatal("MapSet on an owned map left the value unowned")
	}
	m.asMap().set("walk", viaWalk)
	if gHeapOwner[uint64(viaWalk)&idxMaskHeap] != nil {
		t.Fatal("a value stored past the owner is owned before anything reached it")
	}
	a.Release(m)
	if !freed(viaSet) || !freed(viaWalk) {
		t.Fatal("attached members survived the release")
	}
}

// freed reports whether v's slot was released: emptied, or poisoned under -race.
// It reads the table directly, since gHeapGet panics on a poisoned slot.
func freed(v Value) bool {
	switch (*gHeapPtr.Load())[uint64(v)&idxMaskHeap].(type) {
	case nil, released:
		return true
	}
	return false
}

// Under the race detector a released slot is never recycled, and reading a
// Value into one panics by name instead of quietly reading a later object.
func TestReleasedSlotReadPanicsUnderRace(t *testing.T) {
	if !raceEnabled {
		t.Skip("released slots are poisoned only under -race")
	}
	var a Owner
	v := encodeHeap(tagList, gHeapAlloc(&listObj{}, &a))
	a.Release()
	defer func() {
		if got := recover(); got != "buzz: value used after its session closed" {
			t.Fatalf("read of a released slot: got %v, want the use-after-close panic", got)
		}
	}()
	_ = v.asList()
}
