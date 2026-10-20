// Package observability provides OpenTelemetry instrumentation for magus.
// Telemetry is OFF by default; set telemetry.enabled=true in magus.yaml to export.
package observability

import (
	"context"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/sandbox/filesystem"
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
	// LocalCollect builds an always-on in-process metrics collector even when Enabled is false
	// (no external export), so the server can serve OTLP snapshots to the /dashboard. The CLI
	// leaves this false to keep one-shot invocations a true no-op.
	LocalCollect bool
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

// Provider is the OTel runtime API. All methods are concurrency-safe; all are no-ops when Enabled=false.
// A fake embeds [Nop] and defines only the methods it records; a consumer that calls one or two
// methods declares its own interface with just those.
type Provider interface {
	Enabled() bool
	RecordCacheRead(ctx context.Context, result CacheResult, attrs ...Attr)                   // magus.cache.reads
	RecordCacheDuration(ctx context.Context, secs float64, result CacheResult)                // magus.cache.duration histogram
	RecordCacheSaved(ctx context.Context, secs float64)                                       // magus.cache.saved.duration histogram
	RecordGraphQuery(ctx context.Context, secs float64, attrs ...Attr)                        // magus.graph.query.duration histogram
	RecordRemoteOp(ctx context.Context, op RemoteOp)                                          // magus.cache.remote.* metrics
	StartSpan(ctx context.Context, name string, attrs ...Attr) (context.Context, func(error)) // end fn marks failure on non-nil error
	RecordTargetRun(ctx context.Context, secs float64, attrs ...Attr)                         // magus.target.duration
	RecordPoolWait(ctx context.Context, secs float64)                                         // magus.pool.wait.duration
	RecordPoolSlots(ctx context.Context, state PoolSlotState, delta int64)                    // magus.pool.slots.{queued,running} += delta

	// MCP tool call family: magus.mcp.tool.*.
	RecordMCPCall(ctx context.Context, c MCPCall)

	// Agent cost family: magus.agent.*.
	RecordAgentCall(ctx context.Context, c AgentCall)

	// Filesystem sandbox families: magus.sandbox.*.
	RecordSandboxApply(ctx context.Context, secs float64, outcome, scope string) // magus.sandbox.apply.duration
	RecordSandboxRules(ctx context.Context, r SandboxRules)                      // magus.sandbox.rules + magus.sandbox.env.rules
	RecordSandboxCheck(ctx context.Context, access, decision, project string)    // magus.sandbox.checks
	RecordSandboxEnvDropped(ctx context.Context, project string, n int64)        // magus.sandbox.env.dropped

	// Buzz language families: magus.buzz.*.
	RecordBuzzExec(ctx context.Context, e BuzzExec)                              // magus.buzz.exec.duration
	RecordBuzzCompile(ctx context.Context, c BuzzCompile)                        // magus.buzz.compile.duration
	RecordBuzzHostCall(ctx context.Context, c BuzzHostCall)                      // magus.buzz.host.call.duration
	RecordBuzzSessionPool(ctx context.Context, ev SessionPoolEvent)              // magus.buzz.session.pool.{events,idle}
	RecordBuzzSessionWarm(ctx context.Context, secs float64, source string)      // magus.buzz.session.warm.duration
	RecordBuzzImport(ctx context.Context, i BuzzImport)                          // magus.buzz.import.duration
	RecordBuzzSpellResolve(ctx context.Context, r BuzzSpellResolve)              // magus.buzz.spell.resolve.duration
	RecordBuzzSpellBuiltinsWarm(ctx context.Context, secs float64, spell string) // magus.buzz.spell.builtins.warm
	RecordBuzzJITRun(ctx context.Context)                                        // magus.buzz.jit.runs
	RecordBuzzVMFault(ctx context.Context, kind string)                          // magus.buzz.vm.faults

	// Lease, attention and review families: magus.lease.*, magus.attention.*, magus.review.*. Every
	// attribute here is a bounded enum the caller has already validated; an id, a path, a
	// lease label or a remark body must never reach one.
	RecordLeaseRegistration(ctx context.Context, verdict string)              // magus.lease.registrations
	RecordAttentionDisposition(ctx context.Context, secs float64, sev string) // magus.attention.disposition.duration
	RecordReviewRemark(ctx context.Context, author string)                    // magus.review.remarks
	RecordReviewPublish(ctx context.Context, verdict string, downgraded bool) // magus.review.publishes
	// RecordReviewMergedWhileReading records how long the reader had been reading a review
	// that merged under them, in seconds (magus.review.merged_while_reading.duration).
	RecordReviewMergedWhileReading(ctx context.Context, secs float64)
	// Snapshot returns the current metrics as standard OTLP protobuf, or (nil, nil) when this
	// provider is not collecting locally. OTLP is the export format for real monitoring
	// backends; it is never put on the dashboard's wire.
	Snapshot(ctx context.Context) ([]byte, error)
	Shutdown(ctx context.Context) error
}

// CacheResult is how one Cache.Run ended: the result attribute on magus.cache.reads and
// magus.cache.duration. The three are mutually exclusive.
type CacheResult string

const (
	CacheHit   CacheResult = "hit"   // replayed from the local store
	CacheMiss  CacheResult = "miss"  // no entry; the step ran and succeeded
	CacheError CacheResult = "error" // the step failed or was refused
)

// Outcome is whether a recorded call succeeded: the outcome attribute every family that
// reports success or failure shares, so one filter on it reads across instruments.
type Outcome string

const (
	OutcomeOK    Outcome = "ok"
	OutcomeError Outcome = "error"
)

// RemoteOutcome is what a remote cache backend answered. A get reports [RemoteHit],
// [RemoteMiss] or [RemoteError]; a put reports [RemoteStored], [RemoteExists] or
// [RemoteError].
type RemoteOutcome string

const (
	RemoteHit    RemoteOutcome = "hit"    // get returned an entry
	RemoteMiss   RemoteOutcome = "miss"   // get found no entry
	RemoteStored RemoteOutcome = "stored" // put stored the entry
	RemoteExists RemoteOutcome = "exists" // put found the key already held by another writer
	RemoteError  RemoteOutcome = "error"  // the operation failed
)

// RemoteOp describes one completed remote cache backend operation for the
// magus.cache.remote.* instruments. Method is "get" or "put". Bytes is the entry
// payload transferred: 0 for a miss or an error before any data moved.
type RemoteOp struct {
	Method   string
	Outcome  RemoteOutcome
	Duration float64 // wall-clock seconds
	Bytes    int64
}

// PoolSlotState names one of the concurrency pool's slot gauges.
type PoolSlotState string

const (
	PoolQueued  PoolSlotState = "queued"  // callers waiting to acquire slots
	PoolRunning PoolSlotState = "running" // slots held by running steps
)

// SessionPoolEvent is one Buzz session-pool lifecycle event, the event attribute on
// magus.buzz.session.pool.events. A reuse drains one idle session and a return adds one,
// which is how magus.buzz.session.pool.idle moves.
type SessionPoolEvent string

const (
	PoolReused   SessionPoolEvent = "reused"   // a checkout took an idle warm session
	PoolCold     SessionPoolEvent = "cold"     // a checkout found nothing idle and warms a fresh session
	PoolEvicted  SessionPoolEvent = "evicted"  // a finished session was closed: the pool was full or closed
	PoolReturned SessionPoolEvent = "returned" // a finished session went back to the idle set
)

// MCPCall describes one completed MCP tool call for the magus.mcp.tool.* family.
// InputBytes/OutputBytes are the request/response payload sizes; Duration is wall-clock
// seconds.
type MCPCall struct {
	Tool        string
	Outcome     Outcome
	InputBytes  int64
	OutputBytes int64
	Duration    float64
}

// AgentCall describes the cost facts recorded for one agent tool call, for the magus.agent.*
// family. Every field is the caller's and optional: a zero ContextTokens, empty Billing, or
// an empty DelegableClass or ResultChars of nil or 0 records nothing for that instrument,
// and an empty Thread or ContextModel reads as "unknown". Shadow maps each guard profile
// to what it would have decided.
type AgentCall struct {
	types.AgentFacts
}

// SandboxRules describes the allow-rules one sandbox was built from, for the
// magus.sandbox.rules and magus.sandbox.env.rules families. Access counts filesystem rules
// by the access each grants (a rule granting two counts under both); EnvExact/EnvGlob
// count environment allow-rules by match kind; Scope is the sandbox scope attribute (e.g.
// "workspace" or "target").
type SandboxRules struct {
	Access   map[filesystem.Access]int64
	EnvExact int64
	EnvGlob  int64
	Scope    string
}

// BuzzExec describes one Buzz execution for magus.buzz.exec.duration. Mode is the
// execution mode (magusfile, spell, repl); Duration is wall-clock seconds.
type BuzzExec struct {
	Mode     string
	Outcome  Outcome
	Duration float64
}

// BuzzCompile describes one compile phase for magus.buzz.compile.duration. Phase is
// parse, check or compile; Duration is wall-clock seconds.
type BuzzCompile struct {
	Phase    string
	Mode     string
	Duration float64
}

// BuzzImport describes one resolved import for magus.buzz.import.duration. Kind is how
// the import resolved (bound, native, declarations, resolver, file, not-found); Duration
// is wall-clock seconds.
type BuzzImport struct {
	Kind     string
	Outcome  Outcome
	Duration float64
}

// BuzzSpellResolve describes one spell resolve for magus.buzz.spell.resolve.duration.
// Builtin is whether the spell ships with magus; Duration is wall-clock seconds.
type BuzzSpellResolve struct {
	Spell    string
	Builtin  bool
	Duration float64
}

// BuzzHostCall describes one native-boundary call from Buzz into a host callable, for the
// magus.buzz.host.call.* family. Callable names the host function; Duration is
// wall-clock seconds.
type BuzzHostCall struct {
	Callable string
	Outcome  Outcome
	Duration float64
}

// Nop is a Provider that records nothing and reports itself disabled. A fake embeds it
// and defines only the methods it records.
type Nop struct{}

func (Nop) Enabled() bool                                             { return false }
func (Nop) RecordCacheRead(context.Context, CacheResult, ...Attr)     {}
func (Nop) RecordCacheDuration(context.Context, float64, CacheResult) {}
func (Nop) RecordCacheSaved(context.Context, float64)                 {}
func (Nop) RecordGraphQuery(context.Context, float64, ...Attr)        {}
func (Nop) RecordRemoteOp(context.Context, RemoteOp)                  {}
func (Nop) StartSpan(ctx context.Context, _ string, _ ...Attr) (context.Context, func(error)) {
	return ctx, func(error) {}
}
func (Nop) RecordTargetRun(context.Context, float64, ...Attr)            {}
func (Nop) RecordPoolWait(context.Context, float64)                      {}
func (Nop) RecordPoolSlots(context.Context, PoolSlotState, int64)        {}
func (Nop) RecordMCPCall(context.Context, MCPCall)                       {}
func (Nop) RecordAgentCall(context.Context, AgentCall)                   {}
func (Nop) RecordSandboxApply(context.Context, float64, string, string)  {}
func (Nop) RecordSandboxRules(context.Context, SandboxRules)             {}
func (Nop) RecordSandboxCheck(context.Context, string, string, string)   {}
func (Nop) RecordSandboxEnvDropped(context.Context, string, int64)       {}
func (Nop) RecordBuzzExec(context.Context, BuzzExec)                     {}
func (Nop) RecordBuzzCompile(context.Context, BuzzCompile)               {}
func (Nop) RecordBuzzHostCall(context.Context, BuzzHostCall)             {}
func (Nop) RecordBuzzSessionPool(context.Context, SessionPoolEvent)      {}
func (Nop) RecordBuzzSessionWarm(context.Context, float64, string)       {}
func (Nop) RecordBuzzImport(context.Context, BuzzImport)                 {}
func (Nop) RecordBuzzSpellResolve(context.Context, BuzzSpellResolve)     {}
func (Nop) RecordBuzzSpellBuiltinsWarm(context.Context, float64, string) {}
func (Nop) RecordBuzzJITRun(context.Context)                             {}
func (Nop) RecordBuzzVMFault(context.Context, string)                    {}
func (Nop) RecordLeaseRegistration(context.Context, string)              {}
func (Nop) RecordAttentionDisposition(context.Context, float64, string)  {}
func (Nop) RecordReviewRemark(context.Context, string)                   {}
func (Nop) RecordReviewPublish(context.Context, string, bool)            {}
func (Nop) RecordReviewMergedWhileReading(context.Context, float64)      {}
func (Nop) Snapshot(context.Context) ([]byte, error)                     { return nil, nil }
func (Nop) Shutdown(context.Context) error                               { return nil }

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

// Observe records a finished build or query; a failed one has no duration worth a point.
func (o *otelGraphObserver) Observe(e types.GraphEvent) {
	if e.Err != nil {
		return
	}
	attrs := []Attr{{Key: "op", Value: e.Op}}
	if e.Strategy != "" {
		attrs = append(attrs, Attr{Key: "strategy", Value: e.Strategy})
	}
	o.p.RecordGraphQuery(o.ctx, e.Duration.Seconds(), attrs...)
}

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

// CacheRunOptions returns the [cache.RunOption] that records every Cache.Run's result to the
// Provider. Returns nil when p is nil; callers can wire unconditionally.
func CacheRunOptions(ctx context.Context, p Provider) []cache.RunOption {
	if p == nil {
		return nil
	}
	return []cache.RunOption{
		cache.OnResult(func(_ *cache.Step, r *cache.Result, err error) {
			if err != nil {
				p.RecordCacheRead(ctx, CacheError)
				return
			}
			result := CacheMiss
			if r.Hit {
				result = CacheHit
			}
			p.RecordCacheRead(ctx, result)
			p.RecordCacheDuration(ctx, r.Duration.Seconds(), result)
			// An entry written before the manifest carried a duration reports zero saved,
			// which is an absence of measurement rather than a hit that saved nothing;
			// recording it would drag the distribution down with points nobody measured.
			if r.Hit && r.Saved > 0 {
				p.RecordCacheSaved(ctx, r.Saved.Seconds())
			}
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
		cache.OnResult(func(s *cache.Step, r *cache.Result, err error) {
			outcome := OutcomeOK
			if err != nil {
				outcome = OutcomeError
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
					{Key: "outcome", Value: string(outcome)},
					{Key: "cache.hit", Value: cacheHit},
				}
				p.RecordTargetRun(ctx, r.Duration.Seconds(), attrs...)
			}
		}),
	}
}
