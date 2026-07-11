package metrics

import (
	"sync"

	metricsv1 "github.com/egladman/magus/proto/gen/go/magus/metrics/v1"
)

// ringCapacity bounds the utilization/activity history the daemon keeps for backfill:
// 300 samples at the sampler's 1Hz tick is a 5-minute rolling window.
const ringCapacity = 300

// ring is a concurrency-safe bounded ring buffer of samples. Once full, each Append
// overwrites the oldest entry; Snapshot returns an oldest-first copy for the Backfill frame.
type ring struct {
	mu   sync.Mutex
	buf  []*metricsv1.Sample
	next int  // index the next Append writes to
	full bool // whether buf has wrapped at least once
}

// newRing returns an empty ring with the given capacity. A capacity of zero or less falls
// back to ringCapacity.
func newRing(capacity int) *ring {
	if capacity <= 0 {
		capacity = ringCapacity
	}
	return &ring{buf: make([]*metricsv1.Sample, capacity)}
}

// Append adds one sample, overwriting the oldest when the ring is full. Nil samples are
// ignored so a Snapshot never returns a nil entry.
func (r *ring) Append(s *metricsv1.Sample) {
	if s == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf[r.next] = s
	r.next++
	if r.next == len(r.buf) {
		r.next = 0
		r.full = true
	}
}

// Snapshot returns the buffered samples oldest-first, as a fresh slice the caller owns.
func (r *ring) Snapshot() []*metricsv1.Sample {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.full {
		out := make([]*metricsv1.Sample, r.next)
		copy(out, r.buf[:r.next])
		return out
	}
	out := make([]*metricsv1.Sample, 0, len(r.buf))
	out = append(out, r.buf[r.next:]...)
	out = append(out, r.buf[:r.next]...)
	return out
}
