//go:build !buzz_safe && !buzz_unsafe

package vm

import (
	"context"
	"testing"
)

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
	if poisonReleased {
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

// A definition one session holds is read from another: the case that session
// interns first stays in the definition's table after it closes.
func TestEnumCaseLivesAsLongAsItsDefinition(t *testing.T) {
	var holder, reader Owner
	def := EnumDefValue("Tier", []string{"low", "high"}, nil)
	holder.Claim(def)
	vm := NewVM(t.Context())
	vm.SetOwner(&reader)
	high := vm.enumCase(def, 1)
	reader.Release()
	if freed(high) {
		t.Fatal("the reader's release freed a case its definition still holds")
	}
	if _, name, ok := NewVM(t.Context()).enumCase(def, 1).EnumCase(); !ok || name != "high" {
		t.Fatalf("a later reader got case %q, ok=%v; want high", name, ok)
	}
	holder.Release()
	if !freed(high) {
		t.Fatal("the case outlived its definition's owner")
	}
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
	if !poisonReleased {
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

// UnownedObjects counts what no release can reach: a claim takes a slot out
// of it, and a string never enters it.
func TestUnownedObjectsCountsUnownedSlotsButStrings(t *testing.T) {
	unowned := func() int { return ReadHeapStats().UnownedObjects }
	before := unowned()
	list := ListValue(nil)
	gHeapAlloc(&strObj{}, nil)
	if got := unowned() - before; got != 1 {
		t.Fatalf("one unowned list and one string: UnownedObjects rose by %d, want 1", got)
	}
	o := &Owner{}
	o.Claim(list)
	vm := NewVM(context.Background())
	vm.SetOwner(o)
	alloc(vm, tagList, &listObj{})
	if got := unowned() - before; got != 0 {
		t.Fatalf("after the claim and an owned allocation UnownedObjects is %+d, want +0", got)
	}
	o.Release()
	if got := unowned() - before; got != 0 {
		t.Fatalf("a release frees owned slots only, yet UnownedObjects moved %+d", got)
	}
}

// A builtin's fresh result belongs to the calling VM from the start, so the
// adopt at the call site walks none of its items.
func TestBuiltinResultIsOwnedByItsVM(t *testing.T) {
	ctx := context.Background()
	o := &Owner{}
	vm := NewVM(ctx)
	vm.SetOwner(o)
	list := ListValue([]Value{IntValue(1), IntValue(2)})
	m := NewMap()
	m.MapSet("k", IntValue(1))
	calls := map[string]*directObj{
		"list.reverse": listMethod(vm, list, "reverse"),
		"list.clone":   listMethod(vm, list, "clone"),
		"map.keys":     mapMethod(vm, m, "keys"),
		"map.clone":    mapMethod(vm, m, "clone"),
	}
	for name, d := range calls {
		got, err := d.Fn(ctx, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if owner := gHeapOwner[uint64(got)&idxMaskHeap]; owner != o {
			t.Fatalf("%s returned a slot owned by %p, want the VM's owner %p", name, owner, o)
		}
	}
	o.Release()
}

// BenchmarkClaimIntList claims a fresh 2,000-int list, the shape a host
// callable hands back: the claim walk holds gHeapMu throughout.
func BenchmarkClaimIntList(b *testing.B) {
	items := make([]Value, 2000)
	for i := range items {
		items[i] = IntValue(int64(i))
	}
	b.ReportAllocs()
	for b.Loop() {
		o := &Owner{}
		o.Claim(ListValue(items))
		o.Release()
	}
}
