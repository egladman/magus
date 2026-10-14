package otlp

import (
	"cmp"
	"context"
	"fmt"
	"strconv"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/types"
)

// reg threads the first instrument-creation error through a constructor, so each one reads as
// a flat list of instrument declarations instead of repeating the create-and-check dance.
type reg struct {
	m   metric.Meter
	err error
}

func (r *reg) join(name string, err error) {
	if err != nil && r.err == nil {
		r.err = fmt.Errorf("instrument %s: %w", name, err)
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

// mcpInstruments is the magus.mcp.tool.* family: how the MCP tools are exercised.
type mcpInstruments struct {
	inputSize  metric.Int64Histogram
	outputSize metric.Int64Histogram
	duration   metric.Float64Histogram
}

func newMCPInstruments(m metric.Meter) (mcpInstruments, error) {
	r := reg{m: m}
	mi := mcpInstruments{
		inputSize:  r.i64h("magus.mcp.tool.input.size", "Request payload bytes of an MCP tool call.", "By"),
		outputSize: r.i64h("magus.mcp.tool.output.size", "Response payload bytes of an MCP tool call.", "By"),
		duration:   r.f64h("magus.mcp.tool.duration", "Wall-clock duration of an MCP tool call, in seconds."),
	}
	return mi, r.err
}

func (p *otelProvider) RecordMCPCall(ctx context.Context, c observability.MCPCall) {
	tool := metric.WithAttributes(attribute.String("tool", c.Tool))
	p.mcp.inputSize.Record(ctx, c.InputBytes, tool)
	p.mcp.outputSize.Record(ctx, c.OutputBytes, tool)
	p.mcp.duration.Record(ctx, c.Duration, metric.WithAttributes(
		attribute.String("tool", c.Tool),
		attribute.String("outcome", string(c.Outcome)),
	))
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
	for access, n := range r.Access {
		if n == 0 {
			continue
		}
		p.sandbox.rules.Add(ctx, n, metric.WithAttributes(
			attribute.String("access", access.String()),
			attribute.String("scope", r.Scope),
		))
	}

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
	sessionPoolEvents   metric.Int64Counter
	sessionPoolIdle     metric.Int64UpDownCounter
	sessionWarmDuration metric.Float64Histogram
	importDuration      metric.Float64Histogram
	spellResolve        metric.Float64Histogram
	spellBuiltinsWarm   metric.Float64Histogram
	jitRuns             metric.Int64Counter
	vmFaults            metric.Int64Counter
}

func newBuzzInstruments(m metric.Meter) (buzzInstruments, error) {
	r := reg{m: m}
	bi := buzzInstruments{
		execDuration:        r.f64h("magus.buzz.exec.duration", "Wall-clock duration of a Buzz script execution, in seconds."),
		compileDuration:     r.f64h("magus.buzz.compile.duration", "Wall-clock duration of a Buzz compile phase, in seconds."),
		hostCallDuration:    r.f64h("magus.buzz.host.call.duration", "Wall-clock duration of a Buzz call into a host callable, in seconds."),
		sessionPoolEvents:   r.i64c("magus.buzz.session.pool.events", "Buzz session-pool checkouts and releases, by event.", "{event}"),
		sessionPoolIdle:     r.i64ud("magus.buzz.session.pool.idle", "Number of idle Buzz sessions currently pooled.", "{session}"),
		sessionWarmDuration: r.f64h("magus.buzz.session.warm.duration", "Wall-clock duration of warming a Buzz session, in seconds."),
		importDuration:      r.f64h("magus.buzz.import.duration", "Wall-clock duration of resolving a Buzz import, in seconds."),
		spellResolve:        r.f64h("magus.buzz.spell.resolve.duration", "Wall-clock duration of resolving a spell, in seconds."),
		spellBuiltinsWarm:   r.f64h("magus.buzz.spell.builtins.warm", "Wall-clock duration of warming a spell's builtins, in seconds."),
		jitRuns:             r.i64c("magus.buzz.jit.runs", "Number of JIT-compiled entry executions.", "{entry}"),
		vmFaults:            r.i64c("magus.buzz.vm.faults", "Number of Buzz VM faults.", "{fault}"),
	}
	return bi, r.err
}

func (p *otelProvider) RecordBuzzExec(ctx context.Context, e observability.BuzzExec) {
	p.buzz.execDuration.Record(ctx, e.Duration, metric.WithAttributes(
		attribute.String("mode", e.Mode),
		attribute.String("outcome", string(e.Outcome)),
	))
}

func (p *otelProvider) RecordBuzzCompile(ctx context.Context, c observability.BuzzCompile) {
	p.buzz.compileDuration.Record(ctx, c.Duration, metric.WithAttributes(
		attribute.String("phase", c.Phase),
		attribute.String("mode", c.Mode),
	))
}

func (p *otelProvider) RecordBuzzHostCall(ctx context.Context, c observability.BuzzHostCall) {
	p.buzz.hostCallDuration.Record(ctx, c.Duration, metric.WithAttributes(
		attribute.String("callable", c.Callable),
		attribute.String("outcome", string(c.Outcome)),
	))
}

// RecordBuzzSessionPool counts ev and moves the idle gauge by what ev did to the idle set: a
// reuse took one session out of it and a return put one back; a cold checkout and an eviction
// leave it alone.
func (p *otelProvider) RecordBuzzSessionPool(ctx context.Context, ev observability.SessionPoolEvent) {
	p.buzz.sessionPoolEvents.Add(ctx, 1, metric.WithAttributes(attribute.String("event", string(ev))))
	switch ev {
	case observability.PoolReused:
		p.buzz.sessionPoolIdle.Add(ctx, -1)
	case observability.PoolReturned:
		p.buzz.sessionPoolIdle.Add(ctx, 1)
	}
}

func (p *otelProvider) RecordBuzzSessionWarm(ctx context.Context, secs float64, source string) {
	p.buzz.sessionWarmDuration.Record(ctx, secs, metric.WithAttributes(attribute.String("source", source)))
}

func (p *otelProvider) RecordBuzzImport(ctx context.Context, i observability.BuzzImport) {
	p.buzz.importDuration.Record(ctx, i.Duration, metric.WithAttributes(
		attribute.String("kind", i.Kind),
		attribute.String("outcome", string(i.Outcome)),
	))
}

func (p *otelProvider) RecordBuzzSpellResolve(ctx context.Context, r observability.BuzzSpellResolve) {
	p.buzz.spellResolve.Record(ctx, r.Duration, metric.WithAttributes(
		attribute.String("spell", r.Spell),
		attribute.String("builtin", strconv.FormatBool(r.Builtin)),
	))
}

func (p *otelProvider) RecordBuzzSpellBuiltinsWarm(ctx context.Context, secs float64, spell string) {
	p.buzz.spellBuiltinsWarm.Record(ctx, secs, metric.WithAttributes(attribute.String("spell", spell)))
}

func (p *otelProvider) RecordBuzzJITRun(ctx context.Context) {
	p.buzz.jitRuns.Add(ctx, 1)
}

func (p *otelProvider) RecordBuzzVMFault(ctx context.Context, kind string) {
	p.buzz.vmFaults.Add(ctx, 1, metric.WithAttributes(attribute.String("kind", kind)))
}

// agentInstruments is the family for leases, attention and review: magus.lease.*, magus.attention.* and
// magus.review.*, the three places a fleet of agents and the people working with them meet.
// Every producer here runs in the server, which is what makes them collectable at all; the
// CLI halves of the same work (raising an attention request, the guard grading a write)
// live in one-shot processes and are recorded to the activity trail instead.
//
// The attributes are deliberately thin. A lease id, an attention message, a remark body
// and an agent's self-declared host label are all unbounded, and none of them is here.
type agentInstruments struct {
	leaseRegistrations   metric.Int64Counter
	attentionDisposition metric.Float64Histogram
	reviewRemarks        metric.Int64Counter
	reviewPublishes      metric.Int64Counter
	// reviewMergedWhileReading carries no attributes: how long a reader had been reading is
	// the whole fact, and a review id or repository name is unbounded.
	reviewMergedWhileReading metric.Float64Histogram
}

func newAgentInstruments(m metric.Meter) (agentInstruments, error) {
	r := reg{m: m}
	ai := agentInstruments{
		leaseRegistrations:       r.i64c("magus.lease.registrations", "Lease base registrations, by how the base a worker reported compares to the checkpoint it was handed.", "{registration}"),
		attentionDisposition:     r.f64h("magus.attention.disposition.duration", "Wall-clock time an attention request waited from raised to disposed, in seconds."),
		reviewRemarks:            r.i64c("magus.review.remarks", "Review remarks drafted on a change.", "{remark}"),
		reviewPublishes:          r.i64c("magus.review.publishes", "Review publishes, by the verdict that landed.", "{publish}"),
		reviewMergedWhileReading: r.f64h("magus.review.merged_while_reading.duration", "Time from the reader marking a review as being read to it merging, in seconds."),
	}
	return ai, r.err
}

func (p *otelProvider) RecordLeaseRegistration(ctx context.Context, verdict string) {
	p.agent.leaseRegistrations.Add(ctx, 1, metric.WithAttributes(attribute.String("verdict", verdict)))
}

func (p *otelProvider) RecordAttentionDisposition(ctx context.Context, secs float64, sev string) {
	p.agent.attentionDisposition.Record(ctx, secs, metric.WithAttributes(attribute.String("severity", sev)))
}

func (p *otelProvider) RecordReviewRemark(ctx context.Context, author string) {
	p.agent.reviewRemarks.Add(ctx, 1, metric.WithAttributes(attribute.String("author", author)))
}

// The published verdict carries no author attribute, on purpose. Which door a remark came
// through is a fact about the transport; how a change was JUDGED, split by who wrote it, is the
// first half of a threshold that blocks work by author kind. magus observes and does not gate.
func (p *otelProvider) RecordReviewPublish(ctx context.Context, verdict string, downgraded bool) {
	p.agent.reviewPublishes.Add(ctx, 1, metric.WithAttributes(
		attribute.String("verdict", verdict),
		attribute.Bool("downgraded", downgraded),
	))
}

func (p *otelProvider) RecordReviewMergedWhileReading(ctx context.Context, secs float64) {
	p.agent.reviewMergedWhileReading.Record(ctx, secs)
}

// agentCostInstruments is the magus.agent.* family: what an agent session's calls cost in
// context. thread, model, class, billing, profile and decision are bounded enums or model
// names; a session id, a command and a path are not here.
type agentCostInstruments struct {
	contextTokens metric.Int64Histogram
	delegable     metric.Int64Counter
	billing       metric.Int64Counter
	shadow        metric.Int64Counter
}

func newAgentCostInstruments(m metric.Meter) (agentCostInstruments, error) {
	r := reg{m: m}
	ci := agentCostInstruments{
		contextTokens: r.i64h("magus.agent.context.tokens", "Context size a caller reported on a tool call, in tokens.", "{token}"),
		delegable:     r.i64c("magus.agent.delegable.chars", "Result characters of tool calls a subagent could have made in the caller's place.", "{char}"),
		billing:       r.i64c("magus.agent.billing", "Tool calls, by the billing mode in effect.", "{call}"),
		shadow:        r.i64c("magus.agent.shadow.verdicts", "Tool calls, by what each guard profile would have decided.", "{call}"),
	}
	return ci, r.err
}

func (p *otelProvider) RecordAgentCall(ctx context.Context, c observability.AgentCall) {
	if c.ContextTokens > 0 {
		p.agentCost.contextTokens.Record(ctx, c.ContextTokens, metric.WithAttributes(
			attribute.String("thread", string(cmp.Or(c.Thread, types.AgentThreadUnknown))),
			attribute.String("model", cmp.Or(c.ContextModel, "unknown")),
		))
	}
	if c.DelegableClass != "" && c.ResultChars != nil && *c.ResultChars > 0 {
		p.agentCost.delegable.Add(ctx, int64(*c.ResultChars), metric.WithAttributes(attribute.String("class", string(c.DelegableClass))))
	}
	if c.Billing != "" {
		p.agentCost.billing.Add(ctx, 1, metric.WithAttributes(attribute.String("mode", string(c.Billing))))
	}
	for profile, decision := range c.Shadow {
		p.agentCost.shadow.Add(ctx, 1, metric.WithAttributes(
			attribute.String("profile", profile.String()),
			attribute.String("decision", string(cmp.Or(decision, types.GuardAllow))),
		))
	}
}
