package metrics

import (
	"testing"
	"time"

	metricsv1 "github.com/egladman/magus/proto/gen/go/magus/metrics/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// floatHist builds a float64 histogram metric with a single data point.
func floatHist(name string, bounds []float64, counts []uint64, sum float64) metricdata.Metrics {
	return metricdata.Metrics{
		Name: name,
		Data: metricdata.Histogram[float64]{
			Temporality: metricdata.CumulativeTemporality,
			DataPoints: []metricdata.HistogramDataPoint[float64]{{
				Count:        total(counts),
				Bounds:       bounds,
				BucketCounts: counts,
				Sum:          sum,
			}},
		},
	}
}

// intHist builds an int64 histogram metric (e.g. the remote io.size byte histogram).
func intHist(name string, bounds []float64, counts []uint64, sum int64) metricdata.Metrics {
	return metricdata.Metrics{
		Name: name,
		Data: metricdata.Histogram[int64]{
			Temporality: metricdata.CumulativeTemporality,
			DataPoints: []metricdata.HistogramDataPoint[int64]{{
				Count:        total(counts),
				Bounds:       bounds,
				BucketCounts: counts,
				Sum:          sum,
			}},
		},
	}
}

// intCounter builds a monotonic int64 sum metric.
func intCounter(name string, value int64) metricdata.Metrics {
	return metricdata.Metrics{
		Name: name,
		Data: metricdata.Sum[int64]{
			Temporality: metricdata.CumulativeTemporality,
			IsMonotonic: true,
			DataPoints:  []metricdata.DataPoint[int64]{{Value: value}},
		},
	}
}

func total(counts []uint64) uint64 {
	var n uint64
	for _, c := range counts {
		n += c
	}
	return n
}

func TestAggregate(t *testing.T) {
	at := time.Unix(1_700_000_000, 0).UTC()

	rm := metricdata.ResourceMetrics{
		ScopeMetrics: []metricdata.ScopeMetrics{{
			Metrics: []metricdata.Metrics{
				// bounds [1,2], counts [0,10,0]: all 10 observations in (1,2]; sum 15.
				floatHist(instTargetDuration, []float64{1, 2}, []uint64{0, 10, 0}, 15),
				// bounds [0.1], counts [3,0]: 3 observations in (0,0.1]; sum 0.3.
				floatHist(instRemoteDuration, []float64{0.1}, []uint64{3, 0}, 0.3),
				intHist(instRemoteIOSize, []float64{1024}, []uint64{2, 0}, 4096),
				intCounter(instRemoteHits, 2),
				intCounter(instRemoteMisses, 1),
				intCounter(instRemoteErrors, 0),
				// Sample-only counters must not leak into the derived Snapshot.
				intCounter(instCacheHits, 7),
				intCounter(instTargetRuns, 10),
			},
		}},
	}

	got := Aggregate(rm, at)

	// captured_at and the untouched latency families are exact whole-message comparisons.
	require.True(t, proto.Equal(timestamppb.New(at), got.CapturedAt), "captured_at")
	require.True(t, proto.Equal(&metricsv1.Latency{}, got.CacheOp), "cache_op should be zero")
	require.True(t, proto.Equal(&metricsv1.Latency{}, got.PoolWait), "pool_wait should be zero")
	require.True(t, proto.Equal(&metricsv1.Latency{}, got.GraphQuery), "graph_query should be zero")

	// Target: exact count/sum/max, interpolated percentiles within a tight delta.
	require.Equal(t, int64(10), got.Target.Count)
	assert.InDelta(t, 15.0, got.Target.Sum, 1e-9)
	assert.InDelta(t, 1.5, got.Target.P50, 1e-9)
	assert.InDelta(t, 1.95, got.Target.P95, 1e-9)
	assert.InDelta(t, 1.99, got.Target.P99, 1e-9)
	assert.InDelta(t, 2.0, got.Target.Max, 1e-9)

	// Remote: exact tallies and byte total, interpolated durations within a tight delta.
	require.Equal(t, int64(2), got.Remote.Hits)
	require.Equal(t, int64(1), got.Remote.Misses)
	require.Equal(t, int64(0), got.Remote.Errors)
	require.Equal(t, int64(3), got.Remote.IoCount)
	require.Equal(t, int64(4096), got.Remote.BytesTotal)
	assert.InDelta(t, 0.05, got.Remote.DurationP50, 1e-9)
	assert.InDelta(t, 0.095, got.Remote.DurationP95, 1e-9)
}

// TestAggregateEmpty confirms an empty collection yields a fully zero-valued (non-nil)
// Snapshot, so the wire shape is stable before any metric is recorded.
func TestAggregateEmpty(t *testing.T) {
	at := time.Unix(1_700_000_000, 0).UTC()
	got := Aggregate(metricdata.ResourceMetrics{}, at)
	want := &metricsv1.Snapshot{
		CapturedAt: timestamppb.New(at),
		Target:     &metricsv1.Latency{},
		CacheOp:    &metricsv1.Latency{},
		PoolWait:   &metricsv1.Latency{},
		GraphQuery: &metricsv1.Latency{},
		Remote:     &metricsv1.Remote{},
	}
	require.True(t, proto.Equal(want, got), "empty aggregate should be a fully zero Snapshot")
}
