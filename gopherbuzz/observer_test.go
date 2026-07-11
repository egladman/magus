package buzz

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	vmpackage "github.com/egladman/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// observerStub records the TargetEnd notifications it receives.
type observerStub struct {
	mu   sync.Mutex
	ends map[string]error
}

func newObserverStub() *observerStub { return &observerStub{ends: map[string]error{}} }

func (o *observerStub) TargetEnd(_ context.Context, name string, _ time.Duration, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ends[name] = err
}

func (o *observerStub) snapshot() map[string]error {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make(map[string]error, len(o.ends))
	for k, v := range o.ends {
		out[k] = v
	}
	return out
}

// observerTestPool builds a pool whose targets are trivial Go callables, so the test
// exercises the observer hook without compiling a magusfile.
func observerTestPool(t *testing.T) *Pool {
	t.Helper()
	factory := func(ctx context.Context) (*Session, map[string]vmpackage.Callable, error) {
		targets := map[string]vmpackage.Callable{
			"ok": func(context.Context, []vmpackage.Value) (vmpackage.Value, error) { return vmpackage.Null, nil },
			"boom": func(context.Context, []vmpackage.Value) (vmpackage.Value, error) {
				return vmpackage.Null, errors.New("kaboom")
			},
		}
		return NewSession(ctx), targets, nil
	}
	return newPool(factory, nil, 2)
}

// TestPoolObserverFiresPerTarget verifies the pool notifies an attached observer once
// per executed target, with the target's outcome (nil for success, the error for failure).
func TestPoolObserverFiresPerTarget(t *testing.T) {
	p := observerTestPool(t)
	defer func() { _ = p.Close() }()

	obs := newObserverStub()
	ctx := WithObserver(WithTargetMemo(context.Background(), NewTargetMemo()), obs)

	require.NoError(t, p.Dispatch(ctx, []string{"ok"}, nil))
	require.Error(t, p.Dispatch(ctx, []string{"boom"}, nil))

	got := obs.snapshot()
	require.Len(t, got, 2, "observer should see both targets")
	assert.NoError(t, got["ok"], "successful target reports nil error")
	require.Error(t, got["boom"], "failed target reports its error")
	assert.Contains(t, got["boom"].Error(), "kaboom")
}

// TestPoolObserverDedupedByMemo verifies a target needed twice under one memo runs —
// and is observed — exactly once.
func TestPoolObserverDedupedByMemo(t *testing.T) {
	p := observerTestPool(t)
	defer func() { _ = p.Close() }()

	obs := &countingObserver{}
	ctx := WithObserver(WithTargetMemo(context.Background(), NewTargetMemo()), obs)

	require.NoError(t, p.Dispatch(ctx, []string{"ok"}, nil))
	require.NoError(t, p.Dispatch(ctx, []string{"ok"}, nil)) // second need: memo serves the cached result

	assert.Equal(t, 1, obs.count(), "a memo-deduped target is observed exactly once")
}

type countingObserver struct {
	mu sync.Mutex
	n  int
}

func (o *countingObserver) TargetEnd(context.Context, string, time.Duration, error) {
	o.mu.Lock()
	o.n++
	o.mu.Unlock()
}

func (o *countingObserver) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.n
}

// --- PoolObserver ---

type poolObserverStub struct {
	mu       sync.Mutex
	acquires []bool // reused flag per acquire
	warms    int
	releases []bool // evicted flag per release
	lastIdle int
}

func (o *poolObserverStub) SessionAcquire(_ context.Context, reused bool, idle int) {
	o.mu.Lock()
	o.acquires = append(o.acquires, reused)
	o.lastIdle = idle
	o.mu.Unlock()
}

func (o *poolObserverStub) SessionWarm(_ context.Context, _ time.Duration, _ error) {
	o.mu.Lock()
	o.warms++
	o.mu.Unlock()
}

func (o *poolObserverStub) SessionRelease(_ context.Context, evicted bool, idle int) {
	o.mu.Lock()
	o.releases = append(o.releases, evicted)
	o.lastIdle = idle
	o.mu.Unlock()
}

// TestPoolObserverColdThenReuse verifies the pool reports a cold warm on the first
// checkout (no idle session) and a reuse on the second, plus a retaining release
// each time (the pool has capacity for the freed session).
func TestPoolObserverColdThenReuse(t *testing.T) {
	p := observerTestPool(t)
	defer func() { _ = p.Close() }()

	obs := &poolObserverStub{}
	// No memo: each Dispatch must actually run "ok" (a shared memo would dedupe the
	// second call and skip its checkout, hiding the reuse we want to observe).
	ctx := WithPoolObserver(context.Background(), obs)

	require.NoError(t, p.Dispatch(ctx, []string{"ok"}, nil))
	require.NoError(t, p.Dispatch(ctx, []string{"ok"}, nil))

	obs.mu.Lock()
	defer obs.mu.Unlock()
	require.Equal(t, []bool{false, true}, obs.acquires, "first checkout cold, second reused")
	assert.Equal(t, 1, obs.warms, "exactly one session warmed")
	require.Equal(t, []bool{false, false}, obs.releases, "both releases retain (pool not full)")
	assert.Equal(t, 1, obs.lastIdle, "one session idle after the last release")
}

// TestPoolObserverUnsetNoop verifies a pool with no observer attached runs
// unchanged (no panic, correct outcomes).
func TestPoolObserverUnsetNoop(t *testing.T) {
	p := observerTestPool(t)
	defer func() { _ = p.Close() }()

	ctx := WithTargetMemo(context.Background(), NewTargetMemo())
	require.NoError(t, p.Dispatch(ctx, []string{"ok"}, nil))
	require.Error(t, p.Dispatch(ctx, []string{"boom"}, nil))
}

// --- CompileObserver ---

type compileObserverStub struct {
	phases  []vmPhaseRecord
	imports []importRecord
}

type vmPhaseRecord struct {
	phase CompilePhase
	err   error
}

type importRecord struct {
	path    string
	outcome ImportOutcome
	err     error
}

func (o *compileObserverStub) Phase(phase CompilePhase, _ time.Duration, err error) {
	o.phases = append(o.phases, vmPhaseRecord{phase: phase, err: err})
}

func (o *compileObserverStub) Import(path string, outcome ImportOutcome, _ time.Duration, err error) {
	o.imports = append(o.imports, importRecord{path: path, outcome: outcome, err: err})
}

// TestCompileObserverPhases verifies a clean Exec fires parse, check, and compile
// in pipeline order, each with a nil error.
func TestCompileObserverPhases(t *testing.T) {
	ctx := context.Background()
	s := NewSession(ctx, WithEmbedded())
	defer func() { _ = s.Close() }()

	obs := &compileObserverStub{}
	s.SetCompileObserver(obs)

	require.NoError(t, s.Exec(ctx, `var x = 1 + 2;`))

	require.Equal(t, []vmPhaseRecord{
		{phase: PhaseParse},
		{phase: PhaseCheck},
		{phase: PhaseCompile},
	}, obs.phases)
}

// TestCompileObserverImportSynthetic verifies an import resolving to a host
// synthetic module is reported with ImportSynthetic.
func TestCompileObserverImportSynthetic(t *testing.T) {
	ctx := context.Background()
	s := NewSession(ctx, WithEmbedded())
	defer func() { _ = s.Close() }()

	s.SetSyntheticModule("widget", vmpackage.NewMap())
	obs := &compileObserverStub{}
	s.SetCompileObserver(obs)

	require.NoError(t, s.Exec(ctx, `import "widget";`))

	require.Len(t, obs.imports, 1)
	assert.Equal(t, importRecord{path: "widget", outcome: ImportSynthetic}, obs.imports[0])
}

// --- Session fault hook ---

func newFaultSession(t *testing.T) (*Session, *[]vmpackage.FaultKind) {
	t.Helper()
	s := NewSession(context.Background(), WithEmbedded())
	t.Cleanup(func() { _ = s.Close() })
	var kinds []vmpackage.FaultKind
	s.SetFaultHook(func(k vmpackage.FaultKind) { kinds = append(kinds, k) })
	s.SetGlobal("boom", vmpackage.DirectValue("boom", func(context.Context, []vmpackage.Value) (vmpackage.Value, error) {
		return vmpackage.Null, errors.New("kaboom")
	}))
	return s, &kinds
}

// TestFaultHookUncaughtHostError verifies an uncaught host-callable error fires
// exactly one FaultHostError and still propagates.
func TestFaultHookUncaughtHostError(t *testing.T) {
	s, kinds := newFaultSession(t)
	require.Error(t, s.Exec(context.Background(), `boom();`))
	assert.Equal(t, []vmpackage.FaultKind{vmpackage.FaultHostError}, *kinds)
}

// TestFaultHookCaughtHostError verifies a caught host error still fires
// FaultHostError once (it counts the throw, not just uncaught ones) and does not
// surface as an error.
func TestFaultHookCaughtHostError(t *testing.T) {
	s, kinds := newFaultSession(t)
	require.NoError(t, s.Exec(context.Background(), `try { boom(); } catch (e: any) { }`))
	assert.Equal(t, []vmpackage.FaultKind{vmpackage.FaultHostError}, *kinds)
}

// --- WrapDirect ---

type directObserverStub struct {
	calls []directRecord
}

type directRecord struct {
	name string
	err  error
}

func (o *directObserverStub) DirectCall(name string, _ time.Duration, err error) {
	o.calls = append(o.calls, directRecord{name: name, err: err})
}

// TestWrapDirectRecords verifies a Callable wrapped with WrapDirect reports its
// name and outcome, and still returns the underlying value/error unchanged.
func TestWrapDirectRecords(t *testing.T) {
	ctx := context.Background()
	s := NewSession(ctx, WithEmbedded())
	defer func() { _ = s.Close() }()

	obs := &directObserverStub{}
	inner := func(context.Context, []vmpackage.Value) (vmpackage.Value, error) {
		return vmpackage.IntValue(7), nil
	}
	s.SetGlobal("seven", vmpackage.DirectValue("seven", WrapDirect("seven", inner, obs)))

	require.NoError(t, s.Exec(ctx, `final r = seven();`))
	require.Len(t, obs.calls, 1)
	assert.Equal(t, directRecord{name: "seven"}, obs.calls[0])
}

// TestWrapDirectNilObserverIsIdentity verifies WrapDirect returns the callable
// unchanged when no observer is supplied.
func TestWrapDirectNilObserverIsIdentity(t *testing.T) {
	called := 0
	inner := func(context.Context, []vmpackage.Value) (vmpackage.Value, error) {
		called++
		return vmpackage.Null, nil
	}
	wrapped := WrapDirect("x", inner, nil)
	_, err := wrapped(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, 1, called)
}
