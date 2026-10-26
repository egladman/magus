package otlp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/sandbox/filesystem"
	"github.com/egladman/magus/types"
)

// collectLocal builds a LocalCollect provider and returns a Collector over its in-process
// ManualReader, so a test can record through the real Provider methods and read the raw
// metricdata back without any network hop.
func collectLocal(t *testing.T) (observability.Provider, *Collector) {
	t.Helper()
	p, err := New(context.Background(), observability.Config{LocalCollect: true, ServiceName: "magus-test"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	coll, ok := ManualCollector(p)
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

// TestAgentFamiliesCollect records one observation on each lease, attention and review instrument and reads
// back both the value and the attribute set, since a stray unbounded attribute is the failure
// mode these families are most exposed to.
func TestAgentFamiliesCollect(t *testing.T) {
	p, coll := collectLocal(t)
	ctx := context.Background()

	p.RecordLeaseRegistration(ctx, "diverged")
	p.RecordAttentionDisposition(ctx, 42, "warning")
	p.RecordReviewRemark(ctx, "agent")
	p.RecordReviewRemark(ctx, "unattributed")
	p.RecordReviewPublish(ctx, "comment", true)

	rm, err := coll.Collect(ctx)
	require.NoError(t, err)

	regs, ok := sumInt64(t, rm, "magus.lease.registrations")
	require.True(t, ok, "magus.lease.registrations missing")
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

// TestCacheReadsCollect pins magus.cache.reads: one counter whose result attribute says how
// each Cache.Run ended, so hits, misses and errors sum to the runs.
func TestCacheReadsCollect(t *testing.T) {
	p, coll := collectLocal(t)
	ctx := context.Background()

	p.RecordCacheRead(ctx, observability.CacheHit)
	p.RecordCacheRead(ctx, observability.CacheHit)
	p.RecordCacheRead(ctx, observability.CacheMiss)
	p.RecordCacheRead(ctx, observability.CacheError)
	p.RecordCacheDuration(ctx, 0.5, observability.CacheMiss)

	rm, err := coll.Collect(ctx)
	require.NoError(t, err)

	assert.Equal(t, map[attribute.Set]int64{
		attribute.NewSet(attribute.String("result", "hit")):   2,
		attribute.NewSet(attribute.String("result", "miss")):  1,
		attribute.NewSet(attribute.String("result", "error")): 1,
	}, sumAttrs(t, rm, "magus.cache.reads"))
	_, _, attrs, ok := histFloat64(t, rm, "magus.cache.duration")
	require.True(t, ok, "magus.cache.duration missing")
	assert.Equal(t, []attribute.Set{attribute.NewSet(attribute.String("result", "miss"))}, attrs)
	for _, gone := range []string{"magus.cache.hits", "magus.cache.misses", "magus.cache.errors"} {
		_, present := sumInt64(t, rm, gone)
		assert.False(t, present, "%s folded into magus.cache.reads", gone)
	}
}

// TestReviewMergedWhileReadingCollects reads back the duration of reviews that merged under a
// reader, and that it carries no attribute: a review id or repository name is unbounded, and
// how long the reader had been at it is the whole fact. The histogram's count is the merges.
func TestReviewMergedWhileReadingCollects(t *testing.T) {
	p, coll := collectLocal(t)
	ctx := context.Background()

	p.RecordReviewMergedWhileReading(ctx, 90)

	rm, err := coll.Collect(ctx)
	require.NoError(t, err)

	count, sum, attrs, ok := histFloat64(t, rm, "magus.review.merged_while_reading.duration")
	require.True(t, ok, "magus.review.merged_while_reading.duration missing")
	assert.Equal(t, uint64(1), count)
	assert.InDelta(t, 90.0, sum, 1e-9)
	require.Len(t, attrs, 1)
	assert.Equal(t, 0, attrs[0].Len())
	_, present := sumInt64(t, rm, "magus.review.merged_while_reading")
	assert.False(t, present, "the counter restated the histogram's count")
}

// TestPoolInstrumentsCollect exercises the magus.pool.slots.running gauge and the
// magus.pool.slots.queued gauge end to end through a real local provider.
func TestPoolInstrumentsCollect(t *testing.T) {
	p, coll := collectLocal(t)
	ctx := context.Background()

	// Two slots acquired, one released => running net 1.
	p.RecordPoolWait(ctx, 0.01)
	p.RecordPoolSlots(ctx, observability.PoolRunning, 2)
	p.RecordPoolSlots(ctx, observability.PoolRunning, -1)
	// Three callers begin waiting, one acquires => queued net 2.
	p.RecordPoolSlots(ctx, observability.PoolQueued, 3)
	p.RecordPoolSlots(ctx, observability.PoolQueued, -1)

	rm, err := coll.Collect(ctx)
	require.NoError(t, err)

	running, ok := sumInt64(t, rm, "magus.pool.slots.running")
	require.True(t, ok, "magus.pool.slots.running missing")
	assert.Equal(t, int64(1), running)

	queued, ok := sumInt64(t, rm, "magus.pool.slots.queued")
	require.True(t, ok, "magus.pool.slots.queued missing")
	assert.Equal(t, int64(2), queued)

	count, _, _, ok := histFloat64(t, rm, "magus.pool.wait.duration")
	require.True(t, ok, "magus.pool.wait.duration missing")
	assert.Equal(t, uint64(1), count)

	// The old spelling must be gone.
	_, present := sumInt64(t, rm, "magus.pool.slots.inflight")
	assert.False(t, present, "the retired magus.pool.slots.inflight must not be emitted")
}

// TestRemoteOpsCountEveryOutcome pins magus.cache.remote.ops: every outcome a backend answers
// is counted under its method, "exists" included, which the per-outcome counters dropped.
func TestRemoteOpsCountEveryOutcome(t *testing.T) {
	p, coll := collectLocal(t)
	ctx := context.Background()

	p.RecordRemoteOp(ctx, observability.RemoteOp{Method: "get", Outcome: observability.RemoteHit, Duration: 0.01, Bytes: 64})
	p.RecordRemoteOp(ctx, observability.RemoteOp{Method: "put", Outcome: observability.RemoteStored, Duration: 0.02, Bytes: 512})
	p.RecordRemoteOp(ctx, observability.RemoteOp{Method: "put", Outcome: observability.RemoteExists, Duration: 0.02})

	rm, err := coll.Collect(ctx)
	require.NoError(t, err)

	assert.Equal(t, map[attribute.Set]int64{
		attribute.NewSet(attribute.String("method", "get"), attribute.String("outcome", "hit")):    1,
		attribute.NewSet(attribute.String("method", "put"), attribute.String("outcome", "stored")): 1,
		attribute.NewSet(attribute.String("method", "put"), attribute.String("outcome", "exists")): 1,
	}, sumAttrs(t, rm, "magus.cache.remote.ops"))
}

// TestBuzzSessionPoolCollects pins the one pool-event counter and the idle gauge it drives: a
// reuse drains one idle session, a return adds one, a cold checkout and an eviction move nothing.
func TestBuzzSessionPoolCollects(t *testing.T) {
	p, coll := collectLocal(t)
	ctx := context.Background()

	for _, ev := range []observability.SessionPoolEvent{
		observability.PoolCold, observability.PoolReturned, observability.PoolReturned,
		observability.PoolReused, observability.PoolEvicted,
	} {
		p.RecordBuzzSessionPool(ctx, ev)
	}

	rm, err := coll.Collect(ctx)
	require.NoError(t, err)

	assert.Equal(t, map[attribute.Set]int64{
		attribute.NewSet(attribute.String("event", "cold")):     1,
		attribute.NewSet(attribute.String("event", "returned")): 2,
		attribute.NewSet(attribute.String("event", "reused")):   1,
		attribute.NewSet(attribute.String("event", "evicted")):  1,
	}, sumAttrs(t, rm, "magus.buzz.session.pool.events"))
	idle, ok := sumInt64(t, rm, "magus.buzz.session.pool.idle")
	require.True(t, ok, "magus.buzz.session.pool.idle missing")
	assert.Equal(t, int64(1), idle)
}

// TestBuzzFamiliesCollect records one observation across the Buzz/MCP/Sandbox families and
// confirms the collection succeeds (no duplicate-instrument error) and carries them.
func TestBuzzFamiliesCollect(t *testing.T) {
	p, coll := collectLocal(t)
	ctx := context.Background()

	p.RecordMCPCall(ctx, observability.MCPCall{Tool: "graph", Outcome: observability.OutcomeOK, InputBytes: 100, OutputBytes: 200, Duration: 0.03})
	p.RecordSandboxRules(ctx, observability.SandboxRules{
		Access:   map[filesystem.Access]int64{filesystem.Read: 3, filesystem.Write: 2, filesystem.Exec: 0},
		EnvExact: 4,
		EnvGlob:  5,
		Scope:    "target",
	})
	p.RecordSandboxCheck(ctx, "read", "allow", "//app")
	p.RecordBuzzHostCall(ctx, observability.BuzzHostCall{Callable: "proc.exec", Outcome: observability.OutcomeError, Duration: 0.01})
	p.RecordBuzzSpellBuiltinsWarm(ctx, 0.05, "build")
	p.RecordBuzzJITRun(ctx)

	rm, err := coll.Collect(ctx)
	require.NoError(t, err)

	jit, ok := sumInt64(t, rm, "magus.buzz.jit.runs")
	require.True(t, ok, "magus.buzz.jit.runs missing")
	assert.Equal(t, int64(1), jit)

	assert.Equal(t, map[attribute.Set]int64{
		attribute.NewSet(attribute.String("access", "read"), attribute.String("scope", "target")):  3,
		attribute.NewSet(attribute.String("access", "write"), attribute.String("scope", "target")): 2,
	}, sumAttrs(t, rm, "magus.sandbox.rules"), "a zero count adds no series")

	// A histogram's count is the call count, so the duration histograms carry what the
	// dropped counters did, outcome included.
	count, _, attrs, ok := histFloat64(t, rm, "magus.mcp.tool.duration")
	require.True(t, ok, "magus.mcp.tool.duration missing")
	assert.Equal(t, uint64(1), count)
	assert.Equal(t, []attribute.Set{attribute.NewSet(attribute.String("tool", "graph"), attribute.String("outcome", "ok"))}, attrs)
	_, _, attrs, ok = histFloat64(t, rm, "magus.buzz.host.call.duration")
	require.True(t, ok, "magus.buzz.host.call.duration missing")
	assert.Equal(t, []attribute.Set{attribute.NewSet(attribute.String("callable", "proc.exec"), attribute.String("outcome", "error"))}, attrs)
	for _, gone := range []string{"magus.mcp.tool.calls", "magus.buzz.host.call.count", "magus.buzz.spell.builtins.count"} {
		_, present := sumInt64(t, rm, gone)
		assert.False(t, present, "%s restated a histogram's count", gone)
	}
}

// sumAttrs returns each data point of an Int64 sum named name as its attribute set and value.
func sumAttrs(t *testing.T, rm metricdata.ResourceMetrics, name string) map[attribute.Set]int64 {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			s, ok := m.Data.(metricdata.Sum[int64])
			require.True(t, ok, "%s is not an Int64 sum", name)
			out := map[attribute.Set]int64{}
			for _, dp := range s.DataPoints {
				out[dp.Attributes] = dp.Value
			}
			return out
		}
	}
	require.Failf(t, "instrument missing", "%s", name)
	return nil
}

// TestAgentCostFamilyCollects reads back the value and the attribute set of each
// magus.agent.* instrument, since a stray unbounded attribute is the failure mode a metric
// fed from a session's calls is most exposed to.
func TestAgentCostFamilyCollects(t *testing.T) {
	p, coll := collectLocal(t)
	ctx := context.Background()

	large, small, empty := 4000, 1000, 0
	p.RecordAgentCall(ctx, observability.AgentCall{AgentFacts: types.AgentFacts{
		Thread: types.AgentThreadMain, ContextModel: "claude-opus-5-5", ContextTokens: 300_000,
		Billing: types.AgentBillingMetered, DelegableClass: types.DelegableSearch, ResultChars: &large,
		Shadow: map[types.GuardProfile]types.GuardDecision{types.GuardProfileStrict: types.GuardDeny, types.GuardProfileLight: types.GuardAllow},
	}})
	p.RecordAgentCall(ctx, observability.AgentCall{AgentFacts: types.AgentFacts{
		ContextTokens: 5000, DelegableClass: types.DelegableSearch, ResultChars: &small, Billing: types.AgentBillingMetered,
	}})
	p.RecordAgentCall(ctx, observability.AgentCall{AgentFacts: types.AgentFacts{DelegableClass: types.DelegableRead, ResultChars: &empty}})
	p.RecordAgentCall(ctx, observability.AgentCall{})

	rm, err := coll.Collect(ctx)
	require.NoError(t, err)

	var tokens metricdata.Histogram[int64]
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == "magus.agent.context.tokens" {
				var ok bool
				tokens, ok = m.Data.(metricdata.Histogram[int64])
				require.True(t, ok)
			}
		}
	}
	got := map[attribute.Set]int64{}
	for _, dp := range tokens.DataPoints {
		got[dp.Attributes] = dp.Sum
	}
	assert.Equal(t, map[attribute.Set]int64{
		attribute.NewSet(attribute.String("thread", "main"), attribute.String("model", "claude-opus-5-5")): 300_000,
		attribute.NewSet(attribute.String("thread", "unknown"), attribute.String("model", "unknown")):      5000,
	}, got)

	assert.Equal(t, map[attribute.Set]int64{
		attribute.NewSet(attribute.String("class", "search")): 5000,
	}, sumAttrs(t, rm, "magus.agent.delegable.chars"))
	assert.Equal(t, map[attribute.Set]int64{
		attribute.NewSet(attribute.String("mode", "metered")): 2,
	}, sumAttrs(t, rm, "magus.agent.billing"))
	assert.Equal(t, map[attribute.Set]int64{
		attribute.NewSet(attribute.String("profile", "strict"), attribute.String("decision", "deny")): 1,
		attribute.NewSet(attribute.String("profile", "light"), attribute.String("decision", "allow")): 1,
	}, sumAttrs(t, rm, "magus.agent.shadow.verdicts"))
}
