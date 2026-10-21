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

// HeldMB is what a run of this profile holds elapsed after it started, in MiB: the
// largest bucket over the fractions [elapsed/D90, elapsed/D10] of the run, wherever in
// its duration band the run really is. Once elapsed passes D90 it is the peak, held
// until the run is released. A profile with no curve holds 0.
func (p MemoryProfile) HeldMB(elapsed time.Duration) int {
	d10, d90 := time.Duration(p.D10Ms)*time.Millisecond, time.Duration(p.D90Ms)*time.Millisecond
	if d90 <= 0 || elapsed > d90 {
		return p.Curve.PeakMB()
	}
	elapsed = max(elapsed, 0)
	last := MemoryCurveBuckets - 1
	if d10 > 0 {
		last = min(int(int64(elapsed)*MemoryCurveBuckets/int64(d10)), last)
	}
	first := min(int(int64(elapsed)*MemoryCurveBuckets/int64(d90)), last)
	held := uint32(0)
	for _, mb := range p.Curve[first : last+1] {
		held = max(held, mb)
	}
	return int(held)
}

// MemorySample is one reading of a process tree's resident memory, taken At after
// the exec started.
type MemorySample struct {
	At    time.Duration
	Bytes int64
}

// foldCall folds the members of one ctx.needs call, each claiming more than 0 and
// named by names, into the call's claim. A lone member is the call. Several fold by
// curve when every one has a profile, and to the sum of their peaks when one does not.
func foldCall(members []ChainMemory, names []string) ChainMemory {
	if len(members) == 1 {
		return members[0]
	}
	sum, top := 0, members[0]
	for _, m := range members {
		sum += m.MB
		if m.MB > top.MB {
			top = m
		}
	}
	call := ChainMemory{MB: sum, DeclaredBy: top.DeclaredBy, Sizing: top.Sizing}
	for i, m := range members {
		if m.Profile.Runs > 0 {
			continue
		}
		call.Sizing.Fallback = m.Sizing.Fallback
		if call.Sizing.Fallback == "" {
			call.Sizing.Fallback = fmt.Sprintf("%s: no memory curve; summed the peaks of its ctx.needs call", names[i])
		}
		return call
	}
	call.Profile = composeCall(members)
	call.MB = call.Profile.Curve.PeakMB()
	return call
}

// composeCall sums the members' profiles from the call's start; each bucket is the
// largest sum at the breakpoints in it, where any HeldMB can change, so the peak is
// exact. A member ends at its D90, where admission charges a held run its peak, and
// is capped at its MB so no bucket passes the summed peaks.
func composeCall(members []ChainMemory) MemoryProfile {
	composed := MemoryProfile{Runs: members[0].Profile.Runs}
	for _, m := range members {
		composed.D10Ms = max(composed.D10Ms, m.Profile.D10Ms)
		composed.D90Ms = max(composed.D90Ms, m.Profile.D90Ms)
		composed.Runs = min(composed.Runs, m.Profile.Runs)
	}
	span := int64(time.Duration(composed.D90Ms) * time.Millisecond)
	for _, m := range members {
		for _, ms := range []int64{m.Profile.D10Ms, m.Profile.D90Ms} {
			d := int64(time.Duration(ms) * time.Millisecond)
			for k := range int64(MemoryCurveBuckets + 1) {
				t := time.Duration((k*d + MemoryCurveBuckets - 1) / MemoryCurveBuckets)
				bucket := MemoryCurveBuckets - 1
				if span > 0 {
					bucket = min(int(int64(t)*MemoryCurveBuckets/span), bucket)
				}
				composed.Curve[bucket] = max(composed.Curve[bucket], uint32(summedHeldMB(members, t)))
			}
		}
	}
	return composed
}

// summedHeldMB is what the members hold together elapsed after their call started.
func summedHeldMB(members []ChainMemory, elapsed time.Duration) int {
	held := 0
	for _, m := range members {
		if elapsed <= time.Duration(m.Profile.D90Ms)*time.Millisecond {
			held += min(m.Profile.HeldMB(elapsed), m.MB)
		}
	}
	return held
}
