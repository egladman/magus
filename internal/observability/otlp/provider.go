// Package otlp holds the concrete OpenTelemetry/OTLP provider that backs the
// observability.Provider interface. It is a subpackage so the heavy OTLP SDK
// dependency graph (which pulls in net/http and grpc) stays out of the light
// parent package and out of the Buzz playground's wasm build (TinyGo's js/wasm
// cannot compile net/http).
package otlp

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

	"github.com/egladman/magus/internal/observability"
)

// New returns a Provider backed by the configured OTLP collector. With cfg.Enabled and
// cfg.LocalCollect both false it returns [observability.Nop] without opening any connection.
func New(ctx context.Context, cfg observability.Config) (observability.Provider, error) {
	// Disabled with no local collection requested: a true no-op (the common CLI path).
	if !cfg.Enabled && !cfg.LocalCollect {
		return observability.Nop{}, nil
	}
	// External export requires an endpoint; local-only collection (the server's dashboard
	// feed) does not.
	if cfg.Enabled && cfg.Endpoint == "" {
		return nil, errors.New("telemetry.enabled is true but telemetry.endpoint is empty")
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
		return nil, fmt.Errorf("build resource: %w", err)
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

	r := reg{m: meter}
	reads := r.i64c("magus.cache.reads", "Cache.Run results, by whether the step hit, missed or failed.", "{call}")
	dur := r.f64h("magus.cache.duration", "Wall-clock duration of a single Cache.Run, in seconds.")
	// The savings lens: magus.cache.duration says what a hit COST, this says what it AVOIDED.
	// One observation per hit, so the histogram's sum is the wall-clock the cache has saved and
	// its distribution says which hits are worth having. It is the exported half of
	// cache.Stats.SavedMs, which until now only ever reached an end-of-run footer.
	saved := r.f64h("magus.cache.saved.duration", "Wall-clock duration a cache hit replayed instead of running, in seconds.")
	graphQueryDur := r.f64h("magus.graph.query.duration", "Wall-clock duration of a single graph query, in seconds.")

	targetDur := r.f64h("magus.target.duration", "Wall-clock duration of a single target execution, in seconds.")

	poolWait := r.f64h("magus.pool.wait.duration", "Time a target spent waiting to acquire a concurrency slot, in seconds.")
	poolRunning := r.i64ud("magus.pool.slots.running", "Number of concurrency slots currently running.", "{slot}")
	poolQueued := r.i64ud("magus.pool.slots.queued", "Number of callers currently queued to acquire a concurrency slot.", "{slot}")

	// Remote cache backend (S3, GitHub Actions, ...), under a .remote prefix so a remote hit
	// is never conflated with a local one. magus.cache.remote.io.size is the dimension local
	// caching lacks: bytes moved over the network, which maps to egress cost.
	remoteOps := r.i64c("magus.cache.remote.ops", "Remote cache operations, by method and what the backend answered.", "{call}")
	remoteDur := r.f64h("magus.cache.remote.duration", "Wall-clock duration of a single remote cache operation, in seconds.")
	remoteBytes := r.i64h("magus.cache.remote.io.size", "Bytes transferred by a single remote cache get or put.", "By")
	if r.err != nil {
		return nil, r.err
	}

	mcp, err := newMCPInstruments(meter)
	if err != nil {
		return nil, err
	}
	sandbox, err := newSandboxInstruments(meter)
	if err != nil {
		return nil, err
	}
	buzz, err := newBuzzInstruments(meter)
	if err != nil {
		return nil, err
	}
	agent, err := newAgentInstruments(meter)
	if err != nil {
		return nil, err
	}
	agentCost, err := newAgentCostInstruments(meter)
	if err != nil {
		return nil, err
	}

	return &otelProvider{
		mp:            mp,
		capture:       capT,
		manual:        manual,
		mShutdown:     mShutdown,
		tShutdown:     tShutdown,
		tracer:        tracer,
		reads:         reads,
		dur:           dur,
		saved:         saved,
		graphQueryDur: graphQueryDur,
		targetDur:     targetDur,
		poolWait:      poolWait,
		poolRunning:   poolRunning,
		poolQueued:    poolQueued,
		remoteOps:     remoteOps,
		remoteDur:     remoteDur,
		remoteBytes:   remoteBytes,
		mcp:           mcp,
		sandbox:       sandbox,
		buzz:          buzz,
		agent:         agent,
		agentCost:     agentCost,
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

	reads         metric.Int64Counter
	dur           metric.Float64Histogram
	saved         metric.Float64Histogram
	graphQueryDur metric.Float64Histogram
	targetDur     metric.Float64Histogram
	poolWait      metric.Float64Histogram
	poolRunning   metric.Int64UpDownCounter
	poolQueued    metric.Int64UpDownCounter
	remoteOps     metric.Int64Counter
	remoteDur     metric.Float64Histogram
	remoteBytes   metric.Int64Histogram

	mcp     mcpInstruments
	sandbox sandboxInstruments
	buzz    buzzInstruments
	agent   agentInstruments

	agentCost agentCostInstruments
}

func (*otelProvider) Enabled() bool { return true }

func (p *otelProvider) RecordCacheRead(ctx context.Context, result observability.CacheResult, attrs ...observability.Attr) {
	kv := append(toKV(attrs), attribute.String("result", string(result)))
	p.reads.Add(ctx, 1, metric.WithAttributes(kv...))
}

func (p *otelProvider) RecordCacheDuration(ctx context.Context, secs float64, result observability.CacheResult) {
	p.dur.Record(ctx, secs, metric.WithAttributes(attribute.String("result", string(result))))
}

func (p *otelProvider) RecordCacheSaved(ctx context.Context, secs float64) {
	p.saved.Record(ctx, secs)
}

func (p *otelProvider) RecordGraphQuery(ctx context.Context, secs float64, attrs ...observability.Attr) {
	p.graphQueryDur.Record(ctx, secs, metric.WithAttributes(toKV(attrs)...))
}

func (p *otelProvider) StartSpan(ctx context.Context, name string, attrs ...observability.Attr) (context.Context, func(error)) {
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

func (p *otelProvider) RecordRemoteOp(ctx context.Context, op observability.RemoteOp) {
	kv := metric.WithAttributes(
		attribute.String("method", op.Method),
		attribute.String("outcome", string(op.Outcome)),
	)
	p.remoteOps.Add(ctx, 1, kv)
	p.remoteDur.Record(ctx, op.Duration, kv)
	if op.Bytes > 0 {
		p.remoteBytes.Record(ctx, op.Bytes, metric.WithAttributes(attribute.String("method", op.Method)))
	}
}

func (p *otelProvider) RecordTargetRun(ctx context.Context, secs float64, attrs ...observability.Attr) {
	p.targetDur.Record(ctx, secs, metric.WithAttributes(toKV(attrs)...))
}

func (p *otelProvider) RecordPoolWait(ctx context.Context, secs float64) {
	p.poolWait.Record(ctx, secs)
}

func (p *otelProvider) RecordPoolSlots(ctx context.Context, state observability.PoolSlotState, delta int64) {
	switch state {
	case observability.PoolQueued:
		p.poolQueued.Add(ctx, delta)
	case observability.PoolRunning:
		p.poolRunning.Add(ctx, delta)
	}
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

func toKV(attrs []observability.Attr) []attribute.KeyValue {
	out := make([]attribute.KeyValue, len(attrs))
	for i, a := range attrs {
		out[i] = attribute.String(a.Key, a.Value)
	}
	return out
}

func newTracerProvider(ctx context.Context, cfg observability.Config, res *resource.Resource) (*sdktrace.TracerProvider, func(context.Context) error, error) {
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
		return nil, nil, fmt.Errorf("trace exporter: %w", err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(exp),
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(cfg.SampleRatio)),
	)
	return tp, tp.Shutdown, nil
}

func newMeterProvider(ctx context.Context, cfg observability.Config, res *resource.Resource) (*sdkmetric.MeterProvider, func(context.Context) error, *captureTransport, *sdkmetric.ManualReader, error) {
	// The capturing reader is always present: it yields on-demand OTLP snapshots for the
	// dashboard without a network hop (see collector.go). ForceFlush drives it.
	capReader, capT, err := newCaptureReader(ctx)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("capture reader: %w", err)
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
			return nil, nil, nil, nil, fmt.Errorf("metric exporter: %w", err)
		}
		opts = append(opts, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(30*time.Second))))
	}

	mp := sdkmetric.NewMeterProvider(opts...)
	return mp, mp.Shutdown, capT, manual, nil
}
