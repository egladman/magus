package budget

import (
	"fmt"
	"time"

	"github.com/egladman/magus/libs/codec"
)

// CurveBuckets is how many equal slices of a run's duration a [Curve] holds, 5% each.
// Buckets are fractions of the run, not seconds, so a slow run and a fast one of the
// same work line up phase by phase.
const CurveBuckets = 20

// Curve is a run's resident memory over its own duration: each bucket is the largest
// total sampled in that slice of the run, in MiB.
type Curve [CurveBuckets]uint32

// PeakMB returns the largest bucket, in MiB.
func (c Curve) PeakMB() int {
	peak := uint32(0)
	for _, mb := range c {
		peak = max(peak, mb)
	}
	return int(peak)
}

// MarshalJSON encodes the curve as a JSON array of exactly CurveBuckets numbers.
func (c *Curve) MarshalJSON() ([]byte, error) {
	return codec.Marshal([CurveBuckets]uint32(*c))
}

// UnmarshalJSON decodes a JSON array of exactly CurveBuckets numbers. Any other length is
// a [*CurveLengthError]: a short array padded with zeros would read as a run that used
// nothing for its last slices.
func (c *Curve) UnmarshalJSON(b []byte) error {
	var buckets []uint32
	if err := codec.Unmarshal(b, &buckets); err != nil {
		return fmt.Errorf("budget: decode memory curve: %w", err)
	}
	if len(buckets) != CurveBuckets {
		return &CurveLengthError{Buckets: len(buckets)}
	}
	copy(c[:], buckets)
	return nil
}

// CurveLengthError is a decoded curve whose bucket count is not [CurveBuckets].
type CurveLengthError struct {
	Buckets int
}

func (e *CurveLengthError) Error() string {
	return fmt.Sprintf("budget: memory curve has %d buckets, want %d", e.Buckets, CurveBuckets)
}

// Profile is what a target's recorded runs of one shape say about its memory over time:
// Curve is the p90 of each bucket across Runs runs, and D10Ms and D90Ms are the p10 and
// p90 of their durations, the band the curve is stretched over. It carries no margin.
// Runs 0 means no run recorded a curve, so there is no profile and the other fields are
// zero.
type Profile struct {
	Curve Curve `json:"curve"`
	D10Ms int64 `json:"d10_ms"`
	D90Ms int64 `json:"d90_ms"`
	Runs  int   `json:"runs"`
}

// HeldMB is what a run of this profile holds elapsed after it started, in MiB: the
// largest bucket over the fractions [elapsed/D90, elapsed/D10] of the run, wherever in
// its duration band the run really is. Once elapsed passes D90 it is the peak, held
// until the run is released. A profile with no curve holds 0.
func (p Profile) HeldMB(elapsed time.Duration) int {
	d10, d90 := time.Duration(p.D10Ms)*time.Millisecond, time.Duration(p.D90Ms)*time.Millisecond
	if d90 <= 0 || elapsed > d90 {
		return p.Curve.PeakMB()
	}
	elapsed = max(elapsed, 0)
	last := CurveBuckets - 1
	if d10 > 0 {
		last = min(int(int64(elapsed)*CurveBuckets/int64(d10)), last)
	}
	first := min(int(int64(elapsed)*CurveBuckets/int64(d90)), last)
	held := uint32(0)
	for _, mb := range p.Curve[first : last+1] {
		held = max(held, mb)
	}
	return int(held)
}

// Sizing says where a memory claim's figure came from. The zero value is a figure read
// straight from a declaration.
type Sizing struct {
	// Samples is how many successful measured runs the figure was sized from; 0 when
	// it is the declaration itself.
	Samples int `json:"samples,omitzero" yaml:"samples,omitempty"`
	// AnyShape is set when runs with this invocation's charms and forwarded args were
	// too few, so the figure was sized from every run of the target.
	AnyShape bool `json:"any_shape,omitzero" yaml:"any_shape,omitempty"`
	// Slots is where the same claim's slot count came from. It rides here because this
	// is the one provenance record a claim carries from sizing to a refusal.
	Slots SlotSizing `json:"slots,omitzero" yaml:"slots,omitempty"`
	// Fallback is why the claim is a peak held flat rather than a measured curve, empty
	// when nothing fell back.
	Fallback string `json:"fallback,omitempty" yaml:"fallback,omitempty"`
}

// SlotSource is where a claim's slot count came from.
type SlotSource string

const (
	// SlotsUnmeasured is one slot: the target declares none and too few runs measured it.
	SlotsUnmeasured SlotSource = "unmeasured"
	// SlotsDeclared is the target's own slots policy.
	SlotsDeclared SlotSource = "declared"
	// SlotsMeasured is the width the target's runs measured.
	SlotsMeasured SlotSource = "measured"
	// SlotsForMemory is what the memory claim is worth on this host, which is more than
	// the declared or measured slots.
	SlotsForMemory SlotSource = "memory"
)

// SlotSizing says where a claim's slot count came from. The zero value records nothing,
// as on a claim from a client that did not report it.
type SlotSizing struct {
	Source SlotSource `json:"source,omitempty" yaml:"source,omitempty"`
	// Samples and AnyShape say what a measured count was sized from, as on Sizing.
	Samples  int  `json:"samples,omitzero" yaml:"samples,omitempty"`
	AnyShape bool `json:"any_shape,omitzero" yaml:"any_shape,omitempty"`
}

// Measured reports whether the count came from recorded runs.
func (s SlotSizing) Measured() bool { return s.Source == SlotsMeasured }

// String renders the provenance for a person, or "" when none was recorded.
func (s SlotSizing) String() string {
	switch {
	case s.Source == SlotsForMemory:
		return "for its memory"
	case !s.Measured():
		return string(s.Source)
	case s.AnyShape:
		return fmt.Sprintf("measured over %d runs of any shape", s.Samples)
	default:
		return fmt.Sprintf("measured over %d runs", s.Samples)
	}
}

// Measured reports whether the figure came from recorded peaks rather than a declaration.
func (s Sizing) Measured() bool { return s.Samples > 0 }

// String renders the provenance for a person: "declared", or how many runs it was
// measured over, followed by the reason it fell back to a peak when it did.
func (s Sizing) String() string {
	var from string
	switch {
	case !s.Measured():
		from = "declared"
	case s.AnyShape:
		from = fmt.Sprintf("measured over %d runs of any shape", s.Samples)
	default:
		from = fmt.Sprintf("measured over %d runs", s.Samples)
	}
	if s.Fallback == "" {
		return from
	}
	return fmt.Sprintf("%s; %s", from, s.Fallback)
}

// Breakdown renders one line of a claim's memory: the target, its figure and where the
// figure came from. under names the composer an inline member runs beneath, "" for a
// target that claims for itself.
func (s Sizing) Breakdown(target, under string, mb int) string {
	head := fmt.Sprintf("%s %s", target, FormatMB(mb))
	if under != "" {
		head += " under " + under
	}
	var from string
	switch {
	case !s.Measured():
		from = "declared"
	case s.AnyShape:
		from = fmt.Sprintf("own peak over %d runs of any shape", s.Samples)
	default:
		from = fmt.Sprintf("own peak over %d runs", s.Samples)
	}
	if s.Fallback != "" {
		from += "; " + s.Fallback
	}
	return fmt.Sprintf("%s: %s", head, from)
}

// FormatMB renders a memory figure in base-1024 units with a space, so a refusal and a
// status report say the same figure the same way.
func FormatMB(mb int) string {
	if mb >= 1024 {
		return fmt.Sprintf("%.1f GiB", float64(mb)/1024)
	}
	return fmt.Sprintf("%d MiB", mb)
}
