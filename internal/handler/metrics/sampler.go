package metrics

import (
	"context"
	"time"

	metricsv1 "github.com/egladman/magus/proto/gen/go/magus/metrics/v1"
	"github.com/egladman/magus/types"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// collector is the narrow read side the aggregation and sampler need from
// otlp.Collector: one in-process metricdata collection, no export hop.
type collector interface {
	Collect(context.Context) (metricdata.ResourceMetrics, error)
}

// statusSource is the narrow live-pool read the sampler needs, satisfied by
// *console.Service (the same StatusReport the StatusService uses).
type statusSource interface {
	StatusReport(context.Context) types.StatusReport
}

// startSampler runs the utilization sampler until ctx is cancelled: it appends one Sample
// immediately, then one per tick. Each sample pairs the live pool occupancy with the
// cumulative activity counters, so the dashboard's grid and cache-rate trend have history.
func (s *Service) startSampler(ctx context.Context) {
	ticker := time.NewTicker(s.tick)
	defer ticker.Stop()
	s.sampleOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sampleOnce(ctx)
		}
	}
}

// sampleOnce reads pool occupancy and cumulative counters and appends one Sample. A status
// or collect failure degrades to the zero values for that field rather than dropping the
// sample, keeping the ring cadence steady.
func (s *Service) sampleOnce(ctx context.Context) {
	smp := &metricsv1.Sample{At: timestamppb.New(s.now())}

	if rep := s.stat.StatusReport(ctx); rep.Pool != nil {
		smp.Running = int32(rep.Pool.Running)
		smp.Capacity = int32(rep.Pool.Capacity)
		smp.Queued = int32(rep.Pool.Queued)
	}
	if rm, err := s.coll.Collect(ctx); err == nil {
		c := counters(rm)
		smp.CacheHits = c.cacheHits
		smp.CacheMisses = c.cacheMisses
		smp.TargetRuns = c.targetRuns
	}
	s.ring.Append(smp)
}
