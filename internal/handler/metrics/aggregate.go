// Package metrics is the daemon's derived-dashboard presentation layer for magus's OTel
// metrics. It rolls raw in-process metricdata (histogram buckets and counters, read via
// observability.Collector) into the magus.metrics.v1 wire types the /dashboard consumes,
// maintains a rolling sample ring for backfill, and implements the Connect MetricsService.
// It is the only place the generated metrics proto meets the OTel SDK; observability itself
// stays proto-free.
package metrics

import (
	"math"
	"time"

	"github.com/egladman/magus/internal/quantile"
	metricsv1 "github.com/egladman/magus/proto/gen/go/magus/metrics/v1"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// OTel instrument names magus registers (see internal/observability/provider_otel.go). Kept
// as constants so the aggregation and the instrument registration read from one vocabulary.
const (
	instTargetDuration = "magus.target.duration"
	instCacheDuration  = "magus.cache.duration"
	instPoolWait       = "magus.pool.wait.duration"
	instGraphQuery     = "magus.graph.query.duration"
	instRemoteHits     = "magus.cache.remote.hits"
	instRemoteMisses   = "magus.cache.remote.misses"
	instRemoteErrors   = "magus.cache.remote.errors"
	instRemoteDuration = "magus.cache.remote.duration"
	instRemoteIOSize   = "magus.cache.remote.io.size"

	instCacheHits   = "magus.cache.hits"
	instCacheMisses = "magus.cache.misses"
	instTargetRuns  = "magus.target.runs"
)

// Aggregate rolls one metricdata.ResourceMetrics collection into a derived Snapshot: each
// latency histogram family becomes a Latency (count/sum plus interpolated p50/p95/p99/max),
// and the remote-cache instruments become a Remote. Missing instruments yield zero-valued
// (never nil) sub-messages, so the wire shape is stable from the first tick. at stamps
// captured_at.
func Aggregate(rm metricdata.ResourceMetrics, at time.Time) *metricsv1.Snapshot {
	snap := &metricsv1.Snapshot{
		CapturedAt: timestamppb.New(at),
		Target:     &metricsv1.Latency{},
		CacheOp:    &metricsv1.Latency{},
		PoolWait:   &metricsv1.Latency{},
		GraphQuery: &metricsv1.Latency{},
		Remote:     &metricsv1.Remote{},
	}

	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch m.Name {
			case instTargetDuration:
				snap.Target = latencyOf(m.Data)
			case instCacheDuration:
				snap.CacheOp = latencyOf(m.Data)
			case instPoolWait:
				snap.PoolWait = latencyOf(m.Data)
			case instGraphQuery:
				snap.GraphQuery = latencyOf(m.Data)
			case instRemoteHits:
				snap.Remote.Hits = counterOf(m.Data)
			case instRemoteMisses:
				snap.Remote.Misses = counterOf(m.Data)
			case instRemoteErrors:
				snap.Remote.Errors = counterOf(m.Data)
			case instRemoteDuration:
				lat := latencyOf(m.Data)
				snap.Remote.DurationP50 = lat.P50
				snap.Remote.DurationP95 = lat.P95
				snap.Remote.IoCount = lat.Count
			case instRemoteIOSize:
				snap.Remote.BytesTotal = int64(latencyOf(m.Data).Sum)
			}
		}
	}
	return snap
}

// counterTotals holds the cumulative counters the utilization sampler records per tick.
type counterTotals struct {
	cacheHits   int64
	cacheMisses int64
	targetRuns  int64
}

// counters reads the cumulative cache-hit, cache-miss, and target-run counters from a
// collection. Absent instruments read as zero.
func counters(rm metricdata.ResourceMetrics) counterTotals {
	var t counterTotals
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch m.Name {
			case instCacheHits:
				t.cacheHits = counterOf(m.Data)
			case instCacheMisses:
				t.cacheMisses = counterOf(m.Data)
			case instTargetRuns:
				t.targetRuns = counterOf(m.Data)
			}
		}
	}
	return t
}

// latencyOf rolls a histogram Aggregation into a Latency, summing across every data point
// (attribute set) in the family. A non-histogram or absent Aggregation yields a zero Latency.
func latencyOf(agg metricdata.Aggregation) *metricsv1.Latency {
	switch h := agg.(type) {
	case metricdata.Histogram[float64]:
		return foldHistogram(h.DataPoints)
	case metricdata.Histogram[int64]:
		return foldHistogram(h.DataPoints)
	default:
		return &metricsv1.Latency{}
	}
}

// counterOf totals a monotonic Sum Aggregation across its data points as an int64. A
// non-Sum or absent Aggregation reads as zero.
func counterOf(agg metricdata.Aggregation) int64 {
	switch s := agg.(type) {
	case metricdata.Sum[int64]:
		var total int64
		for _, dp := range s.DataPoints {
			total += dp.Value
		}
		return total
	case metricdata.Sum[float64]:
		var total float64
		for _, dp := range s.DataPoints {
			total += dp.Value
		}
		return int64(total)
	default:
		return 0
	}
}

// foldHistogram sums the count, sum, and bucket counts across a family's data points, then
// interpolates the percentiles from the combined buckets. Data points in one instrument
// share bucket boundaries (the SDK's default view), so bucket counts add elementwise.
func foldHistogram[N int64 | float64](dps []metricdata.HistogramDataPoint[N]) *metricsv1.Latency {
	lat := &metricsv1.Latency{}
	if len(dps) == 0 {
		return lat
	}

	var (
		count        uint64
		sum          float64
		bounds       []float64
		bucketCounts []uint64
		maxObserved  float64
		haveMax      bool
	)
	for _, dp := range dps {
		count += dp.Count
		sum += float64(dp.Sum)
		if bounds == nil {
			bounds = dp.Bounds
			bucketCounts = make([]uint64, len(dp.BucketCounts))
		}
		for i := range dp.BucketCounts {
			if i < len(bucketCounts) {
				bucketCounts[i] += dp.BucketCounts[i]
			}
		}
		if v, ok := dp.Max.Value(); ok {
			fv := float64(v)
			if !haveMax || fv > maxObserved {
				maxObserved = fv
				haveMax = true
			}
		}
	}

	lat.Count = int64(count)
	lat.Sum = sum
	if count == 0 {
		return lat
	}

	buckets := cumulativeBuckets(bounds, bucketCounts)
	lat.P50 = sanitize(quantile.Quantile(0.50, buckets))
	lat.P95 = sanitize(quantile.Quantile(0.95, buckets))
	lat.P99 = sanitize(quantile.Quantile(0.99, buckets))
	if haveMax {
		lat.Max = maxObserved
	} else {
		lat.Max = largestPopulatedBound(bounds, bucketCounts)
	}
	return lat
}

// cumulativeBuckets converts OTel explicit bounds plus per-bucket counts into the ascending
// cumulative buckets quantile.Quantile expects, appending the implied +Inf overflow bucket.
// bucketCounts has one more entry than bounds (the final +Inf bucket).
func cumulativeBuckets(bounds []float64, bucketCounts []uint64) []quantile.Bucket {
	buckets := make([]quantile.Bucket, 0, len(bucketCounts))
	var cum uint64
	for i, c := range bucketCounts {
		cum += c
		upper := math.Inf(+1)
		if i < len(bounds) {
			upper = bounds[i]
		}
		buckets = append(buckets, quantile.Bucket{UpperBound: upper, CumulativeCount: float64(cum)})
	}
	return buckets
}

// largestPopulatedBound returns the upper bound of the highest bucket that saw any
// observation, used as a max when the SDK did not record a Min/Max extremum. A populated
// +Inf overflow bucket has no finite bound, so it falls back to the largest finite bound.
func largestPopulatedBound(bounds []float64, bucketCounts []uint64) float64 {
	for i := len(bucketCounts) - 1; i >= 0; i-- {
		if bucketCounts[i] == 0 {
			continue
		}
		if i < len(bounds) {
			return bounds[i]
		}
		if len(bounds) > 0 {
			return bounds[len(bounds)-1]
		}
		return 0
	}
	return 0
}

// sanitize maps a NaN or infinite percentile (degenerate or empty histogram) to 0 so the
// wire carries a clean number.
func sanitize(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}
