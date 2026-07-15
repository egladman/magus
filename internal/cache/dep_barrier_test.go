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

// depStep builds a minimal step for project path p depending on deps. Sources
// is left empty (no files), so the step always misses and runs fn.
func depStep(root, p string, deps ...string) Step {
	return Step{
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
	mkSteps := func() []Step {
		return []Step{
			{ProjectPath: "A", WorkspaceRoot: root, Target: "build", Sources: []string{"a.txt"}},
			{ProjectPath: "B", WorkspaceRoot: root, Target: "build", DependsOn: []string{"A"}},
		}
	}
	run := func() (keyA, keyB string) {
		results, err := c.RunAll(context.Background(), mkSteps(),
			func(_ context.Context, _ Step) error { return nil },
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

// TestRunAllDependencyOrdering verifies that an A→B→C chain (C depends on B,
// B depends on A) executes strictly in topological order even with ample
// concurrency: each project's fn must observe its upstream as finished.
func TestRunAllDependencyOrdering(t *testing.T) {
	root, c := openCache(t)
	rec := newOrderRecorder()

	steps := []Step{
		depStep(root, "C", "B"),
		depStep(root, "B", "A"),
		depStep(root, "A"),
	}

	_, err := c.RunAll(context.Background(), steps, func(_ context.Context, s Step) error {
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

	steps := []Step{
		depStep(root, "D", "B", "C"),
		depStep(root, "B", "A"),
		depStep(root, "C", "A"),
		depStep(root, "A"),
	}

	_, err := c.RunAll(context.Background(), steps, func(_ context.Context, s Step) error {
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
// present in the steps slice does not deadlock: the dependent runs anyway.
func TestRunAllDependencyOutOfScope(t *testing.T) {
	root, c := openCache(t)
	var ran bool

	steps := []Step{
		depStep(root, "X", "not-in-this-run"),
	}

	_, err := c.RunAll(context.Background(), steps, func(_ context.Context, s Step) error {
		ran = true
		return nil
	}, WithConcurrency(4))
	require.NoError(t, err, "RunAll")
	assert.True(t, ran, "X did not run despite its only dependency being out of scope")
}

// TestRunAllSelfDependencyDoesNotDeadlock verifies that a step listing itself
// in DependsOn is tolerated (the self-edge is skipped) rather than blocking
// forever on its own completion.
func TestRunAllSelfDependencyDoesNotDeadlock(t *testing.T) {
	root, c := openCache(t)
	var ran bool

	steps := []Step{
		depStep(root, "self", "self"),
	}

	_, err := c.RunAll(context.Background(), steps, func(_ context.Context, s Step) error {
		ran = true
		return nil
	}, WithConcurrency(4))
	require.NoError(t, err, "RunAll")
	assert.True(t, ran, "self-dependent step deadlocked instead of running")
}

// TestRunAllExclusiveRunsAlone verifies the Step.Exclusive contract: an exclusive
// step never executes concurrently with any other step, while non-exclusive steps
// still overlap with each other. The sleeps widen the windows so a broken lock
// would let a reader land inside the exclusive step's span and trip the assertion.
func TestRunAllExclusiveRunsAlone(t *testing.T) {
	root, c := openCache(t)

	steps := []Step{
		{ProjectPath: "exclusive", WorkspaceRoot: root, Target: "gen", Exclusive: true},
	}
	for i := range 6 {
		steps = append(steps, Step{
			ProjectPath: "p" + string(rune('0'+i)), WorkspaceRoot: root, Target: "build",
		})
	}

	var (
		mu         sync.Mutex
		inFlight   int
		peak       int // max concurrent non-exclusive steps; proves readers overlap
		violations []string
	)
	enter := func(s Step) {
		mu.Lock()
		defer mu.Unlock()
		inFlight++
		if s.Exclusive && inFlight != 1 {
			violations = append(violations, "exclusive step started while another was in flight")
		}
		if !s.Exclusive && inFlight > peak {
			peak = inFlight
		}
	}
	leave := func(s Step) {
		mu.Lock()
		defer mu.Unlock()
		if s.Exclusive && inFlight != 1 {
			violations = append(violations, "another step entered during exclusive run")
		}
		inFlight--
	}

	_, err := c.RunAll(context.Background(), steps, func(_ context.Context, s Step) error {
		enter(s)
		time.Sleep(20 * time.Millisecond)
		leave(s)
		return nil
	}, WithConcurrency(8))
	require.NoError(t, err, "RunAll")
	assert.Empty(t, violations, "exclusive step overlapped with others")
	assert.GreaterOrEqual(t, peak, 2, "non-exclusive steps never overlapped; the read lock is over-serializing")
}

// TestRunAllDependencyFailureCancelsDependents verifies that when an upstream
// fails, its dependents are cancelled (their fn is never invoked) rather than
// left parked.
func TestRunAllDependencyFailureCancelsDependents(t *testing.T) {
	root, c := openCache(t)
	wantErr := errors.New("A boom")

	var bRan bool
	var mu sync.Mutex

	steps := []Step{
		depStep(root, "B", "A"),
		depStep(root, "A"),
	}

	_, err := c.RunAll(context.Background(), steps, func(_ context.Context, s Step) error {
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

	steps := []Step{
		depStep(root, "A", "B"),
		depStep(root, "B", "A"),
	}

	_, err := c.RunAll(context.Background(), steps, func(_ context.Context, s Step) error {
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

	steps := []Step{
		depStep(root, "A", "B"),
		depStep(root, "B", "C"),
		depStep(root, "C", "A"),
	}

	_, err := c.RunAll(context.Background(), steps, func(_ context.Context, s Step) error {
		return nil
	}, WithConcurrency(4))
	assert.Error(t, err, "expected RunAll to reject the 3-node cycle")
}

// TestRunAllNoDependencies is a regression guard: steps with no DependsOn run
// concurrently and every result slot is populated, matching pre-barrier
// behaviour.
func TestRunAllNoDependencies(t *testing.T) {
	root, c := openCache(t)

	steps := []Step{
		depStep(root, "p0"),
		depStep(root, "p1"),
		depStep(root, "p2"),
	}

	var count int
	var mu sync.Mutex
	results, err := c.RunAll(context.Background(), steps, func(_ context.Context, s Step) error {
		mu.Lock()
		count++
		mu.Unlock()
		return nil
	}, WithConcurrency(4))
	require.NoError(t, err, "RunAll")
	assert.Equal(t, 3, count, "expected 3 fn invocations")
	require.Len(t, results, 3, "expected 3 results")
	for i, r := range results {
		assert.Equalf(t, steps[i].ProjectPath, r.ProjectPath, "results[%d].ProjectPath", i)
	}
}
