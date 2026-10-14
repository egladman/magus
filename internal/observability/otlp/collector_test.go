package otlp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	colmetricpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"

	"github.com/egladman/magus/internal/observability"
)

// TestLocalCollectSnapshot exercises the real provider path: a LocalCollect provider (telemetry
// export OFF) records through the normal Provider methods, and Snapshot returns standard OTLP
// protobuf carrying those values: the wire the /dashboard reads. No external export, no network.
func TestLocalCollectSnapshot(t *testing.T) {
	p, err := New(context.Background(), observability.Config{LocalCollect: true, ServiceName: "magus-test"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })

	ctx := context.Background()
	p.RecordCacheRead(ctx, observability.CacheHit)
	p.RecordCacheRead(ctx, observability.CacheHit)
	p.RecordCacheRead(ctx, observability.CacheMiss)
	// Spans are export-only; in local-collect mode StartSpan must be a safe no-op.
	_, end := p.StartSpan(ctx, "noop")
	end(nil)

	raw, err := p.Snapshot(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, raw, "local-collect Snapshot returned no OTLP bytes")

	var req colmetricpb.ExportMetricsServiceRequest
	require.NoError(t, proto.Unmarshal(raw, &req), "snapshot is not OTLP protobuf")
	assert.Equal(t, int64(2), sumCounter(&req, "magus.cache.reads", "result", "hit"))
	assert.Equal(t, int64(1), sumCounter(&req, "magus.cache.reads", "result", "miss"))
}

// TestDisabledSnapshotNil confirms a fully-disabled provider (no export, no local collect)
// returns no snapshot and stays a no-op, so the CLI hot path is untouched.
func TestDisabledSnapshotNil(t *testing.T) {
	p, err := New(context.Background(), observability.Config{})
	require.NoError(t, err)
	raw, err := p.Snapshot(context.Background())
	require.NoError(t, err)
	assert.Nil(t, raw)
}

// sumCounter walks the OTLP metrics tree for a Sum metric by name and returns the integer
// value of its data point whose attribute key is value, or -1 when absent.
func sumCounter(req *colmetricpb.ExportMetricsServiceRequest, name, key, value string) int64 {
	for _, rm := range req.GetResourceMetrics() {
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				if m.GetName() != name {
					continue
				}
				for _, dp := range m.GetSum().GetDataPoints() {
					for _, kv := range dp.GetAttributes() {
						if kv.GetKey() == key && kv.GetValue().GetStringValue() == value {
							return dp.GetAsInt()
						}
					}
				}
			}
		}
	}
	return -1
}
