package observability

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	otlptracegrpc "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otlptracehttp "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// New returns a Provider backed by the configured OTLP collector.
// When cfg.Enabled is false it returns a no-op disabledProvider without opening any connection.
func New(ctx context.Context, cfg Config) (Provider, error) {
	// Disabled with no local collection requested: a true no-op (the common CLI path).
	if !cfg.Enabled && !cfg.LocalCollect {
		return disabledProvider{}, nil
	}
	// External export requires an endpoint; local-only collection (the daemon's dashboard
	// feed) does not.
	if cfg.Enabled && cfg.Endpoint == "" {
		return nil, errors.New("observability: telemetry.enabled is true but telemetry.endpoint is empty")
	}

	resAttrs := []attribute.KeyValue{
		semconv.ServiceName(cfg.ServiceName),
		semconv.ServiceVersion(cfg.ServiceVersion),
	}
	if cfg.WorkspaceRoot != "" {
		resAttrs = append(resAttrs, attribute.String("magus.workspace.root", cfg.WorkspaceRoot))
	}
	res, err := resource.New(
		ctx,
		resource.WithAttributes(resAttrs...),
		resource.WithProcess(),
		resource.WithHost(),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: build resource: %w", err)
	}

	// The meter provider always carries a capturing reader (for on-demand OTLP snapshots the
	// dashboard reads) plus, when telemetry is enabled, the external OTLP push reader.
	mp, mShutdown, capT, manual, err := newMeterProvider(ctx, cfg, res)
	if err != nil {
		return nil, err
	}

	// Tracing (spans) is export-only: skip it entirely in local-collect-only mode, and only
	// then install the global providers (the local capture provider must not clobber the
	// global for other otel.Meter/Tracer users).
	var tShutdown func(context.Context) error
	var tracer trace.Tracer
	if cfg.Enabled {
		tp, ts, terr := newTracerProvider(ctx, cfg, res)
		if terr != nil {
			return nil, terr
		}
		tShutdown = ts
		otel.SetMeterProvider(mp)
		otel.SetTracerProvider(tp)
		tracer = tp.Tracer("github.com/egladman/magus/internal/observability")
	}

	meter := mp.Meter("github.com/egladman/magus/internal/observability")

	hits, err := meter.Int64Counter(
		"magus.cache.hits",
		metric.WithDescription("Number of magus cache hits."),
		metric.WithUnit("{call}"),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: cache.hits counter: %w", err)
	}
	misses, err := meter.Int64Counter(
		"magus.cache.misses",
		metric.WithDescription("Number of magus cache misses."),
		metric.WithUnit("{call}"),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: cache.misses counter: %w", err)
	}
	errs, err := meter.Int64Counter(
		"magus.cache.errors",
		metric.WithDescription("Number of cached step failures."),
		metric.WithUnit("{call}"),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: cache.errors counter: %w", err)
	}
	dur, err := meter.Float64Histogram(
		"magus.cache.duration",
		metric.WithDescription("Wall-clock duration of a single Cache.Run, in seconds."),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: cache.duration histogram: %w", err)
	}
	graphQueryDur, err := meter.Float64Histogram(
		"magus.graph.query.duration",
		metric.WithDescription("Wall-clock duration of a single graph query, in seconds."),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: graph.query.duration histogram: %w", err)
	}
	graphQueryCount, err := meter.Int64Counter(
		"magus.graph.queries",
		metric.WithDescription("Number of graph query operations."),
		metric.WithUnit("{call}"),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: graph.queries counter: %w", err)
	}

	targetRuns, err := meter.Int64Counter(
		"magus.target.runs",
		metric.WithDescription("Number of target executions, including cache replays."),
		metric.WithUnit("{call}"),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: target.runs counter: %w", err)
	}
	targetDur, err := meter.Float64Histogram(
		"magus.target.duration",
		metric.WithDescription("Wall-clock duration of a single target execution, in seconds."),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: target.duration histogram: %w", err)
	}

	poolWait, err := meter.Float64Histogram(
		"magus.pool.wait.duration",
		metric.WithDescription("Time a target spent waiting to acquire a concurrency slot, in seconds."),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: pool.wait.duration histogram: %w", err)
	}
	poolInflight, err := meter.Int64UpDownCounter(
		"magus.pool.slots.inflight",
		metric.WithDescription("Number of concurrency slots currently in use."),
		metric.WithUnit("{slot}"),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: pool.slots.inflight counter: %w", err)
	}

	// Remote cache backend (S3, GitHub Actions, ...). These mirror the local
	// magus.cache.{hits,misses,errors,duration} vocabulary under a .remote prefix
	// so a remote hit is never conflated with a local one — they live in different
	// counters. magus.cache.remote.io.size is the dimension local caching lacks:
	// bytes moved over the network, which maps to egress cost.
	remoteHits, err := meter.Int64Counter(
		"magus.cache.remote.hits",
		metric.WithDescription("Number of remote cache hits (get returned an entry)."),
		metric.WithUnit("{call}"),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: cache.remote.hits counter: %w", err)
	}
	remoteMisses, err := meter.Int64Counter(
		"magus.cache.remote.misses",
		metric.WithDescription("Number of remote cache misses (get found no entry)."),
		metric.WithUnit("{call}"),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: cache.remote.misses counter: %w", err)
	}
	remoteErrs, err := meter.Int64Counter(
		"magus.cache.remote.errors",
		metric.WithDescription("Number of failed remote cache operations."),
		metric.WithUnit("{call}"),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: cache.remote.errors counter: %w", err)
	}
	remoteDur, err := meter.Float64Histogram(
		"magus.cache.remote.duration",
		metric.WithDescription("Wall-clock duration of a single remote cache operation, in seconds."),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: cache.remote.duration histogram: %w", err)
	}
	remoteBytes, err := meter.Int64Histogram(
		"magus.cache.remote.io.size",
		metric.WithDescription("Bytes transferred by a single remote cache get or put."),
		metric.WithUnit("By"),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: cache.remote.io.size histogram: %w", err)
	}

	return &otelProvider{
		mp:              mp,
		capture:         capT,
		manual:          manual,
		mShutdown:       mShutdown,
		tShutdown:       tShutdown,
		tracer:          tracer,
		hits:            hits,
		misses:          misses,
		errs:            errs,
		dur:             dur,
		graphQueryDur:   graphQueryDur,
		graphQueryCount: graphQueryCount,
		targetRuns:      targetRuns,
		targetDur:       targetDur,
		poolWait:        poolWait,
		poolInflight:    poolInflight,
		remoteHits:      remoteHits,
		remoteMisses:    remoteMisses,
		remoteErrs:      remoteErrs,
		remoteDur:       remoteDur,
		remoteBytes:     remoteBytes,
	}, nil
}

type otelProvider struct {
	mu        sync.Mutex
	mp        *sdkmetric.MeterProvider // for ForceFlush in Snapshot
	capture   *captureTransport        // captures OTLP bytes; nil when not collecting locally
	manual    *sdkmetric.ManualReader  // in-process Collect for Derived; no export hop
	mShutdown func(context.Context) error
	tShutdown func(context.Context) error
	tracer    trace.Tracer

	hits            metric.Int64Counter
	misses          metric.Int64Counter
	errs            metric.Int64Counter
	dur             metric.Float64Histogram
	graphQueryDur   metric.Float64Histogram
	graphQueryCount metric.Int64Counter
	targetRuns      metric.Int64Counter
	targetDur       metric.Float64Histogram
	poolWait        metric.Float64Histogram
	poolInflight    metric.Int64UpDownCounter
	remoteHits      metric.Int64Counter
	remoteMisses    metric.Int64Counter
	remoteErrs      metric.Int64Counter
	remoteDur       metric.Float64Histogram
	remoteBytes     metric.Int64Histogram
}

func (*otelProvider) Enabled() bool { return true }

func (p *otelProvider) RecordCacheHit(ctx context.Context, attrs ...Attr) {
	p.hits.Add(ctx, 1, metric.WithAttributes(toKV(attrs)...))
}

func (p *otelProvider) RecordCacheMiss(ctx context.Context, attrs ...Attr) {
	p.misses.Add(ctx, 1, metric.WithAttributes(toKV(attrs)...))
}

func (p *otelProvider) RecordCacheError(ctx context.Context, attrs ...Attr) {
	p.errs.Add(ctx, 1, metric.WithAttributes(toKV(attrs)...))
}

func (p *otelProvider) RecordCacheDuration(ctx context.Context, secs float64, attrs ...Attr) {
	p.dur.Record(ctx, secs, metric.WithAttributes(toKV(attrs)...))
}

func (p *otelProvider) RecordGraphQuery(ctx context.Context, secs float64, attrs ...Attr) {
	kv := metric.WithAttributes(toKV(attrs)...)
	p.graphQueryDur.Record(ctx, secs, kv)
	p.graphQueryCount.Add(ctx, 1, kv)
}

func (p *otelProvider) StartSpan(ctx context.Context, name string, attrs ...Attr) (context.Context, func(error)) {
	if p.tracer == nil { // local-collect-only mode records metrics but not spans
		return ctx, func(error) {}
	}
	ctx, span := p.tracer.Start(
		ctx, name,
		trace.WithAttributes(toKV(attrs)...),
	)
	return ctx, func(err error) {
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
			span.RecordError(err)
		} else {
			span.SetStatus(codes.Ok, "")
		}
		span.End()
	}
}

func (p *otelProvider) RecordRemoteOp(ctx context.Context, op RemoteOp) {
	kv := metric.WithAttributes(
		attribute.String("op", op.Op),
		attribute.String("outcome", op.Outcome),
	)
	switch op.Outcome {
	case "hit":
		p.remoteHits.Add(ctx, 1, kv)
	case "miss":
		p.remoteMisses.Add(ctx, 1, kv)
	case "error":
		p.remoteErrs.Add(ctx, 1, kv)
	}
	p.remoteDur.Record(ctx, op.Duration, kv)
	if op.Bytes > 0 {
		p.remoteBytes.Record(ctx, op.Bytes, metric.WithAttributes(attribute.String("op", op.Op)))
	}
}

func (p *otelProvider) RecordTargetRun(ctx context.Context, secs float64, attrs ...Attr) {
	kv := metric.WithAttributes(toKV(attrs)...)
	p.targetRuns.Add(ctx, 1, kv)
	p.targetDur.Record(ctx, secs, kv)
}

func (p *otelProvider) RecordPoolAcquire(ctx context.Context, waitSecs float64, n int64) {
	p.poolWait.Record(ctx, waitSecs)
	p.poolInflight.Add(ctx, n)
}

func (p *otelProvider) RecordPoolRelease(ctx context.Context, n int64) {
	p.poolInflight.Add(ctx, -n)
}

func (p *otelProvider) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	var errs []error
	if p.mShutdown != nil {
		if err := p.mShutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("meter shutdown: %w", err))
		}
		p.mShutdown = nil
	}
	if p.tShutdown != nil {
		if err := p.tShutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("tracer shutdown: %w", err))
		}
		p.tShutdown = nil
	}
	return errors.Join(errs...)
}

// disabledProvider honours the Provider contract without opening any connection.
type disabledProvider struct{}

func (disabledProvider) Enabled() bool                                               { return false }
func (disabledProvider) RecordCacheHit(_ context.Context, _ ...Attr)                 {}
func (disabledProvider) RecordCacheMiss(_ context.Context, _ ...Attr)                {}
func (disabledProvider) RecordCacheError(_ context.Context, _ ...Attr)               {}
func (disabledProvider) RecordCacheDuration(_ context.Context, _ float64, _ ...Attr) {}
func (disabledProvider) RecordGraphQuery(_ context.Context, _ float64, _ ...Attr)    {}
func (disabledProvider) RecordRemoteOp(_ context.Context, _ RemoteOp)                {}
func (disabledProvider) StartSpan(ctx context.Context, _ string, _ ...Attr) (context.Context, func(error)) {
	return ctx, func(error) {}
}
func (disabledProvider) RecordTargetRun(_ context.Context, _ float64, _ ...Attr) {}
func (disabledProvider) RecordPoolAcquire(_ context.Context, _ float64, _ int64) {}
func (disabledProvider) RecordPoolRelease(_ context.Context, _ int64)            {}
func (disabledProvider) Snapshot(_ context.Context) ([]byte, error)              { return nil, nil }
func (disabledProvider) Shutdown(_ context.Context) error                        { return nil }

func toKV(attrs []Attr) []attribute.KeyValue {
	out := make([]attribute.KeyValue, len(attrs))
	for i, a := range attrs {
		out[i] = attribute.String(a.Key, a.Value)
	}
	return out
}

func newTracerProvider(ctx context.Context, cfg Config, res *resource.Resource) (*sdktrace.TracerProvider, func(context.Context) error, error) {
	var (
		exp sdktrace.SpanExporter
		err error
	)
	switch cfg.Protocol {
	case "http":
		opts := []otlptracehttp.Option{otlptracehttp.WithEndpoint(cfg.Endpoint)}
		if cfg.Insecure {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		if len(cfg.Headers) > 0 {
			opts = append(opts, otlptracehttp.WithHeaders(cfg.Headers))
		}
		exp, err = otlptracehttp.New(ctx, opts...)
	default:
		opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(cfg.Endpoint)}
		if cfg.Insecure {
			opts = append(opts, otlptracegrpc.WithInsecure())
		}
		if len(cfg.Headers) > 0 {
			opts = append(opts, otlptracegrpc.WithHeaders(cfg.Headers))
		}
		exp, err = otlptracegrpc.New(ctx, opts...)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("observability: trace exporter: %w", err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(exp),
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(cfg.SampleRatio)),
	)
	return tp, tp.Shutdown, nil
}

func newMeterProvider(ctx context.Context, cfg Config, res *resource.Resource) (*sdkmetric.MeterProvider, func(context.Context) error, *captureTransport, *sdkmetric.ManualReader, error) {
	// The capturing reader is always present: it yields on-demand OTLP snapshots for the
	// dashboard without a network hop (see collector.go). ForceFlush drives it.
	capReader, capT, err := newCaptureReader(ctx)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("observability: capture reader: %w", err)
	}
	// The manual reader backs Derived: an in-process Collect of metricdata (histogram buckets
	// and counters) with no exporter hop, which the dashboard aggregation reads on demand.
	manual := sdkmetric.NewManualReader()
	opts := []sdkmetric.Option{sdkmetric.WithResource(res), sdkmetric.WithReader(capReader), sdkmetric.WithReader(manual)}

	// The external push exporter is added only when telemetry export is enabled.
	if cfg.Enabled {
		var exp sdkmetric.Exporter
		switch cfg.Protocol {
		case "http":
			hopts := []otlpmetrichttp.Option{otlpmetrichttp.WithEndpoint(cfg.Endpoint)}
			if cfg.Insecure {
				hopts = append(hopts, otlpmetrichttp.WithInsecure())
			}
			if len(cfg.Headers) > 0 {
				hopts = append(hopts, otlpmetrichttp.WithHeaders(cfg.Headers))
			}
			exp, err = otlpmetrichttp.New(ctx, hopts...)
		default:
			gopts := []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpoint(cfg.Endpoint)}
			if cfg.Insecure {
				gopts = append(gopts, otlpmetricgrpc.WithInsecure())
			}
			if len(cfg.Headers) > 0 {
				gopts = append(gopts, otlpmetricgrpc.WithHeaders(cfg.Headers))
			}
			exp, err = otlpmetricgrpc.New(ctx, gopts...)
		}
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("observability: metric exporter: %w", err)
		}
		opts = append(opts, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(30*time.Second))))
	}

	mp := sdkmetric.NewMeterProvider(opts...)
	return mp, mp.Shutdown, capT, manual, nil
}
