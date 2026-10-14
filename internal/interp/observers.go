package interp

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/egladman/magus/internal/observability"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
)

// This file translates the gopherbuzz instrumentation hooks (CompileObserver,
// PoolObserver, FaultHook, WrapDirect's DirectObserver) into the observability
// spine's magus.buzz.* Record calls. Every attachment is nil-gated on an active
// provider: a non-server one-shot run reaches providerFrom == nil, no observer is
// set, and the interpreter runs byte-for-byte unobserved with no timing on the hot
// path. The terminology (mode, host.call, phase, kind) is fixed by the spine.

// Buzz execution modes, the "mode" attribute on magus.buzz.exec/compile.
const (
	ModeMagusfile = "magusfile"
	ModeSpell     = "spell"
	ModeRepl      = "repl"
)

// outcomeOf labels a completed Buzz call by its error.
func outcomeOf(err error) observability.Outcome {
	if err != nil {
		return observability.OutcomeError
	}
	return observability.OutcomeOK
}

// providerFrom returns ctx's telemetry provider only when it is enabled. A nil
// return is the signal to attach nothing, keeping the hot path free of observer
// cost on ordinary one-shot runs.
func providerFrom(ctx context.Context) observability.Provider {
	p := observability.FromContext(ctx)
	if p == nil || !p.Enabled() {
		return nil
	}
	return p
}

// lastJITRuns tracks the vm.JITRunCount() value observed after the previous timed
// execution so each exec records only its own delta. Swap makes the partition
// race-safe across concurrently pooled execs: the deltas sum to the true total.
var lastJITRuns atomic.Int64

// recordJITDelta records the JIT-entry executions that happened since the last
// timed exec. vm.JITRunCount is a process-global monotonic counter, so we diff it
// and feed the delta to the (add-1) spine counter.
func recordJITDelta(ctx context.Context, p observability.Provider) {
	now := vm.JITRunCount()
	prev := lastJITRuns.Swap(now)
	delta := now - prev
	for i := int64(0); i < delta; i++ {
		p.RecordBuzzJITRun(ctx)
	}
}

// TimeExec times fn as one Buzz execution under mode, recording exec duration,
// outcome, and the JIT-run delta when telemetry is active on ctx. With none it
// runs fn directly, adding no timing to the hot path.
func TimeExec(ctx context.Context, mode string, fn func() error) error {
	p := providerFrom(ctx)
	if p == nil {
		return fn()
	}
	start := time.Now()
	err := fn()
	p.RecordBuzzExec(ctx, observability.BuzzExec{Mode: mode, Outcome: outcomeOf(err), Duration: time.Since(start).Seconds()})
	recordJITDelta(ctx, p)
	return err
}

// TimeCall is TimeExec for a value-returning call (Session.CallValue), the target
// and spell-handler dispatch boundary.
func TimeCall(ctx context.Context, mode string, fn func() (vm.Value, error)) (vm.Value, error) {
	p := providerFrom(ctx)
	if p == nil {
		return fn()
	}
	start := time.Now()
	v, err := fn()
	p.RecordBuzzExec(ctx, observability.BuzzExec{Mode: mode, Outcome: outcomeOf(err), Duration: time.Since(start).Seconds()})
	recordJITDelta(ctx, p)
	return v, err
}

// AttachSessionObservers wires the session-scoped compile observer and VM fault
// hook so sess's parse/check/compile phases, resolved imports, and faults feed the
// spine under mode. A no-op when telemetry is disabled, leaving sess unobserved.
func AttachSessionObservers(ctx context.Context, sess *buzz.Session, mode string) {
	p := providerFrom(ctx)
	if p == nil {
		return
	}
	sess.AddCompileObserver(compileObserver{ctx: ctx, p: p, mode: mode})
	sess.SetFaultHook(func(k vm.FaultKind) { p.RecordBuzzVMFault(ctx, k.String()) })
}

// compileObserver forwards a Session's compile-phase and import events to the
// spine. It closes over the run ctx (the hooks carry no ctx) and the provider.
type compileObserver struct {
	ctx  context.Context
	p    observability.Provider
	mode string
}

func (o compileObserver) Phase(phase buzz.CompilePhase, elapsed time.Duration, _ error) {
	o.p.RecordBuzzCompile(o.ctx, observability.BuzzCompile{Phase: phase.String(), Mode: o.mode, Duration: elapsed.Seconds()})
}

func (o compileObserver) Import(_ string, outcome buzz.ImportOutcome, elapsed time.Duration, err error) {
	o.p.RecordBuzzImport(o.ctx, observability.BuzzImport{Kind: outcome.String(), Outcome: outcomeOf(err), Duration: elapsed.Seconds()})
}

// NewPoolObserver returns a PoolObserver that reports Buzz session-pool lifecycle
// (reuse, warm, eviction, idle) to the spine, or nil when telemetry is disabled so
// the pool runs unobserved. Thread the result onto the dispatch ctx with
// buzz.WithPoolObserver.
func NewPoolObserver(ctx context.Context) buzz.PoolObserver {
	p := providerFrom(ctx)
	if p == nil {
		return nil
	}
	return poolObserver{p: p}
}

// poolObserver reports each checkout and release as one pool event; the provider derives the
// idle gauge from the events.
type poolObserver struct {
	p observability.Provider
}

func (o poolObserver) SessionAcquire(ctx context.Context, kind buzz.SessionAcquireKind, _ int) {
	ev := observability.PoolCold
	if kind == buzz.SessionReused {
		ev = observability.PoolReused
	}
	o.p.RecordBuzzSessionPool(ctx, ev)
}

func (o poolObserver) SessionWarm(ctx context.Context, elapsed time.Duration, _ error) {
	o.p.RecordBuzzSessionWarm(ctx, elapsed.Seconds(), "pool")
}

func (o poolObserver) SessionRelease(ctx context.Context, kind buzz.SessionReleaseKind, _ int) {
	ev := observability.PoolReturned
	if kind == buzz.SessionEvicted {
		ev = observability.PoolEvicted
	}
	o.p.RecordBuzzSessionPool(ctx, ev)
}

// NewHostCallObserver returns a DirectObserver that records magus.buzz.host.call
// for every wrapped native callable, or nil when telemetry is disabled so
// buzz.WrapDirect returns the callable unchanged and the VM's hot native-dispatch
// arm is untouched.
func NewHostCallObserver(ctx context.Context) buzz.DirectObserver {
	p := providerFrom(ctx)
	if p == nil {
		return nil
	}
	return hostCallObserver{ctx: ctx, p: p}
}

// hostCallObserver forwards one wrapped native call's timing to the spine. It
// closes over the registration ctx (DirectCall carries none) and the provider.
type hostCallObserver struct {
	ctx context.Context
	p   observability.Provider
}

func (o hostCallObserver) DirectCall(name string, elapsed time.Duration, err error) {
	o.p.RecordBuzzHostCall(o.ctx, observability.BuzzHostCall{
		Callable: name,
		Outcome:  outcomeOf(err),
		Duration: elapsed.Seconds(),
	})
}
