package metrics

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	metricsv1 "github.com/egladman/magus/proto/gen/go/magus/metrics/v1alpha1"
	"github.com/egladman/magus/proto/gen/go/magus/metrics/v1alpha1/metricsv1alpha1connect"
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
	snap := resp.Msg
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
	require.Equal(t, int32(3), samples[0].GetRunning())
	require.Equal(t, int32(8), samples[0].GetCapacity())
	require.Equal(t, int32(2), samples[0].GetQueued())
	require.Equal(t, int64(7), samples[0].GetCacheHits())
	require.Equal(t, int64(3), samples[0].GetCacheMisses())
	require.Equal(t, int64(10), samples[0].GetTargetRuns())
	require.Equal(t, at.Unix(), samples[0].SampleTime.AsTime().Unix())
}

// A tick whose collection failed leaves the counters UNSET rather than zero. Zero is a
// measurement, and in a cumulative series it reads downstream as a counter reset followed
// by a spike - so this is the difference between "we did not look" and "nothing happened".
func TestSampleOnceLeavesFailedReadsUnset(t *testing.T) {
	at := time.Unix(1_700_000_000, 0).UTC()
	svc := NewService(fakeCollector{err: errors.New("collect boom")}, fakeStatus{},
		WithClock(func() time.Time { return at }))
	svc.sampleOnce(context.Background())

	samples := svc.ring.Snapshot()
	require.Len(t, samples, 1)
	require.Nil(t, samples[0].CacheHits, "a failed collect must not record a zero")
	require.Nil(t, samples[0].CacheMisses)
	require.Nil(t, samples[0].TargetRuns)
	require.Nil(t, samples[0].Running, "an unreadable pool must not record as idle")
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
	path, handler := metricsv1alpha1connect.NewMetricsServiceHandler(svc)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := metricsv1alpha1connect.NewMetricsServiceClient(srv.Client(), srv.URL)
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
