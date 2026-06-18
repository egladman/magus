package cache

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// depSpec builds a minimal spec for project path p depending on deps. Sources
// is left empty (no files), so the spec always misses and runs fn.
func depSpec(root, p string, deps ...string) Spec {
	return Spec{
		ProjectPath:   p,
		WorkspaceRoot: root,
		DependsOn:     deps,
	}
}

// orderRecorder records the start order of project executions under a lock so
// the test can assert dependency-respecting ordering.
type orderRecorder struct {
	mu       sync.Mutex
	started  []string
	finished map[string]bool
}

func newOrderRecorder() *orderRecorder {
	return &orderRecorder{finished: map[string]bool{}}
}

func (r *orderRecorder) start(p string) {
	r.mu.Lock()
	r.started = append(r.started, p)
	r.mu.Unlock()
}

func (r *orderRecorder) finish(p string) {
	r.mu.Lock()
	r.finished[p] = true
	r.mu.Unlock()
}

func (r *orderRecorder) doneBefore(p string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.finished[p]
}

func openCache(t *testing.T) (root string, c *Cache) {
	t.Helper()
	root = t.TempDir()
	cdir := filepath.Join(t.TempDir(), ".magus")
	c, err := Open(cdir, WithMutable(true))
	require.NoError(t, err, "cache.Open")
	return root, c
}

// TestRunAllUpstreamKeyPropagatesToDependent verifies that a dependent's cache
// key changes when an in-scope upstream's key changes, even when the dependent
// has no sources of its own. This is the transitive-miss guarantee: a change
// captured upstream (e.g. a toolchain bump) must invalidate everything
// downstream, not just the upstream itself.
func TestRunAllUpstreamKeyPropagatesToDependent(t *testing.T) {
	root, c := openCache(t)

	srcA := filepath.Join(root, "a.txt")
	require.NoError(t, os.WriteFile(srcA, []byte("v1"), 0o644))

	// A hashes a real source file; B depends on A but declares no sources, so
	// its key can only change via upstream-key propagation.
	mkSpecs := func() []Spec {
		return []Spec{
			{ProjectPath: "A", WorkspaceRoot: root, Target: "build", Sources: []string{"a.txt"}},
			{ProjectPath: "B", WorkspaceRoot: root, Target: "build", DependsOn: []string{"A"}},
		}
	}
	run := func() (keyA, keyB string) {
		results, err := c.RunAll(context.Background(), mkSpecs(),
			func(_ context.Context, _ Spec) error { return nil },
			WithConcurrency(4))
		require.NoError(t, err, "RunAll")
		return results[0].Hash, results[1].Hash
	}

	a1, b1 := run()
	require.NotEmpty(t, a1, "empty key A")
	require.NotEmpty(t, b1, "empty key B")

	// Re-running with no input change must leave both keys stable.
	a2, b2 := run()
	assert.Equal(t, a1, a2, "A key changed without any input change")
	assert.Equal(t, b1, b2, "B key changed without any input change")

	// Edit A's source (different length defeats the mtime/size fast-path). A's
	// key must change, and B must inherit the change.
	require.NoError(t, os.WriteFile(srcA, []byte("v2-different-length"), 0o644))
	a3, b3 := run()
	assert.NotEqual(t, a1, a3, "A key unchanged after editing its source")
	assert.NotEqual(t, b1, b3, "B key unchanged after upstream A changed: upstream-key propagation is missing")
}

// TestRunAllAfterCrossTargetOrdering verifies the After edge: P:test must wait
// for P:build even though both share a ProjectPath. This also exercises the
// (project,target) keying — the two specs are distinct nodes, not collapsed.
func TestRunAllAfterCrossTargetOrdering(t *testing.T) {
	root, c := openCache(t)
	rec := newOrderRecorder()

	specs := []Spec{
		{ProjectPath: "P", WorkspaceRoot: root, Target: "test", After: []string{DepKey("P", "build")}},
		{ProjectPath: "P", WorkspaceRoot: root, Target: "build"},
	}
	_, err := c.RunAll(context.Background(), specs, func(_ context.Context, s Spec) error {
		id := s.ProjectPath + ":" + s.Target
		if s.Target == "test" {
			assert.True(t, rec.doneBefore("P:build"), "P:test started before P:build finished")
		}
		rec.start(id)
		rec.finish(id)
		return nil
	}, WithConcurrency(8))
	require.NoError(t, err, "RunAll")
	assert.Len(t, rec.started, 2, "expected both P:build and P:test to run")
}

// TestRunAllAfterCycleRejected verifies a cross-target cycle (P:test after
// P:build, P:build after P:test) is detected before any goroutine launches.
func TestRunAllAfterCycleRejected(t *testing.T) {
	root, c := openCache(t)
	specs := []Spec{
		{ProjectPath: "P", WorkspaceRoot: root, Target: "test", After: []string{DepKey("P", "build")}},
		{ProjectPath: "P", WorkspaceRoot: root, Target: "build", After: []string{DepKey("P", "test")}},
	}
	_, err := c.RunAll(context.Background(), specs, func(_ context.Context, _ Spec) error {
		return nil
	}, WithConcurrency(8))
	assert.Error(t, err, "expected cycle error")
}

// TestRunAllDependencyOrdering verifies that an A→B→C chain (C depends on B,
// B depends on A) executes strictly in topological order even with ample
// concurrency: each project's fn must observe its upstream as finished.
func TestRunAllDependencyOrdering(t *testing.T) {
	root, c := openCache(t)
	rec := newOrderRecorder()

	specs := []Spec{
		depSpec(root, "C", "B"),
		depSpec(root, "B", "A"),
		depSpec(root, "A"),
	}

	_, err := c.RunAll(context.Background(), specs, func(_ context.Context, s Spec) error {
		// Upstream must already be finished when this fn runs.
		switch s.ProjectPath {
		case "B":
			assert.True(t, rec.doneBefore("A"), "B started before A finished")
		case "C":
			assert.True(t, rec.doneBefore("B"), "C started before B finished")
		}
		rec.start(s.ProjectPath)
		rec.finish(s.ProjectPath)
		return nil
	}, WithConcurrency(8))
	require.NoError(t, err, "RunAll")

	assert.Len(t, rec.started, 3, "expected 3 projects to run")
}

// TestRunAllDependencyDiamond verifies a diamond graph: D depends on B and C,
// both of which depend on A. D must not start until both B and C finish.
func TestRunAllDependencyDiamond(t *testing.T) {
	root, c := openCache(t)
	rec := newOrderRecorder()

	specs := []Spec{
		depSpec(root, "D", "B", "C"),
		depSpec(root, "B", "A"),
		depSpec(root, "C", "A"),
		depSpec(root, "A"),
	}

	_, err := c.RunAll(context.Background(), specs, func(_ context.Context, s Spec) error {
		switch s.ProjectPath {
		case "B", "C":
			assert.Truef(t, rec.doneBefore("A"), "%s started before A finished", s.ProjectPath)
		case "D":
			assert.True(t, rec.doneBefore("B") && rec.doneBefore("C"), "D started before both B and C finished")
		}
		rec.start(s.ProjectPath)
		rec.finish(s.ProjectPath)
		return nil
	}, WithConcurrency(8))
	require.NoError(t, err, "RunAll")
}

// TestRunAllDependencyOutOfScope verifies that a dependency on a project not
// present in the specs slice does not deadlock: the dependent runs anyway.
func TestRunAllDependencyOutOfScope(t *testing.T) {
	root, c := openCache(t)
	var ran bool

	specs := []Spec{
		depSpec(root, "X", "not-in-this-run"),
	}

	_, err := c.RunAll(context.Background(), specs, func(_ context.Context, s Spec) error {
		ran = true
		return nil
	}, WithConcurrency(4))
	require.NoError(t, err, "RunAll")
	assert.True(t, ran, "X did not run despite its only dependency being out of scope")
}

// TestRunAllSelfDependencyDoesNotDeadlock verifies that a spec listing itself
// in DependsOn is tolerated (the self-edge is skipped) rather than blocking
// forever on its own completion.
func TestRunAllSelfDependencyDoesNotDeadlock(t *testing.T) {
	root, c := openCache(t)
	var ran bool

	specs := []Spec{
		depSpec(root, "self", "self"),
	}

	_, err := c.RunAll(context.Background(), specs, func(_ context.Context, s Spec) error {
		ran = true
		return nil
	}, WithConcurrency(4))
	require.NoError(t, err, "RunAll")
	assert.True(t, ran, "self-dependent spec deadlocked instead of running")
}

// TestRunAllIsolatedRunsAlone verifies the Spec.Isolated contract: an isolated
// spec never executes concurrently with any other spec, while non-isolated specs
// still overlap with each other. The sleeps widen the windows so a broken lock
// would let a reader land inside the isolated spec's span and trip the assertion.
func TestRunAllIsolatedRunsAlone(t *testing.T) {
	root, c := openCache(t)

	specs := []Spec{
		{ProjectPath: "isolated", WorkspaceRoot: root, Target: "gen", Isolated: true},
	}
	for i := range 6 {
		specs = append(specs, Spec{
			ProjectPath: "p" + string(rune('0'+i)), WorkspaceRoot: root, Target: "build",
		})
	}

	var (
		mu         sync.Mutex
		inFlight   int
		peak       int // max concurrent non-isolated specs — proves readers overlap
		violations []string
	)
	enter := func(s Spec) {
		mu.Lock()
		defer mu.Unlock()
		inFlight++
		if s.Isolated && inFlight != 1 {
			violations = append(violations, "isolated spec started while another was in flight")
		}
		if !s.Isolated && inFlight > peak {
			peak = inFlight
		}
	}
	leave := func(s Spec) {
		mu.Lock()
		defer mu.Unlock()
		if s.Isolated && inFlight != 1 {
			violations = append(violations, "another spec entered during isolated run")
		}
		inFlight--
	}

	_, err := c.RunAll(context.Background(), specs, func(_ context.Context, s Spec) error {
		enter(s)
		time.Sleep(20 * time.Millisecond)
		leave(s)
		return nil
	}, WithConcurrency(8))
	require.NoError(t, err, "RunAll")
	assert.Empty(t, violations, "isolated spec overlapped with others")
	assert.GreaterOrEqual(t, peak, 2, "non-isolated specs never overlapped; the read lock is over-serializing")
}

// TestRunAllDependencyFailureCancelsDependents verifies that when an upstream
// fails, its dependents are cancelled (their fn is never invoked) rather than
// left parked.
func TestRunAllDependencyFailureCancelsDependents(t *testing.T) {
	root, c := openCache(t)
	wantErr := errors.New("A boom")

	var bRan bool
	var mu sync.Mutex

	specs := []Spec{
		depSpec(root, "B", "A"),
		depSpec(root, "A"),
	}

	_, err := c.RunAll(context.Background(), specs, func(_ context.Context, s Spec) error {
		if s.ProjectPath == "A" {
			return wantErr
		}
		mu.Lock()
		bRan = true
		mu.Unlock()
		return nil
	}, WithConcurrency(8))
	assert.Error(t, err, "expected RunAll to return the upstream error")
	mu.Lock()
	defer mu.Unlock()
	assert.False(t, bRan, "B's fn ran even though its dependency A failed")
}

// TestRunAllDependencyCycleRejected verifies that a true cycle (A→B→A) is
// rejected before any fn runs, returning an error rather than hanging g.Wait()
// forever (which it would under a non-cancellable context).
func TestRunAllDependencyCycleRejected(t *testing.T) {
	root, c := openCache(t)
	var ran bool

	specs := []Spec{
		depSpec(root, "A", "B"),
		depSpec(root, "B", "A"),
	}

	_, err := c.RunAll(context.Background(), specs, func(_ context.Context, s Spec) error {
		ran = true
		return nil
	}, WithConcurrency(4))
	assert.Error(t, err, "expected RunAll to reject the cyclic batch")
	assert.False(t, ran, "fn ran despite the batch being cyclic; nothing should execute")
}

// TestRunAllDependencyCycleThreeNode verifies a longer cycle A→B→C→A is also
// detected (the back edge is not adjacent to the cycle entry point).
func TestRunAllDependencyCycleThreeNode(t *testing.T) {
	root, c := openCache(t)

	specs := []Spec{
		depSpec(root, "A", "B"),
		depSpec(root, "B", "C"),
		depSpec(root, "C", "A"),
	}

	_, err := c.RunAll(context.Background(), specs, func(_ context.Context, s Spec) error {
		return nil
	}, WithConcurrency(4))
	assert.Error(t, err, "expected RunAll to reject the 3-node cycle")
}

// TestRunAllNoDependencies is a regression guard: specs with no DependsOn run
// concurrently and every result slot is populated, matching pre-barrier
// behaviour.
func TestRunAllNoDependencies(t *testing.T) {
	root, c := openCache(t)

	specs := []Spec{
		depSpec(root, "p0"),
		depSpec(root, "p1"),
		depSpec(root, "p2"),
	}

	var count int
	var mu sync.Mutex
	results, err := c.RunAll(context.Background(), specs, func(_ context.Context, s Spec) error {
		mu.Lock()
		count++
		mu.Unlock()
		return nil
	}, WithConcurrency(4))
	require.NoError(t, err, "RunAll")
	assert.Equal(t, 3, count, "expected 3 fn invocations")
	require.Len(t, results, 3, "expected 3 results")
	for i, r := range results {
		assert.Equalf(t, specs[i].ProjectPath, r.ProjectPath, "results[%d].ProjectPath", i)
	}
}
