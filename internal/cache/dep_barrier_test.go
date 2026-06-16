package cache

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
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
	if err != nil {
		t.Fatalf("cache.Open: %v", err)
	}
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
	if err := os.WriteFile(srcA, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}

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
		if err != nil {
			t.Fatalf("RunAll: %v", err)
		}
		return results[0].Hash, results[1].Hash
	}

	a1, b1 := run()
	if a1 == "" || b1 == "" {
		t.Fatalf("empty keys: a=%q b=%q", a1, b1)
	}

	// Re-running with no input change must leave both keys stable.
	a2, b2 := run()
	if a1 != a2 || b1 != b2 {
		t.Fatalf("keys changed without any input change: a %q→%q b %q→%q", a1, a2, b1, b2)
	}

	// Edit A's source (different length defeats the mtime/size fast-path). A's
	// key must change, and B must inherit the change.
	if err := os.WriteFile(srcA, []byte("v2-different-length"), 0o644); err != nil {
		t.Fatal(err)
	}
	a3, b3 := run()
	if a3 == a1 {
		t.Errorf("A key unchanged after editing its source: %q", a3)
	}
	if b3 == b1 {
		t.Errorf("B key unchanged after upstream A changed: upstream-key propagation is missing (b=%q)", b3)
	}
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
		if s.Target == "test" && !rec.doneBefore("P:build") {
			t.Errorf("P:test started before P:build finished")
		}
		rec.start(id)
		rec.finish(id)
		return nil
	}, WithConcurrency(8))
	if err != nil {
		t.Fatalf("RunAll: %v", err)
	}
	if len(rec.started) != 2 {
		t.Fatalf("expected both P:build and P:test to run, got %v", rec.started)
	}
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
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
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
			if !rec.doneBefore("A") {
				t.Errorf("B started before A finished")
			}
		case "C":
			if !rec.doneBefore("B") {
				t.Errorf("C started before B finished")
			}
		}
		rec.start(s.ProjectPath)
		rec.finish(s.ProjectPath)
		return nil
	}, WithConcurrency(8))
	if err != nil {
		t.Fatalf("RunAll: %v", err)
	}

	if len(rec.started) != 3 {
		t.Fatalf("expected 3 projects to run, got %v", rec.started)
	}
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
			if !rec.doneBefore("A") {
				t.Errorf("%s started before A finished", s.ProjectPath)
			}
		case "D":
			if !rec.doneBefore("B") || !rec.doneBefore("C") {
				t.Errorf("D started before both B and C finished")
			}
		}
		rec.start(s.ProjectPath)
		rec.finish(s.ProjectPath)
		return nil
	}, WithConcurrency(8))
	if err != nil {
		t.Fatalf("RunAll: %v", err)
	}
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
	if err != nil {
		t.Fatalf("RunAll: %v", err)
	}
	if !ran {
		t.Fatal("X did not run despite its only dependency being out of scope")
	}
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
	if err != nil {
		t.Fatalf("RunAll: %v", err)
	}
	if !ran {
		t.Fatal("self-dependent spec deadlocked instead of running")
	}
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
	if err != nil {
		t.Fatalf("RunAll: %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("isolated spec overlapped with others: %v", violations)
	}
	if peak < 2 {
		t.Fatalf("non-isolated specs never overlapped (peak=%d); the read lock is over-serializing", peak)
	}
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
	if err == nil {
		t.Fatal("expected RunAll to return the upstream error, got nil")
	}
	mu.Lock()
	defer mu.Unlock()
	if bRan {
		t.Error("B's fn ran even though its dependency A failed")
	}
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
	if err == nil {
		t.Fatal("expected RunAll to reject the cyclic batch, got nil error")
	}
	if ran {
		t.Error("fn ran despite the batch being cyclic; nothing should execute")
	}
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
	if err == nil {
		t.Fatal("expected RunAll to reject the 3-node cycle, got nil error")
	}
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
	if err != nil {
		t.Fatalf("RunAll: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 fn invocations, got %d", count)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	for i, r := range results {
		if r.ProjectPath != specs[i].ProjectPath {
			t.Errorf("results[%d].ProjectPath = %q, want %q", i, r.ProjectPath, specs[i].ProjectPath)
		}
	}
}
