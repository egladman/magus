package metrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	metricsv1 "github.com/egladman/magus/proto/gen/go/magus/metrics/v1"
	"github.com/egladman/magus/proto/gen/go/magus/metrics/v1/metricsv1connect"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

type fakeCollector struct {
	rm  metricdata.ResourceMetrics
	err error
}

func (f fakeCollector) Collect(context.Context) (metricdata.ResourceMetrics, error) {
	return f.rm, f.err
}

type fakeStatus struct{ rep types.StatusReport }

func (f fakeStatus) StatusReport(context.Context) types.StatusReport { return f.rep }

func fixtureRM() metricdata.ResourceMetrics {
	return metricdata.ResourceMetrics{
		ScopeMetrics: []metricdata.ScopeMetrics{{
			Metrics: []metricdata.Metrics{
				floatHist(instTargetDuration, []float64{1, 2}, []uint64{0, 10, 0}, 15),
				intCounter(instCacheHits, 7),
				intCounter(instCacheMisses, 3),
				intCounter(instTargetRuns, 10),
			},
		}},
	}
}

func TestGetMetrics(t *testing.T) {
	at := time.Unix(1_700_000_000, 0).UTC()
	svc := NewService(fakeCollector{rm: fixtureRM()}, fakeStatus{}, WithClock(func() time.Time { return at }))

	resp, err := svc.GetMetrics(context.Background(), connect.NewRequest(&metricsv1.GetMetricsRequest{}))
	require.NoError(t, err)
	snap := resp.Msg.Snapshot
	require.NotNil(t, snap)
	require.Equal(t, int64(10), snap.Target.Count)
	require.InDelta(t, 1.5, snap.Target.P50, 1e-9)
}

func TestSampleOncePopulatesRingFromPoolAndCounters(t *testing.T) {
	at := time.Unix(1_700_000_000, 0).UTC()
	stat := fakeStatus{rep: types.StatusReport{Pool: &types.StatusOutput{
		Running: 3, Capacity: 8, Queued: 2,
	}}}
	svc := NewService(fakeCollector{rm: fixtureRM()}, stat,
		WithClock(func() time.Time { return at }),
		WithRingCapacity(4),
	)

	svc.sampleOnce(context.Background())

	samples := svc.ring.Snapshot()
	require.Len(t, samples, 1)
	want := &metricsv1.Sample{
		At:          samples[0].At, // timestamp compared separately below
		Running:     3,
		Capacity:    8,
		Queued:      2,
		CacheHits:   7,
		CacheMisses: 3,
		TargetRuns:  10,
	}
	require.Equal(t, want.Running, samples[0].Running)
	require.Equal(t, want.Capacity, samples[0].Capacity)
	require.Equal(t, want.Queued, samples[0].Queued)
	require.Equal(t, want.CacheHits, samples[0].CacheHits)
	require.Equal(t, want.CacheMisses, samples[0].CacheMisses)
	require.Equal(t, want.TargetRuns, samples[0].TargetRuns)
	require.Equal(t, at.Unix(), samples[0].At.AsTime().Unix())
}

func TestStreamMetricsSendsBackfillThenSnapshot(t *testing.T) {
	at := time.Unix(1_700_000_000, 0).UTC()
	svc := NewService(fakeCollector{rm: fixtureRM()}, fakeStatus{},
		WithClock(func() time.Time { return at }),
		WithTick(5*time.Millisecond),
		WithRingCapacity(4),
	)
	// Seed one backfill sample.
	svc.sampleOnce(context.Background())

	// Exercise the real wire path: the generated handler + client over httptest.
	path, handler := metricsv1connect.NewMetricsServiceHandler(svc)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := metricsv1connect.NewMetricsServiceClient(srv.Client(), srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := client.StreamMetrics(ctx, connect.NewRequest(&metricsv1.StreamMetricsRequest{}))
	require.NoError(t, err)
	defer stream.Close()

	// First frame is the backfill carrying the one seeded sample.
	require.True(t, stream.Receive(), "expected a backfill frame")
	first := stream.Msg()
	require.NotNil(t, first.GetBackfill())
	require.Len(t, first.GetBackfill().Samples, 1)

	// A subsequent frame is a live snapshot.
	require.True(t, stream.Receive(), "expected a snapshot frame")
	require.NotNil(t, stream.Msg().GetSnapshot())
}
