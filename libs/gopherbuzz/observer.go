package buzz

import (
	"context"
	"time"

	vmpackage "github.com/egladman/magus/libs/gopherbuzz/vm"
)

// The interpreter exposes several optional, nil-gated observers so an embedding
// host (e.g. magus) can attach recorders without paying any cost or changing any
// behaviour when none is set. Two carry through ctx (TargetObserver, PoolObserver)
// because their events fire deep in the dispatch call stack; the compile and fault
// hooks are set directly on a Session because compilation and VM execution are
// session-scoped. Native (direct) call timing is done at binding registration via
// WrapDirect, keeping the VM's hot dispatch arm untouched. The JIT engagement
// counter needs no hook: read vm.JITRunCount() directly.

// TargetObserver is notified as the pool runs targets. The pool calls it once per
// target per run (the target memo collapses repeat dependents into a single run), so
// an observer sees each target exactly once with its wall-clock duration and outcome.
//
// It is optional: attach one with WithObserver. With none set the pool runs unchanged,
// so this adds no cost and no behaviour change to callers that do not opt in.
type TargetObserver interface {
	// TargetEnd reports a finished target: its name, how long its function ran, and the
	// error it returned (nil on success). Called after the target function returns.
	TargetEnd(ctx context.Context, name string, elapsed time.Duration, err error)
}

type observerKey struct{}

// WithObserver returns ctx carrying obs, which Pool.execute notifies for each target.
func WithObserver(ctx context.Context, obs TargetObserver) context.Context {
	return context.WithValue(ctx, observerKey{}, obs)
}

// observerFrom returns the observer attached to ctx, or nil.
func observerFrom(ctx context.Context) TargetObserver {
	obs, _ := ctx.Value(observerKey{}).(TargetObserver)
	return obs
}

// PoolObserver is notified of a Session pool's lifecycle as it serves target runs:
// session checkout (reuse vs cold warm), warm cost, and release or eviction.
//
// It is optional: attach one with WithPoolObserver. With none set the pool runs
// unchanged, so this adds no cost and no behaviour change to callers that do not
// opt in.
type PoolObserver interface {
	// SessionAcquire fires when the pool checks out a session to run a target.
	// reused is true when an idle warm session was taken; false when none was idle
	// and the pool must warm a fresh one (a cold start, reported next by
	// SessionWarm). idle is the idle-session count remaining right after checkout.
	SessionAcquire(ctx context.Context, reused bool, idle int)
	// SessionWarm fires when the pool warms a fresh session, reporting how long
	// construction took and its error (nil on success). Always preceded by a
	// SessionAcquire with reused=false.
	SessionWarm(ctx context.Context, elapsed time.Duration, err error)
	// SessionRelease fires when a finished session returns to the pool. evicted is
	// true when the pool was full or closed and the session was closed instead of
	// retained; idle is the idle-session count right after the release.
	SessionRelease(ctx context.Context, evicted bool, idle int)
}

type poolObserverKey struct{}

// WithPoolObserver returns ctx carrying obs, which a Pool notifies as it acquires,
// warms, and releases sessions. Pass it on the same ctx you hand to Dispatch.
func WithPoolObserver(ctx context.Context, obs PoolObserver) context.Context {
	return context.WithValue(ctx, poolObserverKey{}, obs)
}

// poolObserverFrom returns the pool observer attached to ctx, or nil.
func poolObserverFrom(ctx context.Context) PoolObserver {
	obs, _ := ctx.Value(poolObserverKey{}).(PoolObserver)
	return obs
}

// CompilePhase identifies a sub-phase of turning Buzz source into a runnable chunk.
type CompilePhase int

const (
	PhaseParse   CompilePhase = iota // lexer + parser: source -> AST
	PhaseCheck                       // type checker over the AST
	PhaseCompile                     // AST -> bytecode chunk
)

// String names the compile phase for logs and metric labels (plain ASCII).
func (p CompilePhase) String() string {
	switch p {
	case PhaseParse:
		return "parse"
	case PhaseCheck:
		return "check"
	case PhaseCompile:
		return "compile"
	default:
		return "unknown"
	}
}

// ImportOutcome classifies how a Session resolved one import statement.
type ImportOutcome int

const (
	ImportBound     ImportOutcome = iota // already bound or already loaded; skipped
	ImportSynthetic                      // a host synthetic module value
	ImportSource                         // a host embedded-source module
	ImportResolver                       // resolved by the host module resolver
	ImportFile                           // a .buzz file on the search path
	ImportNotFound                       // nothing resolved the import (an error)
)

// String names the import outcome for logs and metric labels (plain ASCII).
func (o ImportOutcome) String() string {
	switch o {
	case ImportBound:
		return "bound"
	case ImportSynthetic:
		return "synthetic"
	case ImportSource:
		return "source"
	case ImportResolver:
		return "resolver"
	case ImportFile:
		return "file"
	case ImportNotFound:
		return "not-found"
	default:
		return "unknown"
	}
}

// CompileObserver is notified as a Session compiles source into a runnable chunk:
// the parse/check/compile phase timings and each import it resolves.
//
// It is optional: attach one with Session.SetCompileObserver. With none set the
// session compiles unchanged, so this adds no cost and no behaviour change.
type CompileObserver interface {
	// Phase reports one finished compile sub-phase: which phase, how long it ran,
	// and the error it produced (nil on success). Fired in pipeline order (parse,
	// then check, then compile) for each compiled chunk. Diagnostics runs parse and
	// check only, so it fires those two without a following compile.
	Phase(phase CompilePhase, elapsed time.Duration, err error)
	// Import reports one resolved import: its path (as written, e.g. "buzz:os"),
	// how it resolved, how long resolution took, and any error. For a flat file or
	// source import the duration includes executing the imported module, whose own
	// compile phases fire separately on this same observer.
	Import(importPath string, outcome ImportOutcome, elapsed time.Duration, err error)
}

// DirectObserver is notified when a wrapped native (direct) callable returns. It
// is the recommended seam for timing host calls: wrap a Callable with WrapDirect
// at binding registration, which leaves the VM's hot direct-dispatch arm
// untouched. See WrapDirect.
type DirectObserver interface {
	// DirectCall reports one finished direct call: the binding name, its wall-clock
	// duration, and the error it returned (nil on success).
	DirectCall(name string, elapsed time.Duration, err error)
}

// WrapDirect returns a Callable that runs fn and reports its duration and outcome
// to obs under name; register the result with vm.DirectValue as usual. When obs is
// nil it returns fn unchanged, so an unobserved binding pays nothing. This is the
// recommended way to time native calls (host bindings, stdlib directs) without
// touching the interpreter dispatch loop. The wrapper passes args straight through
// and does not retain them, preserving the Callable no-retain contract.
func WrapDirect(name string, fn vmpackage.Callable, obs DirectObserver) vmpackage.Callable {
	if obs == nil {
		return fn
	}
	return func(ctx context.Context, args []vmpackage.Value) (vmpackage.Value, error) {
		start := time.Now()
		v, err := fn(ctx, args)
		obs.DirectCall(name, time.Since(start), err)
		return v, err
	}
}
