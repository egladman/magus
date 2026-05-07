// Package observability provides OpenTelemetry instrumentation for magus.
// Telemetry is OFF by default; set telemetry.enabled=true in magus.yaml to export.
package observability

import (
	"context"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
)

// Config holds the values needed to start the OTel exporter. Construct via [ConfigFromTelemetry].
type Config struct {
	Enabled        bool              // gates exporter setup; false = no-op
	Endpoint       string            // OTLP collector host:port (no scheme); required when Enabled
	Protocol       string            // "grpc" (default) or "http"
	Insecure       bool              // disable TLS for the OTLP exporter
	Headers        map[string]string // static headers on every OTLP request
	ServiceName    string            // resource attribute service.name
	ServiceVersion string            // resource attribute service.version
	SampleRatio    float64           // head-based trace sampling ratio [0,1]
	WorkspaceRoot  string            // stamped as magus.workspace.root when set
}

// ConfigFromTelemetry converts a magus.yaml telemetry section into a Provider Config with defaults applied.
func ConfigFromTelemetry(t config.Telemetry, version, workspaceRoot string) Config {
	cfg := Config{
		Enabled:        t.Enabled,
		Endpoint:       t.Endpoint,
		Protocol:       t.Protocol,
		Insecure:       t.Insecure,
		Headers:        t.Headers,
		ServiceName:    t.ServiceName,
		ServiceVersion: version,
		SampleRatio:    t.SampleRatio,
		WorkspaceRoot:  workspaceRoot,
	}
	if cfg.Protocol == "" {
		cfg.Protocol = "grpc"
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "magus"
	}
	if cfg.SampleRatio == 0 {
		cfg.SampleRatio = 1.0
	}
	return cfg
}

// Provider is the OTel runtime surface. All methods are concurrency-safe; all are no-ops when Enabled=false.
type Provider interface {
	Enabled() bool
	RecordCacheHit(ctx context.Context, attrs ...Attr)
	RecordCacheMiss(ctx context.Context, attrs ...Attr)
	RecordCacheError(ctx context.Context, attrs ...Attr)
	RecordCacheDuration(ctx context.Context, secs float64, attrs ...Attr)                     // magus.cache.duration histogram
	RecordGraphQuery(ctx context.Context, secs float64, attrs ...Attr)                        // magus.graph.query.duration histogram
	RecordRemoteOp(ctx context.Context, op RemoteOp)                                          // magus.cache.remote.* metrics
	StartSpan(ctx context.Context, name string, attrs ...Attr) (context.Context, func(error)) // end fn marks failure on non-nil error
	RecordTargetRun(ctx context.Context, secs float64, attrs ...Attr)                         // magus.target.runs + magus.target.duration
	RecordPoolAcquire(ctx context.Context, waitSecs float64, n int64)                         // magus.pool.wait.duration + inflight+n
	RecordPoolRelease(ctx context.Context, n int64)                                           // inflight-n
	Shutdown(ctx context.Context) error
}

// RemoteOp describes one completed remote cache backend operation for the
// magus.cache.remote.* instruments. Op is "get" or "put"; Outcome is "hit" or
// "miss" for a get, "stored" for a successful put, or "error" for any failure.
// Bytes is the entry payload transferred — 0 for a miss or an error before any
// data moved. A get hit and a put both carry a non-zero Bytes.
type RemoteOp struct {
	Op       string
	Outcome  string
	Duration float64 // wall-clock seconds
	Bytes    int64
}

// GraphObserver wraps p as a types.Observer; returns types.NoopObserver{} when p is nil or disabled.
func GraphObserver(ctx context.Context, p Provider) types.Observer {
	if p == nil || !p.Enabled() {
		return types.NoopObserver{}
	}
	return &otelGraphObserver{ctx: ctx, p: p}
}

type otelGraphObserver struct {
	ctx context.Context
	p   Provider
}

func (o *otelGraphObserver) OnBuild(s types.BuildStats) {
	o.p.RecordGraphQuery(
		o.ctx, s.Duration.Seconds(),
		Attr{Key: "op", Value: "build"},
	)
}

func (o *otelGraphObserver) OnQuery(e types.QueryEvent) {
	attrs := []Attr{
		{Key: "op", Value: e.Op},
	}
	if e.Strategy != "" {
		attrs = append(attrs, Attr{Key: "strategy", Value: e.Strategy})
	}
	o.p.RecordGraphQuery(o.ctx, e.Duration.Seconds(), attrs...)
}

func (*otelGraphObserver) OnError(_ error) {}

type providerKey struct{}

// WithProvider returns a copy of ctx carrying p. Retrieve with [FromContext].
func WithProvider(ctx context.Context, p Provider) context.Context {
	return context.WithValue(ctx, providerKey{}, p)
}

// FromContext returns the Provider stored by [WithProvider], or nil.
func FromContext(ctx context.Context) Provider {
	p, _ := ctx.Value(providerKey{}).(Provider)
	return p
}

// Attr is a key/value attribute attached to a metric.
type Attr struct {
	Key   string
	Value string
}

// CacheRunOptions returns [cache.RunOption] values that wire OnHit/OnMiss/OnError to the Provider.
// Returns nil when p is nil or disabled; callers can wire unconditionally.
func CacheRunOptions(ctx context.Context, p Provider) []cache.RunOption {
	if p == nil {
		return nil
	}
	return []cache.RunOption{
		cache.OnHit(func(r *cache.Result) {
			p.RecordCacheHit(ctx, Attr{Key: "outcome", Value: "hit"})
			p.RecordCacheDuration(
				ctx, r.Duration.Seconds(),
				Attr{Key: "outcome", Value: "hit"},
			)
		}),
		cache.OnMiss(func(r *cache.Result) {
			p.RecordCacheMiss(ctx, Attr{Key: "outcome", Value: "miss"})
			p.RecordCacheDuration(
				ctx, r.Duration.Seconds(),
				Attr{Key: "outcome", Value: "miss"},
			)
		}),
		cache.OnError(func(_ error) {
			p.RecordCacheError(ctx, Attr{Key: "outcome", Value: "error"})
		}),
	}
}

// TargetRunOptions returns [cache.RunOption] values that record per-target metrics (magus.project, spell, target,
// outcome, cache.hit) via the Provider. spellsOf maps project path → spell names; one row is emitted per spell.
// Returns nil when p is nil.
func TargetRunOptions(ctx context.Context, p Provider, spellsOf func(projectPath string) []string) []cache.RunOption {
	if p == nil {
		return nil
	}
	return []cache.RunOption{
		cache.OnResult(func(s *cache.Spec, r *cache.Result, err error) {
			outcome := "success"
			if err != nil {
				outcome = "error"
			}
			cacheHit := "false"
			if r.Hit {
				cacheHit = "true"
			}
			spells := spellsOf(s.ProjectPath)
			if len(spells) == 0 {
				spells = []string{""}
			}
			for _, spell := range spells {
				attrs := []Attr{
					{Key: "magus.project", Value: s.ProjectPath},
					{Key: "magus.spell", Value: spell},
					{Key: "magus.target", Value: s.Target},
					{Key: "outcome", Value: outcome},
					{Key: "cache.hit", Value: cacheHit},
				}
				p.RecordTargetRun(ctx, r.Duration.Seconds(), attrs...)
			}
		}),
	}
}
