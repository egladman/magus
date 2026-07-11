package metrics

import (
	"testing"
	"time"

	metricsv1 "github.com/egladman/magus/proto/gen/go/magus/metrics/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// kvSet builds an attribute.Set from alternating key/value strings.
func kvSet(pairs ...string) attribute.Set {
	kv := make([]attribute.KeyValue, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		kv = append(kv, attribute.String(pairs[i], pairs[i+1]))
	}
	return attribute.NewSet(kv...)
}

// fHistDP builds one attributed float64 histogram data point.
func fHistDP(bounds []float64, counts []uint64, sum float64, attrs attribute.Set) metricdata.HistogramDataPoint[float64] {
	return metricdata.HistogramDataPoint[float64]{Attributes: attrs, Count: total(counts), Bounds: bounds, BucketCounts: counts, Sum: sum}
}

// iHistDP builds one attributed int64 histogram data point.
func iHistDP(bounds []float64, counts []uint64, sum int64, attrs attribute.Set) metricdata.HistogramDataPoint[int64] {
	return metricdata.HistogramDataPoint[int64]{Attributes: attrs, Count: total(counts), Bounds: bounds, BucketCounts: counts, Sum: sum}
}

// fHistMetric wraps float64 histogram data points as a named metric.
func fHistMetric(name string, dps ...metricdata.HistogramDataPoint[float64]) metricdata.Metrics {
	return metricdata.Metrics{Name: name, Data: metricdata.Histogram[float64]{Temporality: metricdata.CumulativeTemporality, DataPoints: dps}}
}

// iHistMetric wraps int64 histogram data points as a named metric.
func iHistMetric(name string, dps ...metricdata.HistogramDataPoint[int64]) metricdata.Metrics {
	return metricdata.Metrics{Name: name, Data: metricdata.Histogram[int64]{Temporality: metricdata.CumulativeTemporality, DataPoints: dps}}
}

// sumMetric wraps attributed int64 sum data points as a named monotonic sum metric.
func sumMetric(name string, dps ...metricdata.DataPoint[int64]) metricdata.Metrics {
	return metricdata.Metrics{Name: name, Data: metricdata.Sum[int64]{Temporality: metricdata.CumulativeTemporality, IsMonotonic: true, DataPoints: dps}}
}

// idp builds one attributed int64 sum data point.
func idp(val int64, attrs attribute.Set) metricdata.DataPoint[int64] {
	return metricdata.DataPoint[int64]{Attributes: attrs, Value: val}
}

func scope(ms ...metricdata.Metrics) metricdata.ResourceMetrics {
	return metricdata.ResourceMetrics{ScopeMetrics: []metricdata.ScopeMetrics{{Metrics: ms}}}
}

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
	require.True(t, proto.Equal(&metricsv1.Latency{}, got.Cache), "cache should be zero")
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

// TestTargetStats groups the magus.target.duration histogram's attributed data points into one
// TargetStat per (project, spell, target), rolling up counts, cache hit-rate, and the
// success/error split, ordered deterministically.
func TestTargetStats(t *testing.T) {
	b := []float64{1, 2} // all observations land in (1,2] => p50 1.5, p95 1.95, p99 1.99
	rm := scope(fHistMetric(instTargetDuration,
		fHistDP(b, []uint64{0, 4, 0}, 6, kvSet("magus.project", "//a", "magus.spell", "build", "magus.target", "build", "outcome", "success", "cache.hit", "false")),
		fHistDP(b, []uint64{0, 4, 0}, 6, kvSet("magus.project", "//a", "magus.spell", "build", "magus.target", "build", "outcome", "success", "cache.hit", "true")),
		fHistDP(b, []uint64{0, 2, 0}, 3, kvSet("magus.project", "//a", "magus.spell", "build", "magus.target", "build", "outcome", "error", "cache.hit", "false")),
		fHistDP(b, []uint64{0, 1, 0}, 1.5, kvSet("magus.project", "//b", "magus.spell", "test", "magus.target", "test", "outcome", "success", "cache.hit", "false")),
	))

	rows := targetStats(rm)
	require.Len(t, rows, 2)

	// //a sorts before //b.
	a := rows[0]
	assert.Equal(t, "//a", a.Project)
	assert.Equal(t, "build", a.Target)
	assert.Equal(t, "build", a.Spell)
	assert.Equal(t, int64(10), a.Count)
	assert.Equal(t, int64(8), a.Success)
	assert.Equal(t, int64(2), a.Errors)
	assert.InDelta(t, 0.4, a.CacheHitRate, 1e-9) // 4 of 10 runs were cache hits
	assert.InDelta(t, 1.5, a.P50, 1e-9)
	assert.InDelta(t, 1.95, a.P95, 1e-9)
	assert.InDelta(t, 1.99, a.P99, 1e-9)

	bb := rows[1]
	assert.Equal(t, "//b", bb.Project)
	assert.Equal(t, int64(1), bb.Count)
	assert.Equal(t, int64(1), bb.Success)
	assert.Equal(t, int64(0), bb.Errors)
	assert.InDelta(t, 0.0, bb.CacheHitRate, 1e-9)
}

// TestMCPToolStats rolls the magus.mcp.tool.* instruments into one MCPToolStat per tool.
func TestMCPToolStats(t *testing.T) {
	rm := scope(
		sumMetric(instMCPCalls,
			idp(3, kvSet("tool", "graph", "outcome", "success")),
			idp(1, kvSet("tool", "graph", "outcome", "error")),
		),
		iHistMetric(instMCPInput, iHistDP([]float64{1000}, []uint64{4, 0}, 400, kvSet("tool", "graph"))),
		iHistMetric(instMCPOutput, iHistDP([]float64{2000}, []uint64{4, 0}, 800, kvSet("tool", "graph"))),
		fHistMetric(instMCPDuration, fHistDP([]float64{1}, []uint64{4, 0}, 0.4, kvSet("tool", "graph", "outcome", "success"))),
	)

	rows := mcpToolStats(rm)
	require.Len(t, rows, 1)
	r := rows[0]
	assert.Equal(t, "graph", r.Tool)
	assert.Equal(t, int64(4), r.Calls)
	assert.Equal(t, int64(1), r.Errors)
	assert.Equal(t, int64(400), r.InputTotal)
	assert.Equal(t, int64(800), r.OutputTotal)
	assert.InDelta(t, 500.0, r.InputP50, 1e-9) // single (0,1000] bucket => 1000*q
	assert.InDelta(t, 950.0, r.InputP95, 1e-9)
	assert.InDelta(t, 1000.0, r.OutputP50, 1e-9)
	assert.InDelta(t, 0.5, r.DurationP50, 1e-9)
	assert.InDelta(t, 0.95, r.DurationP95, 1e-9)
}

// TestBuzzStats rolls the magus.buzz.* families into a single Buzz message.
func TestBuzzStats(t *testing.T) {
	rm := scope(
		fHistMetric(instBuzzExec, fHistDP([]float64{1}, []uint64{2, 0}, 0.6, kvSet("mode", "run", "outcome", "success"))),
		fHistMetric(instBuzzCompile, fHistDP([]float64{1}, []uint64{3, 0}, 0.3, kvSet("phase", "parse", "mode", "run"))),
		sumMetric(instBuzzHostCallCount, idp(5, kvSet("callable", "os.exec", "outcome", "success"))),
		fHistMetric(instBuzzHostCallDur, fHistDP([]float64{1}, []uint64{5, 0}, 0.5, kvSet("callable", "os.exec"))),
		sumMetric(instBuzzSessionReuse, idp(7, kvSet("outcome", "reused"))),
		sumMetric(instBuzzSessionIdle, idp(2, kvSet())),
		sumMetric(instBuzzSessionEvict, idp(1, kvSet("source", "ttl"))),
		sumMetric(instBuzzJITRuns, idp(9, kvSet())),
		sumMetric(instBuzzVMFaults, idp(4, kvSet("kind", "panic"))),
	)

	b := buzzStats(rm)
	assert.Equal(t, int64(2), b.ExecCount)
	assert.Equal(t, int64(3), b.CompileCount)
	assert.Equal(t, int64(5), b.HostCallCount)
	assert.Equal(t, int64(7), b.SessionPoolReuse)
	assert.Equal(t, int64(2), b.SessionPoolIdle)
	assert.Equal(t, int64(1), b.SessionPoolEvictions)
	assert.Equal(t, int64(9), b.JitRuns)
	assert.Equal(t, int64(4), b.VmFaults)
	assert.InDelta(t, 0.5, b.ExecP50, 1e-9)
}

// TestSandboxStats rolls the magus.sandbox.* families into a single Sandbox message, splitting
// the rule counter by access and the check counter by decision.
func TestSandboxStats(t *testing.T) {
	rm := scope(
		fHistMetric(instSandboxApply, fHistDP([]float64{1}, []uint64{2, 0}, 0.2, kvSet("outcome", "success", "scope", "target"))),
		sumMetric(instSandboxRules,
			idp(3, kvSet("access", "read", "scope", "target")),
			idp(2, kvSet("access", "write", "scope", "target")),
			idp(1, kvSet("access", "exec", "scope", "target")),
		),
		sumMetric(instSandboxEnvRules,
			idp(4, kvSet("kind", "exact")),
			idp(5, kvSet("kind", "glob")),
		),
		sumMetric(instSandboxChecks,
			idp(8, kvSet("access", "read", "decision", "allow", "magus.project", "//app")),
			idp(2, kvSet("access", "write", "decision", "deny", "magus.project", "//app")),
		),
		sumMetric(instSandboxEnvDropped, idp(6, kvSet("magus.project", "//app"))),
	)

	got := sandboxStats(rm)
	want := &metricsv1.Sandbox{
		RulesRead:   3,
		RulesWrite:  2,
		RulesExec:   1,
		EnvRules:    9,
		ChecksAllow: 8,
		ChecksDeny:  2,
		EnvDropped:  6,
	}
	// Copy the interpolated apply percentiles into want so the rest is an exact whole-message
	// comparison.
	want.ApplyP50 = got.ApplyP50
	want.ApplyP95 = got.ApplyP95
	require.True(t, proto.Equal(want, got), "sandbox rollup mismatch: got %+v", got)
	assert.InDelta(t, 0.5, got.ApplyP50, 1e-9)
}

// TestAggregateEmpty confirms an empty collection yields a fully zero-valued (non-nil)
// Snapshot, so the wire shape is stable before any metric is recorded.
func TestAggregateEmpty(t *testing.T) {
	at := time.Unix(1_700_000_000, 0).UTC()
	got := Aggregate(metricdata.ResourceMetrics{}, at)
	want := &metricsv1.Snapshot{
		CapturedAt: timestamppb.New(at),
		Target:     &metricsv1.Latency{},
		Cache:      &metricsv1.Latency{},
		PoolWait:   &metricsv1.Latency{},
		GraphQuery: &metricsv1.Latency{},
		Remote:     &metricsv1.Remote{},
		Buzz:       &metricsv1.Buzz{},
		Sandbox:    &metricsv1.Sandbox{},
	}
	require.True(t, proto.Equal(want, got), "empty aggregate should be a fully zero Snapshot")
}
