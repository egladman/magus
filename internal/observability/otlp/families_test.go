package otlp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/egladman/magus/internal/observability"
)

// collectLocal builds a LocalCollect provider and returns a Collector over its in-process
// ManualReader, so a test can record through the real Provider methods and read the raw
// metricdata back without any network hop.
func collectLocal(t *testing.T) (observability.Provider, *Collector) {
	t.Helper()
	p, err := New(context.Background(), observability.Config{LocalCollect: true, ServiceName: "magus-test"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	coll, ok := CollectorFrom(p)
	require.True(t, ok, "expected a local collector")
	return p, coll
}

// sumInt64 returns the total of a monotonic or up/down Int64 sum named name, and whether the
// instrument was present at all.
func sumInt64(t *testing.T, rm metricdata.ResourceMetrics, name string) (int64, bool) {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			s, ok := m.Data.(metricdata.Sum[int64])
			require.True(t, ok, "%s is not an Int64 sum", name)
			var total int64
			for _, dp := range s.DataPoints {
				total += dp.Value
			}
			return total, true
		}
	}
	return 0, false
}

// histFloat64 returns the count and summed value of a float64 histogram named name, plus
// the attribute sets its data points carry, and whether the instrument was present at all.
func histFloat64(t *testing.T, rm metricdata.ResourceMetrics, name string) (count uint64, sum float64, attrs []attribute.Set, ok bool) {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			h, isHist := m.Data.(metricdata.Histogram[float64])
			require.True(t, isHist, "%s is not a float64 histogram", name)
			for _, dp := range h.DataPoints {
				count += dp.Count
				sum += dp.Sum
				attrs = append(attrs, dp.Attributes)
			}
			return count, sum, attrs, true
		}
	}
	return 0, 0, nil, false
}

// TestCacheSavedCollects covers magus.cache.saved.duration: the savings lens, which until it
// existed lived only in the end-of-run footer and never reached an exporter.
func TestCacheSavedCollects(t *testing.T) {
	p, coll := collectLocal(t)
	ctx := context.Background()

	p.RecordCacheSaved(ctx, 1.5)
	p.RecordCacheSaved(ctx, 0.5)

	rm, err := coll.Collect(ctx)
	require.NoError(t, err)

	count, sum, attrs, ok := histFloat64(t, rm, "magus.cache.saved.duration")
	require.True(t, ok, "magus.cache.saved.duration missing")
	assert.Equal(t, uint64(2), count)
	assert.InDelta(t, 2.0, sum, 1e-9, "the histogram sum IS the wall-clock the cache saved")
	// The cache family is a low-cardinality aggregate view; saved time carries no attribute
	// at all, so a workspace with thousands of targets adds no series here.
	require.Len(t, attrs, 1)
	assert.Equal(t, 0, attrs[0].Len(), "magus.cache.saved.duration must carry no attributes")
}

// TestAgentFamiliesCollect records one observation on each agent-surface instrument and reads
// back both the value and the attribute set, since a stray unbounded attribute is the failure
// mode these families are most exposed to.
func TestAgentFamiliesCollect(t *testing.T) {
	p, coll := collectLocal(t)
	ctx := context.Background()

	p.RecordDelegationRegistration(ctx, "diverged")
	p.RecordAttentionDisposition(ctx, 42, "warning")
	p.RecordReviewRemark(ctx, "agent")
	p.RecordReviewRemark(ctx, "human")
	p.RecordReviewPublish(ctx, "comment", true)

	rm, err := coll.Collect(ctx)
	require.NoError(t, err)

	regs, ok := sumInt64(t, rm, "magus.delegation.registrations")
	require.True(t, ok, "magus.delegation.registrations missing")
	assert.Equal(t, int64(1), regs)

	remarks, ok := sumInt64(t, rm, "magus.review.remarks")
	require.True(t, ok, "magus.review.remarks missing")
	assert.Equal(t, int64(2), remarks)

	publishes, ok := sumInt64(t, rm, "magus.review.publishes")
	require.True(t, ok, "magus.review.publishes missing")
	assert.Equal(t, int64(1), publishes)

	count, sum, attrs, ok := histFloat64(t, rm, "magus.attention.disposition.duration")
	require.True(t, ok, "magus.attention.disposition.duration missing")
	assert.Equal(t, uint64(1), count)
	assert.InDelta(t, 42.0, sum, 1e-9)
	require.Len(t, attrs, 1)
	sev, found := attrs[0].Value(attribute.Key("severity"))
	require.True(t, found, "the disposition histogram must be attributed by severity")
	assert.Equal(t, "warning", sev.AsString())
	assert.Equal(t, 1, attrs[0].Len(), "severity is the only attribute; a session id must never join it")
}

// TestPoolInstrumentsCollect exercises the magus.pool.slots.running gauge and the
// magus.pool.slots.queued gauge end to end through a real local provider.
func TestPoolInstrumentsCollect(t *testing.T) {
	p, coll := collectLocal(t)
	ctx := context.Background()

	// Two slots acquired, one released => running net 1.
	p.RecordPoolAcquire(ctx, 0.01, 2)
	p.RecordPoolRelease(ctx, 1)
	// Three callers begin waiting, one acquires => queued net 2.
	p.RecordPoolWaiting(ctx, 3)
	p.RecordPoolWaiting(ctx, -1)

	rm, err := coll.Collect(ctx)
	require.NoError(t, err)

	running, ok := sumInt64(t, rm, "magus.pool.slots.running")
	require.True(t, ok, "magus.pool.slots.running missing")
	assert.Equal(t, int64(1), running)

	queued, ok := sumInt64(t, rm, "magus.pool.slots.queued")
	require.True(t, ok, "magus.pool.slots.queued missing")
	assert.Equal(t, int64(2), queued)

	// The old spelling must be gone.
	_, present := sumInt64(t, rm, "magus.pool.slots.inflight")
	assert.False(t, present, "the retired magus.pool.slots.inflight must not be emitted")
}

// TestRemoteStoredOutcome confirms a "stored" put outcome now increments a counter (it
// previously fell through the hit/miss/error switch and vanished from the export).
func TestRemoteStoredOutcome(t *testing.T) {
	p, coll := collectLocal(t)
	ctx := context.Background()

	p.RecordRemoteOp(ctx, observability.RemoteOp{Method: "put", Outcome: "stored", Duration: 0.02, Bytes: 512})

	rm, err := coll.Collect(ctx)
	require.NoError(t, err)

	stores, ok := sumInt64(t, rm, "magus.cache.remote.stores")
	require.True(t, ok, "magus.cache.remote.stores missing")
	assert.Equal(t, int64(1), stores)
}

// TestBuzzFamiliesCollect records one observation across the new Buzz/MCP/Sandbox families and
// confirms the collection succeeds (no duplicate-instrument error) and carries them.
func TestBuzzFamiliesCollect(t *testing.T) {
	p, coll := collectLocal(t)
	ctx := context.Background()

	p.RecordMCPCall(ctx, observability.MCPCall{Tool: "graph", Outcome: "success", InputBytes: 100, OutputBytes: 200, Duration: 0.03})
	p.RecordSandboxRules(ctx, observability.SandboxRules{Read: 3, Write: 2, Exec: 1, EnvExact: 4, EnvGlob: 5, Scope: "target"})
	p.RecordSandboxCheck(ctx, "read", "allow", "//app")
	p.RecordBuzzHostCall(ctx, observability.BuzzHostCall{Callable: "proc.exec", Outcome: "success", Duration: 0.01})
	p.RecordBuzzSpellBuiltinsWarm(ctx, 0.05, "build")
	p.RecordBuzzJITRun(ctx)

	rm, err := coll.Collect(ctx)
	require.NoError(t, err)

	jit, ok := sumInt64(t, rm, "magus.buzz.jit.runs")
	require.True(t, ok, "magus.buzz.jit.runs missing")
	assert.Equal(t, int64(1), jit)

	// The spell-builtins warm counter must be registered under its own name, distinct from
	// the same-family warm-duration histogram (magus.buzz.spell.builtins.warm); a name clash
	// would drop it from the collection.
	builtins, ok := sumInt64(t, rm, "magus.buzz.spell.builtins.count")
	require.True(t, ok, "magus.buzz.spell.builtins.count missing")
	assert.Equal(t, int64(1), builtins)

	// Confirm the MCP calls counter recorded the call.
	calls, ok := sumInt64(t, rm, "magus.mcp.tool.calls")
	require.True(t, ok, "magus.mcp.tool.calls missing")
	assert.Equal(t, int64(1), calls)
}
