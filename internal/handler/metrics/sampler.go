package metrics

import (
	"context"
	"time"

	metricsv1 "github.com/egladman/magus/proto/gen/go/magus/metrics/v1alpha1"
	"github.com/egladman/magus/types"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/protobuf/proto"
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

// sampleOnce reads pool occupancy and cumulative counters and appends one Sample. The sample
// is always appended so the ring cadence stays steady, but a field whose read FAILED is left
// UNSET rather than zero: the counters are cumulative, so a zero written for an unreadable
// collection reads to any consumer as a counter reset followed by a spike, corrupting the
// rate on both sides of it. Leaving it unset says "not measured", which is what happened.
func (s *Service) sampleOnce(ctx context.Context) {
	smp := &metricsv1.Sample{SampleTime: timestamppb.New(s.now())}

	rep := s.stat.StatusReport(ctx)
	if rep.Pool != nil {
		smp.Running = proto.Int32(int32(rep.Pool.Running))
		smp.Capacity = proto.Int32(int32(rep.Pool.Capacity))
		smp.Queued = proto.Int32(int32(rep.Pool.Queued))
	}
	// The generation these cumulative counters belong to. Left unset when the report
	// carries no start instant (a non-daemon status), which reads downstream as "unknown
	// generation" and breaks the series rather than silently joining two processes.
	if !rep.ObservingSince.IsZero() {
		smp.ObserveStartTime = timestamppb.New(rep.ObservingSince)
	}
	if rm, err := s.coll.Collect(ctx); err == nil {
		c := counters(rm)
		smp.CacheHits = proto.Int64(c.cacheHits)
		smp.CacheMisses = proto.Int64(c.cacheMisses)
		smp.TargetRuns = proto.Int64(c.targetRuns)
	}
	s.ring.Append(smp)
}
