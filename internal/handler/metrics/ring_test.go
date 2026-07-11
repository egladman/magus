package metrics

import (
	"testing"

	metricsv1 "github.com/egladman/magus/proto/gen/go/magus/metrics/v1"
)

func sample(runs int64) *metricsv1.Sample {
	return &metricsv1.Sample{TargetRuns: runs}
}

func runsOf(samples []*metricsv1.Sample) []int64 {
	out := make([]int64, len(samples))
	for i, s := range samples {
		out[i] = s.TargetRuns
	}
	return out
}

func TestRingEmptySnapshot(t *testing.T) {
	r := newRing(3)
	if got := r.Snapshot(); len(got) != 0 {
		t.Fatalf("empty ring Snapshot = %v, want empty", got)
	}
}

func TestRingPartialFillOldestFirst(t *testing.T) {
	r := newRing(3)
	r.Append(sample(1))
	r.Append(sample(2))
	got := runsOf(r.Snapshot())
	want := []int64{1, 2}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Snapshot = %v, want %v", got, want)
	}
}

func TestRingWraparoundOldestFirst(t *testing.T) {
	r := newRing(3)
	for i := int64(1); i <= 5; i++ {
		r.Append(sample(i))
	}
	// Capacity 3, appended 1..5: oldest two (1,2) evicted, oldest-first 3,4,5.
	got := runsOf(r.Snapshot())
	want := []int64{3, 4, 5}
	if len(got) != len(want) {
		t.Fatalf("Snapshot len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Snapshot = %v, want %v", got, want)
		}
	}
}

func TestRingCapacityBound(t *testing.T) {
	r := newRing(4)
	for i := int64(0); i < 100; i++ {
		r.Append(sample(i))
	}
	if got := len(r.Snapshot()); got != 4 {
		t.Fatalf("Snapshot len = %d, want capacity 4", got)
	}
}

func TestRingIgnoresNil(t *testing.T) {
	r := newRing(2)
	r.Append(nil)
	r.Append(sample(7))
	got := runsOf(r.Snapshot())
	if len(got) != 1 || got[0] != 7 {
		t.Fatalf("Snapshot = %v, want [7]", got)
	}
}

func TestRingDefaultCapacity(t *testing.T) {
	r := newRing(0)
	if len(r.buf) != ringCapacity {
		t.Fatalf("newRing(0) capacity = %d, want %d", len(r.buf), ringCapacity)
	}
}
