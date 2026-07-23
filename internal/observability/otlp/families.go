package otlp

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/egladman/magus/internal/observability"
)

// reg is a small registration helper that threads the first instrument-creation error through
// a family constructor, so each constructor reads as a flat list of instrument declarations
// instead of repeating the create-and-check dance for every one. The core provider instruments
// (cache/target/pool/remote) predate this helper and keep their explicit per-instrument checks.
type reg struct {
	m   metric.Meter
	err error
}

func (r *reg) join(name string, err error) {
	if err != nil && r.err == nil {
		r.err = fmt.Errorf("observability: %s: %w", name, err)
	}
}

func (r *reg) i64c(name, desc, unit string) metric.Int64Counter {
	c, err := r.m.Int64Counter(name, metric.WithDescription(desc), metric.WithUnit(unit))
	r.join(name, err)
	return c
}

func (r *reg) i64ud(name, desc, unit string) metric.Int64UpDownCounter {
	c, err := r.m.Int64UpDownCounter(name, metric.WithDescription(desc), metric.WithUnit(unit))
	r.join(name, err)
	return c
}

func (r *reg) i64h(name, desc, unit string) metric.Int64Histogram {
	h, err := r.m.Int64Histogram(name, metric.WithDescription(desc), metric.WithUnit(unit))
	r.join(name, err)
	return h
}

func (r *reg) f64h(name, desc string) metric.Float64Histogram {
	// Every duration histogram is in seconds; the unit is not a per-call parameter.
	h, err := r.m.Float64Histogram(name, metric.WithDescription(desc), metric.WithUnit("s"))
	r.join(name, err)
	return h
}

// mcpInstruments is the magus.mcp.tool.* family: how the MCP surface is exercised.
type mcpInstruments struct {
	calls      metric.Int64Counter
	inputSize  metric.Int64Histogram
	outputSize metric.Int64Histogram
	duration   metric.Float64Histogram
}

func newMCPInstruments(m metric.Meter) (mcpInstruments, error) {
	r := reg{m: m}
	mi := mcpInstruments{
		calls:      r.i64c("magus.mcp.tool.calls", "Number of MCP tool calls.", "{call}"),
		inputSize:  r.i64h("magus.mcp.tool.input.size", "Request payload bytes of an MCP tool call.", "By"),
		outputSize: r.i64h("magus.mcp.tool.output.size", "Response payload bytes of an MCP tool call.", "By"),
		duration:   r.f64h("magus.mcp.tool.duration", "Wall-clock duration of an MCP tool call, in seconds."),
	}
	return mi, r.err
}

func (p *otelProvider) RecordMCPCall(ctx context.Context, c observability.MCPCall) {
	tool := metric.WithAttributes(attribute.String("tool", c.Tool))
	toolOutcome := metric.WithAttributes(
		attribute.String("tool", c.Tool),
		attribute.String("outcome", c.Outcome),
	)
	p.mcp.calls.Add(ctx, 1, toolOutcome)
	p.mcp.inputSize.Record(ctx, c.InputBytes, tool)
	p.mcp.outputSize.Record(ctx, c.OutputBytes, tool)
	p.mcp.duration.Record(ctx, c.Duration, toolOutcome)
}

// sandboxInstruments is the magus.sandbox.* filesystem family. Network sandboxing is out of
// scope by design, so there is deliberately no magus.sandbox.net.* here.
type sandboxInstruments struct {
	applyDuration metric.Float64Histogram
	rules         metric.Int64Counter
	envRules      metric.Int64Counter
	checks        metric.Int64Counter
	envDropped    metric.Int64Counter
}

func newSandboxInstruments(m metric.Meter) (sandboxInstruments, error) {
	r := reg{m: m}
	si := sandboxInstruments{
		applyDuration: r.f64h("magus.sandbox.apply.duration", "Wall-clock duration of applying a filesystem sandbox, in seconds."),
		rules:         r.i64c("magus.sandbox.rules", "Filesystem access rules a sandbox was built from.", "{rule}"),
		envRules:      r.i64c("magus.sandbox.env.rules", "Environment allow-rules a sandbox was built from.", "{rule}"),
		checks:        r.i64c("magus.sandbox.checks", "Filesystem access checks resolved against a sandbox.", "{check}"),
		envDropped:    r.i64c("magus.sandbox.env.dropped", "Environment variables dropped by a sandbox.", "{var}"),
	}
	return si, r.err
}

func (p *otelProvider) RecordSandboxApply(ctx context.Context, secs float64, outcome, scope string) {
	p.sandbox.applyDuration.Record(ctx, secs, metric.WithAttributes(
		attribute.String("outcome", outcome),
		attribute.String("scope", scope),
	))
}

func (p *otelProvider) RecordSandboxRules(ctx context.Context, r observability.SandboxRules) {
	add := func(access string, n int64) {
		if n == 0 {
			return
		}
		p.sandbox.rules.Add(ctx, n, metric.WithAttributes(
			attribute.String("access", access),
			attribute.String("scope", r.Scope),
		))
	}
	add("read", r.Read)
	add("write", r.Write)
	add("exec", r.Exec)

	addEnv := func(kind string, n int64) {
		if n == 0 {
			return
		}
		p.sandbox.envRules.Add(ctx, n, metric.WithAttributes(attribute.String("kind", kind)))
	}
	addEnv("exact", r.EnvExact)
	addEnv("glob", r.EnvGlob)
}

func (p *otelProvider) RecordSandboxCheck(ctx context.Context, access, decision, project string) {
	p.sandbox.checks.Add(ctx, 1, metric.WithAttributes(
		attribute.String("access", access),
		attribute.String("decision", decision),
		attribute.String("magus.project", project),
	))
}

func (p *otelProvider) RecordSandboxEnvDropped(ctx context.Context, project string, n int64) {
	p.sandbox.envDropped.Add(ctx, n, metric.WithAttributes(attribute.String("magus.project", project)))
}

// buzzInstruments is the magus.buzz.* family. Names use gopherbuzz vocabulary (chunk/fiber/
// session/module/JIT); the native-boundary metric is magus.buzz.host.call.*. There is
// deliberately no per-instruction/opcode instrument (it sits on the interpreter hot path).
type buzzInstruments struct {
	execDuration        metric.Float64Histogram
	compileDuration     metric.Float64Histogram
	hostCallDuration    metric.Float64Histogram
	hostCallCount       metric.Int64Counter
	sessionPoolReuse    metric.Int64Counter
	sessionPoolIdle     metric.Int64UpDownCounter
	sessionPoolEviction metric.Int64Counter
	sessionWarmDuration metric.Float64Histogram
	importDuration      metric.Float64Histogram
	spellResolve        metric.Float64Histogram
	spellBuiltinsWarm   metric.Float64Histogram
	spellBuiltinsCount  metric.Int64Counter
	jitRuns             metric.Int64Counter
	vmFaults            metric.Int64Counter
}

func newBuzzInstruments(m metric.Meter) (buzzInstruments, error) {
	r := reg{m: m}
	bi := buzzInstruments{
		execDuration:        r.f64h("magus.buzz.exec.duration", "Wall-clock duration of a Buzz script execution, in seconds."),
		compileDuration:     r.f64h("magus.buzz.compile.duration", "Wall-clock duration of a Buzz compile phase, in seconds."),
		hostCallDuration:    r.f64h("magus.buzz.host.call.duration", "Wall-clock duration of a Buzz call into a host callable, in seconds."),
		hostCallCount:       r.i64c("magus.buzz.host.call.count", "Number of Buzz calls across the native boundary.", "{call}"),
		sessionPoolReuse:    r.i64c("magus.buzz.session.pool.reuse", "Buzz session-pool acquires, by whether an idle session was reused.", "{acquire}"),
		sessionPoolIdle:     r.i64ud("magus.buzz.session.pool.idle", "Number of idle Buzz sessions currently pooled.", "{session}"),
		sessionPoolEviction: r.i64c("magus.buzz.session.pool.evictions", "Buzz sessions evicted from the pool.", "{session}"),
		sessionWarmDuration: r.f64h("magus.buzz.session.warm.duration", "Wall-clock duration of warming a Buzz session, in seconds."),
		importDuration:      r.f64h("magus.buzz.import.duration", "Wall-clock duration of resolving a Buzz import, in seconds."),
		spellResolve:        r.f64h("magus.buzz.spell.resolve.duration", "Wall-clock duration of resolving a spell, in seconds."),
		spellBuiltinsWarm:   r.f64h("magus.buzz.spell.builtins.warm", "Wall-clock duration of warming a spell's builtins, in seconds."),
		spellBuiltinsCount:  r.i64c("magus.buzz.spell.builtins.count", "Number of spell builtin warms.", "{spell}"),
		jitRuns:             r.i64c("magus.buzz.jit.runs", "Number of JIT-compiled entry executions.", "{entry}"),
		vmFaults:            r.i64c("magus.buzz.vm.faults", "Number of Buzz VM faults.", "{fault}"),
	}
	return bi, r.err
}

func (p *otelProvider) RecordBuzzExec(ctx context.Context, secs float64, mode, outcome string) {
	p.buzz.execDuration.Record(ctx, secs, metric.WithAttributes(
		attribute.String("mode", mode),
		attribute.String("outcome", outcome),
	))
}

func (p *otelProvider) RecordBuzzCompile(ctx context.Context, secs float64, phase, mode string) {
	p.buzz.compileDuration.Record(ctx, secs, metric.WithAttributes(
		attribute.String("phase", phase),
		attribute.String("mode", mode),
	))
}

func (p *otelProvider) RecordBuzzHostCall(ctx context.Context, c observability.BuzzHostCall) {
	p.buzz.hostCallDuration.Record(ctx, c.Duration, metric.WithAttributes(attribute.String("callable", c.Callable)))
	p.buzz.hostCallCount.Add(ctx, 1, metric.WithAttributes(
		attribute.String("callable", c.Callable),
		attribute.String("outcome", c.Outcome),
	))
}

func (p *otelProvider) RecordBuzzSessionReuse(ctx context.Context, outcome string) {
	p.buzz.sessionPoolReuse.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
}

func (p *otelProvider) RecordBuzzSessionIdle(ctx context.Context, delta int64) {
	p.buzz.sessionPoolIdle.Add(ctx, delta)
}

func (p *otelProvider) RecordBuzzSessionEviction(ctx context.Context, source string) {
	p.buzz.sessionPoolEviction.Add(ctx, 1, metric.WithAttributes(attribute.String("source", source)))
}

func (p *otelProvider) RecordBuzzSessionWarm(ctx context.Context, secs float64, source string) {
	p.buzz.sessionWarmDuration.Record(ctx, secs, metric.WithAttributes(attribute.String("source", source)))
}

func (p *otelProvider) RecordBuzzImport(ctx context.Context, secs float64, kind, outcome string) {
	p.buzz.importDuration.Record(ctx, secs, metric.WithAttributes(
		attribute.String("kind", kind),
		attribute.String("outcome", outcome),
	))
}

func (p *otelProvider) RecordBuzzSpellResolve(ctx context.Context, secs float64, spell, builtin string) {
	p.buzz.spellResolve.Record(ctx, secs, metric.WithAttributes(
		attribute.String("spell", spell),
		attribute.String("builtin", builtin),
	))
}

func (p *otelProvider) RecordBuzzSpellBuiltinsWarm(ctx context.Context, secs float64, spell string) {
	kv := metric.WithAttributes(attribute.String("spell", spell))
	p.buzz.spellBuiltinsWarm.Record(ctx, secs, kv)
	p.buzz.spellBuiltinsCount.Add(ctx, 1, kv)
}

func (p *otelProvider) RecordBuzzJITRun(ctx context.Context) {
	p.buzz.jitRuns.Add(ctx, 1)
}

func (p *otelProvider) RecordBuzzVMFault(ctx context.Context, kind string) {
	p.buzz.vmFaults.Add(ctx, 1, metric.WithAttributes(attribute.String("kind", kind)))
}
