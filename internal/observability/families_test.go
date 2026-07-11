package observability

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// collectLocal builds a LocalCollect provider and returns a Collector over its in-process
// ManualReader, so a test can record through the real Provider methods and read the raw
// metricdata back without any network hop.
func collectLocal(t *testing.T) (Provider, *Collector) {
	t.Helper()
	p, err := New(context.Background(), Config{LocalCollect: true, ServiceName: "magus-test"})
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

	p.RecordRemoteOp(ctx, RemoteOp{Method: "put", Outcome: "stored", Duration: 0.02, Bytes: 512})

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

	p.RecordMCPCall(ctx, MCPCall{Tool: "graph", Outcome: "success", InputBytes: 100, OutputBytes: 200, Duration: 0.03})
	p.RecordSandboxRules(ctx, SandboxRules{Read: 3, Write: 2, Exec: 1, EnvExact: 4, EnvGlob: 5, Scope: "target"})
	p.RecordSandboxCheck(ctx, "read", "allow", "//app")
	p.RecordBuzzHostCall(ctx, BuzzHostCall{Callable: "os.exec", Outcome: "success", Duration: 0.01})
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
