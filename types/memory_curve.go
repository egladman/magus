package types

import (
	"fmt"
	"time"

	"github.com/egladman/magus/internal/json"
)

// MemoryCurveBuckets is how many equal slices of a run's duration a MemoryCurve
// holds, 5% each. Buckets are fractions of the run, not seconds, so a slow run
// and a fast one of the same work line up phase by phase.
const MemoryCurveBuckets = 20

// MemoryCurve is a run's resident memory over its own duration: each bucket is
// the largest total sampled in that slice of the run, in MiB.
type MemoryCurve [MemoryCurveBuckets]uint32

// PeakMB returns the largest bucket, in MiB.
func (c MemoryCurve) PeakMB() int {
	peak := uint32(0)
	for _, mb := range c {
		peak = max(peak, mb)
	}
	return int(peak)
}

// MarshalJSON encodes the curve as a JSON array of exactly MemoryCurveBuckets
// numbers.
func (c *MemoryCurve) MarshalJSON() ([]byte, error) {
	return json.Marshal([MemoryCurveBuckets]uint32(*c))
}

// UnmarshalJSON decodes a JSON array of exactly MemoryCurveBuckets numbers. Any
// other length is an error naming it: a short array padded with zeros would read
// as a run that used nothing for its last slices.
func (c *MemoryCurve) UnmarshalJSON(b []byte) error {
	var buckets []uint32
	if err := json.Unmarshal(b, &buckets); err != nil {
		return fmt.Errorf("decode memory curve: %w", err)
	}
	if len(buckets) != MemoryCurveBuckets {
		return fmt.Errorf("memory curve has %d buckets, want %d", len(buckets), MemoryCurveBuckets)
	}
	copy(c[:], buckets)
	return nil
}

// MemoryProfile is what a target's recorded runs of one shape say about its memory
// over time: Curve is the p90 of each bucket across Runs runs, and D10Ms and D90Ms
// are the p10 and p90 of their durations, the band the curve is stretched over.
// It carries no margin. Runs 0 means no run recorded a curve, so there is no
// profile and the other fields are zero.
type MemoryProfile struct {
	Curve MemoryCurve `json:"curve"`
	D10Ms int64       `json:"d10_ms"`
	D90Ms int64       `json:"d90_ms"`
	Runs  int         `json:"runs"`
}

// MemorySample is one reading of a process tree's resident memory, taken At after
// the exec started.
type MemorySample struct {
	At    time.Duration
	Bytes int64
}
