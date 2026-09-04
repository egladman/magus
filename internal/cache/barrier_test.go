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
	c, err := Open(t.Context(), cdir, WithMutable(true))
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
			WithLimiter(NewLimiter(4)))
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
	}, WithLimiter(NewLimiter(8)))
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
	}, WithLimiter(NewLimiter(8)))
	require.NoError(t, err, "RunAll")
}

// TestRunAllRunAfterOrdersAcrossTargets verifies a derived RunAfter edge orders
// two steps that DependsOn cannot: they carry different target names, so the
// same-target coarse wait never applies.
func TestRunAllRunAfterOrdersAcrossTargets(t *testing.T) {
	root, c := openCache(t)
	rec := newOrderRecorder()

	steps := []Step{
		{ProjectPath: "docs", Target: "check", WorkspaceRoot: root, RunAfter: []string{DepKey(".", "gen")}},
		{ProjectPath: ".", Target: "gen", WorkspaceRoot: root},
	}

	_, err := c.RunAll(context.Background(), steps, func(_ context.Context, s Step) error {
		if s.ProjectPath == "docs" {
			assert.True(t, rec.doneBefore("."), "reader started before its derived writer finished")
		}
		rec.start(s.ProjectPath)
		rec.finish(s.ProjectPath)
		return nil
	}, WithLimiter(NewLimiter(8)))
	require.NoError(t, err, "RunAll")
	assert.Len(t, rec.started, 2)
}

// TestRunAllRunAfterUpstreamFailureReleasesWaiter verifies a RunAfter waiter is
// released - and failed - when its writer step fails, not left blocked: markDone
// runs on every exit path, and the waiter reads the writer's real verdict.
func TestRunAllRunAfterUpstreamFailureReleasesWaiter(t *testing.T) {
	root, c := openCache(t)
	steps := []Step{
		{ProjectPath: "docs", Target: "check", WorkspaceRoot: root, RunAfter: []string{DepKey(".", "gen")}},
		{ProjectPath: ".", Target: "gen", WorkspaceRoot: root},
	}
	ran := make(map[string]bool)
	var mu sync.Mutex
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := c.RunAll(context.Background(), steps, func(_ context.Context, s Step) error {
			mu.Lock()
			ran[s.ProjectPath] = true
			mu.Unlock()
			if s.ProjectPath == "." {
				return errors.New("boom")
			}
			return nil
		}, WithLimiter(NewLimiter(8)))
		assert.ErrorContains(t, err, "boom")
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("RunAll deadlocked: RunAfter waiter never released after its writer failed")
	}
	mu.Lock()
	defer mu.Unlock()
	assert.True(t, ran["."], "writer ran")
	assert.False(t, ran["docs"], "reader must not run after its RunAfter writer failed")
}

// TestRunAllRunAfterOutOfScope verifies a RunAfter key naming a step outside
// the batch is skipped, not blocked on forever.
func TestRunAllRunAfterOutOfScope(t *testing.T) {
	root, c := openCache(t)
	steps := []Step{
		{ProjectPath: "docs", Target: "check", WorkspaceRoot: root, RunAfter: []string{DepKey("elsewhere", "gen")}},
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := c.RunAll(context.Background(), steps, func(_ context.Context, _ Step) error { return nil })
		assert.NoError(t, err)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("RunAll deadlocked on an out-of-scope RunAfter key")
	}
}

// TestRunAllRunAfterCycleRejected verifies RunAfter edges join the pre-launch
// acyclicity check, so a bad derivation fails loudly instead of deadlocking.
func TestRunAllRunAfterCycleRejected(t *testing.T) {
	root, c := openCache(t)
	steps := []Step{
		{ProjectPath: "a", Target: "gen", WorkspaceRoot: root, RunAfter: []string{DepKey("b", "check")}},
		{ProjectPath: "b", Target: "check", WorkspaceRoot: root, RunAfter: []string{DepKey("a", "gen")}},
	}
	_, err := c.RunAll(context.Background(), steps, func(_ context.Context, _ Step) error { return nil })
	require.ErrorContains(t, err, "dependency cycle")
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
	}, WithLimiter(NewLimiter(4)))
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
	}, WithLimiter(NewLimiter(4)))
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
	}, WithLimiter(NewLimiter(8)))
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
	}, WithLimiter(NewLimiter(8)))
	assert.Error(t, err, "expected RunAll to return the upstream error")
	mu.Lock()
	defer mu.Unlock()
	assert.False(t, bRan, "B's fn ran even though its dependency A failed")
}

// TestDepBarrierWaitForDepsFailsOnFailedUpstream drives depBarrier directly - no
// goroutines, no errgroup - so it pins the defect rather than racing for it: markDone
// signalling only "done" and not "succeeded" let a dependent proceed on a failed
// upstream, because markDone fires as a defer inside the upstream's own goroutine,
// strictly before errgroup cancels the shared ctx. Marking done-with-error and then
// waiting, both on this goroutine, reproduces that ordering on every run.
func TestDepBarrierWaitForDepsFailsOnFailedUpstream(t *testing.T) {
	steps := []Step{depStep("", "B", "A"), depStep("", "A")}
	b := newDepBarrier(steps)
	wantErr := errors.New("A boom")

	b.markDone(stepKey(steps[1]), wantErr)

	err := b.waitForDeps(context.Background(), steps[0])
	require.Error(t, err, "B must not treat a failed A as satisfied")
	assert.ErrorIs(t, err, wantErr, "the dependent's error names the actual upstream failure")
}

// TestDepBarrierWaitForDepsSucceedsOnPassedUpstream is the control for the test
// above: markDone(nil) must still unblock a dependent cleanly, so the fix above
// (checking e.err) does not turn every dependency into a false failure.
func TestDepBarrierWaitForDepsSucceedsOnPassedUpstream(t *testing.T) {
	steps := []Step{depStep("", "B", "A"), depStep("", "A")}
	b := newDepBarrier(steps)

	b.markDone(stepKey(steps[1]), nil)

	err := b.waitForDeps(context.Background(), steps[0])
	assert.NoError(t, err, "a successful upstream must still unblock its dependent")
}

// TestDepBarrierNamesTheFailedUpstreamEvenWhenCtxIsCancelled pins the tie-break. When
// an upstream fails AND a sibling has already cancelled the group, both the barrier
// channel and ctx.Done() are ready, and a bare select over the two picks uniformly at
// random - so the error naming the actual dependency would appear only about half the
// time and the same failure would report differently run to run. Both are ready on
// every iteration here, so a regression to the random form fails this quickly rather
// than flaking in CI.
func TestDepBarrierNamesTheFailedUpstreamEvenWhenCtxIsCancelled(t *testing.T) {
	steps := []Step{depStep("", "B", "A"), depStep("", "A")}
	wantErr := errors.New("A boom")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for range 100 {
		b := newDepBarrier(steps)
		b.markDone(stepKey(steps[1]), wantErr)

		err := b.waitForDeps(ctx, steps[0])

		require.Error(t, err)
		assert.ErrorIs(t, err, wantErr, "a settled upstream must outrank a cancelled ctx")
	}
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
	}, WithLimiter(NewLimiter(4)))
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
	}, WithLimiter(NewLimiter(4)))
	assert.Error(t, err, "expected RunAll to reject the 3-node cycle")
	assert.NotContains(t, err.Error(), nodeKeySep,
		"the cycle report must not leak the raw node-key separator into a user-facing error")
}

// TestFormatCycle pins the rendering of a node-key cycle. The keys join project and
// target with a control byte, so the naive %v puts an unprintable character in front of
// the user - and this is the error they see when a build order cannot be satisfied, which
// is exactly when the text has to be readable.
func TestFormatCycle(t *testing.T) {
	cycle := []string{DepKey("site", "build"), DepKey("producer", "build"), DepKey("site", "build")}

	assert.Equal(t, "site build -> producer build -> site build", formatCycle(cycle))
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
	}, WithLimiter(NewLimiter(4)))
	require.NoError(t, err, "RunAll")
	assert.Equal(t, 3, count, "expected 3 fn invocations")
	require.Len(t, results, 3, "expected 3 results")
	for i, r := range results {
		assert.Equalf(t, steps[i].ProjectPath, r.ProjectPath, "results[%d].ProjectPath", i)
	}
}

// TestRunAllKeepsGoingPastAnIndependentFailure pins the default: a failing step does
// not cancel peers that do not depend on it. Before this, errgroup cancelled the whole
// group on the first error, so one project's failure killed every unrelated project
// mid-flight and a run could only ever report one failure - which is how a broken npm
// advisory in `console` took down `docs` and hid what `docs` would have said.
func TestRunAllKeepsGoingPastAnIndependentFailure(t *testing.T) {
	root, c := openCache(t)
	boom := errors.New("A boom")

	var mu sync.Mutex
	ran := map[string]bool{}
	steps := []Step{depStep(root, "A"), depStep(root, "B"), depStep(root, "C")}

	_, err := c.RunAll(t.Context(), steps, func(_ context.Context, s Step) error {
		mu.Lock()
		ran[s.ProjectPath] = true
		mu.Unlock()
		if s.ProjectPath == "A" {
			return boom
		}
		return nil
	}, WithLimiter(NewLimiter(1))) // serialized, so A finishes (and would have cancelled) first

	require.Error(t, err)
	assert.ErrorIs(t, err, boom, "the failure is still reported")
	mu.Lock()
	defer mu.Unlock()
	assert.True(t, ran["B"], "B does not depend on A and must still run")
	assert.True(t, ran["C"], "C does not depend on A and must still run")
}

// TestRunAllReportsEveryFailure proves the batch answers with all of its failures
// rather than whichever finished first, so one run tells you everything to fix.
func TestRunAllReportsEveryFailure(t *testing.T) {
	root, c := openCache(t)
	aBoom, cBoom := errors.New("A boom"), errors.New("C boom")

	steps := []Step{depStep(root, "A"), depStep(root, "B"), depStep(root, "C")}
	_, err := c.RunAll(t.Context(), steps, func(_ context.Context, s Step) error {
		switch s.ProjectPath {
		case "A":
			return aBoom
		case "C":
			return cBoom
		}
		return nil
	}, WithLimiter(NewLimiter(4)))

	require.Error(t, err)
	assert.ErrorIs(t, err, aBoom)
	assert.ErrorIs(t, err, cBoom, "the second failure must survive too, not be replaced by the first")
}

// TestRunAllMaxFailuresStops is the opt-in half: a spent budget cancels the batch, so
// --max-failures 1 is fail-fast.
func TestRunAllMaxFailuresStops(t *testing.T) {
	root, c := openCache(t)
	boom := errors.New("A boom")

	var mu sync.Mutex
	started := 0
	release := make(chan struct{})
	steps := []Step{depStep(root, "A"), depStep(root, "B"), depStep(root, "C")}

	_, err := c.RunAll(t.Context(), steps, func(ctx context.Context, s Step) error {
		if s.ProjectPath == "A" {
			return boom
		}
		mu.Lock()
		started++
		mu.Unlock()
		// Block until the batch is torn down, so a peer cannot finish before the
		// budget is spent and make this pass for the wrong reason.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
			return nil
		}
	}, WithLimiter(NewLimiter(4)), WithMaxFailures(1))
	close(release)

	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
}

// TestRunAllDependentFailureDoesNotSpendTheBudget proves a cascade victim is not
// counted: only a step that actually ran and failed on its own account spends the
// budget, so --max-failures 1 stops at the first REAL failure rather than at whichever
// dependent reported first.
func TestRunAllDependentFailureDoesNotSpendTheBudget(t *testing.T) {
	root, c := openCache(t)
	boom := errors.New("A boom")

	var mu sync.Mutex
	ran := map[string]bool{}
	// B depends on A (which fails); C is independent and must still run under a
	// budget of 2, which only A's failure spends.
	steps := []Step{depStep(root, "B", "A"), depStep(root, "A"), depStep(root, "C")}

	_, err := c.RunAll(t.Context(), steps, func(_ context.Context, s Step) error {
		mu.Lock()
		ran[s.ProjectPath] = true
		mu.Unlock()
		if s.ProjectPath == "A" {
			return boom
		}
		return nil
	}, WithLimiter(NewLimiter(1)), WithMaxFailures(2))

	require.Error(t, err)
	mu.Lock()
	defer mu.Unlock()
	assert.False(t, ran["B"], "B's dependency failed, so it must not run")
	assert.True(t, ran["C"], "C is independent and B's cascade must not have spent the budget")
}
