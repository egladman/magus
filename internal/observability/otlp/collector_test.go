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
// protobuf carrying those values - the wire the /dashboard reads. No external export, no network.
func TestLocalCollectSnapshot(t *testing.T) {
	p, err := New(context.Background(), observability.Config{LocalCollect: true, ServiceName: "magus-test"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })

	ctx := context.Background()
	p.RecordCacheHit(ctx, observability.Attr{Key: "outcome", Value: "hit"})
	p.RecordCacheHit(ctx, observability.Attr{Key: "outcome", Value: "hit"})
	p.RecordCacheMiss(ctx, observability.Attr{Key: "outcome", Value: "miss"})
	// Spans are export-only; in local-collect mode StartSpan must be a safe no-op.
	_, end := p.StartSpan(ctx, "noop")
	end(nil)

	raw, err := p.Snapshot(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, raw, "local-collect Snapshot returned no OTLP bytes")

	var req colmetricpb.ExportMetricsServiceRequest
	require.NoError(t, proto.Unmarshal(raw, &req), "snapshot is not OTLP protobuf")
	assert.Equal(t, int64(2), sumCounter(&req, "magus.cache.hits"))
	assert.Equal(t, int64(1), sumCounter(&req, "magus.cache.misses"))
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

// sumCounter walks the OTLP metrics tree for a Sum metric by name and returns its first data
// point's integer value, or -1 when absent.
func sumCounter(req *colmetricpb.ExportMetricsServiceRequest, name string) int64 {
	for _, rm := range req.GetResourceMetrics() {
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				if m.GetName() == name {
					if dps := m.GetSum().GetDataPoints(); len(dps) > 0 {
						return dps[0].GetAsInt()
					}
				}
			}
		}
	}
	return -1
}
