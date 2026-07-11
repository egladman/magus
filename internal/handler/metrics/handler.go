package metrics

import (
	"context"
	"time"

	"connectrpc.com/connect"
	metricsv1 "github.com/egladman/magus/proto/gen/go/magus/metrics/v1"
	"github.com/egladman/magus/proto/gen/go/magus/metrics/v1/metricsv1connect"
)

// defaultTick is the sampler and stream cadence: one Sample appended and one Snapshot
// pushed per second, matching the dashboard's live refresh.
const defaultTick = time.Second

// Service implements metricsv1connect.MetricsServiceHandler for the /dashboard: it serves
// the current derived Snapshot (GetMetrics) and a backfilled live stream (StreamMetrics),
// reading raw metricdata through a collector and keeping a rolling Sample ring. Construct it
// with NewService and launch its sampler with Start.
type Service struct {
	coll collector
	stat statusSource
	ring *ring
	now  func() time.Time
	tick time.Duration
}

var _ metricsv1connect.MetricsServiceHandler = (*Service)(nil)

// Option customizes a Service; production callers pass none. Test seams inject a clock, the
// tick interval, and the ring capacity.
type Option func(*Service)

// WithClock overrides the sample/snapshot timestamp source (tests use a fixed clock).
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// WithTick overrides the sampler and stream cadence.
func WithTick(d time.Duration) Option { return func(s *Service) { s.tick = d } }

// WithRingCapacity overrides the backfill ring capacity.
func WithRingCapacity(n int) Option { return func(s *Service) { s.ring = newRing(n) } }

// NewService builds a Service reading derived metrics from coll and live pool occupancy
// from stat. Call Start to begin filling the backfill ring.
func NewService(coll collector, stat statusSource, opts ...Option) *Service {
	s := &Service{
		coll: coll,
		stat: stat,
		ring: newRing(ringCapacity),
		now:  time.Now,
		tick: defaultTick,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Start launches the utilization sampler goroutine; it stops when ctx is cancelled.
func (s *Service) Start(ctx context.Context) {
	go s.startSampler(ctx)
}

// GetMetrics returns the current derived snapshot.
func (s *Service) GetMetrics(ctx context.Context, _ *connect.Request[metricsv1.GetMetricsRequest]) (*connect.Response[metricsv1.GetMetricsResponse], error) {
	snap, err := s.snapshot(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&metricsv1.GetMetricsResponse{Snapshot: snap}), nil
}

// StreamMetrics sends exactly one Backfill (the ring history, oldest-first), then a fresh
// Snapshot on each tick until the client disconnects (ctx cancelled).
func (s *Service) StreamMetrics(ctx context.Context, _ *connect.Request[metricsv1.StreamMetricsRequest], stream *connect.ServerStream[metricsv1.StreamMetricsResponse]) error {
	backfill := &metricsv1.StreamMetricsResponse{
		Of: &metricsv1.StreamMetricsResponse_Backfill{
			Backfill: &metricsv1.Backfill{Samples: s.ring.Snapshot()},
		},
	}
	if err := stream.Send(backfill); err != nil {
		return err
	}

	ticker := time.NewTicker(s.tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			snap, err := s.snapshot(ctx)
			if err != nil {
				return connect.NewError(connect.CodeInternal, err)
			}
			msg := &metricsv1.StreamMetricsResponse{
				Of: &metricsv1.StreamMetricsResponse_Snapshot{Snapshot: snap},
			}
			if err := stream.Send(msg); err != nil {
				return err
			}
		}
	}
}

// snapshot collects the current metricdata and aggregates it into a derived Snapshot.
func (s *Service) snapshot(ctx context.Context) (*metricsv1.Snapshot, error) {
	rm, err := s.coll.Collect(ctx)
	if err != nil {
		return nil, err
	}
	return Aggregate(rm, s.now()), nil
}
