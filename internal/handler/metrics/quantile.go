// Package quantile estimates a quantile from a classic (explicit-bucket) histogram
// by linear interpolation within the matched bucket.
//
// The algorithm is the histogram_quantile / bucketQuantile routine from Prometheus,
// reduced to the single interpolation core magus needs to turn an OTel histogram's
// cumulative buckets into a latency percentile. Provenance and license:
//
//	Copyright The Prometheus Authors
//	Licensed under the Apache License, Version 2.0 (the "License");
//	you may not use this file except in compliance with the License.
//	You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
//	Unless required by applicable law or agreed to in writing, software
//	distributed under the License is distributed on an "AS IS" BASIS,
//	WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//	See the License for the specific language governing permissions and
//	limitations under the License.
//
// Source: https://github.com/prometheus/prometheus/blob/main/promql/quantile.go
package quantile

import (
	"math"
	"sort"
)

// Bucket is one cumulative histogram bucket: CumulativeCount observations fell at or
// below UpperBound. Buckets are cumulative and ascending by UpperBound; the last
// bucket's UpperBound is conventionally +Inf (math.Inf(1)), which an OTel explicit-bucket
// histogram always carries as its implied final bucket.
type Bucket struct {
	UpperBound      float64
	CumulativeCount float64
}

// Quantile estimates the q-quantile (q in [0,1]) of the distribution described by
// buckets. It sorts a copy of buckets (the caller's slice is never mutated), then
// linearly interpolates within the bucket that contains the q*N-th observation.
//
// It follows Prometheus bucketQuantile's edge handling: q outside [0,1] yields an
// infinity, a q of NaN yields NaN, and a degenerate histogram (empty, missing the
// implied +Inf bucket, fewer than two buckets, or zero observations) yields NaN. A
// rank that falls in the final +Inf bucket clamps to the largest finite upper bound,
// since that overflow bucket has no finite boundary to interpolate toward.
func Quantile(q float64, buckets []Bucket) float64 {
	switch {
	case math.IsNaN(q):
		return math.NaN()
	case q < 0:
		return math.Inf(-1)
	case q > 1:
		return math.Inf(+1)
	}
	if len(buckets) == 0 {
		return math.NaN()
	}

	// Sort a copy so callers keep their slice order (and any shared backing array).
	bs := make([]Bucket, len(buckets))
	copy(bs, buckets)
	sort.Slice(bs, func(i, j int) bool { return bs[i].UpperBound < bs[j].UpperBound })

	// The classic algorithm requires the final bucket to be the +Inf overflow bucket;
	// without it the total observation count is unknown and the estimate is undefined.
	if !math.IsInf(bs[len(bs)-1].UpperBound, +1) {
		return math.NaN()
	}
	if len(bs) < 2 {
		return math.NaN()
	}
	observations := bs[len(bs)-1].CumulativeCount
	if observations == 0 {
		return math.NaN()
	}

	rank := q * observations
	// Search only the finite buckets (all but the +Inf overflow) for the first whose
	// cumulative count reaches the rank.
	b := sort.Search(len(bs)-1, func(i int) bool { return bs[i].CumulativeCount >= rank })

	switch {
	case b == len(bs)-1:
		// Rank spills into the +Inf bucket: clamp to the largest finite upper bound.
		return bs[len(bs)-2].UpperBound
	case b == 0 && bs[0].UpperBound <= 0:
		return bs[0].UpperBound
	default:
		bucketStart := 0.0
		bucketEnd := bs[b].UpperBound
		count := bs[b].CumulativeCount
		if b > 0 {
			bucketStart = bs[b-1].UpperBound
			count -= bs[b-1].CumulativeCount
			rank -= bs[b-1].CumulativeCount
		}
		return bucketStart + (bucketEnd-bucketStart)*(rank/count)
	}
}
