package pool_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/interp/pool"
)

// leafBody returns a Teal target body that signals it has started (by creating
// startedDir/<id>), then busy-polls for releasePath so the job stays in-flight
// until the test releases it. Pure Lua io only — no sh/os dependency — so the
// barrier works in the bare gopher-lua test environment.
func leafBody(id, startedDir, releasePath string) string {
	marker := filepath.Join(startedDir, id)
	return fmt.Sprintf(`global function leaf%s(args: {string})
    local f = io.open(%q, "w")
    if f then f:write("1") f:close() end
    local i = 0
    while i < 200000 do
        local r = io.open(%q, "r")
        if r then r:close() break end
        local x = 0
        while x < 50000 do x = x + 1 end
        i = i + 1
    end
end
`, id, marker, releasePath)
}

func countEntries(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	return len(entries)
}

// TestNestedDispatchRespectsLimiterBudget guards the global-budget invariant:
// pool workers acquire a slot from the run limiter injected into the job
// context, so the number of in-process targets executing at once never exceeds
// the limiter capacity, even when the pool itself has more workers.
//
// With pool capacity 4 and budget 2, exactly 2 leaves run concurrently; the
// other 2 block in the limiter Acquire (and so never write their start marker)
// until a slot frees. If the per-worker acquire were removed, all 4 would run
// and peak would be 4 — this test would then fail, catching the regression.
func TestNestedDispatchRespectsLimiterBudget(t *testing.T) {
	const (
		k      = 4 // distinct leaf targets / pool capacity
		budget = 2 // run-limiter capacity (smaller than the pool)
	)

	dir := t.TempDir()
	startedDir := filepath.Join(dir, "started")
	if err := os.Mkdir(startedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	releasePath := filepath.Join(dir, "release")

	var body strings.Builder
	for i := range k {
		body.WriteString(leafBody(strconv.Itoa(i), startedDir, releasePath))
	}
	writeMagusfile(t, dir, body.String())
	src := findSource(t, dir)

	// Inject a limiter smaller than the pool and mark the caller as holding a
	// slot — the context shape a real top-level RunAll worker has when its spell
	// calls magus.dispatch.
	lim := cache.NewLimiter(budget)
	ctx := cache.WithSlotHeld(cache.ContextWithLimiter(context.Background(), lim))

	p := pool.New(src, k)
	defer p.Close()

	chs := make([]<-chan pool.Result, k)
	for i := range k {
		chs[i] = p.Submit(ctx, "leaf"+strconv.Itoa(i), nil)
	}

	// Observe the peak number of leaves running at once. Running leaves hold
	// until released, so the started-marker count plateaus at the budget while
	// the over-budget leaves wait in Acquire without writing a marker.
	peak := 0
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if n := countEntries(t, startedDir); n > peak {
			peak = n
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Release every leaf and drain.
	if err := os.WriteFile(releasePath, []byte("go"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, ch := range chs {
		if res := <-ch; res.Err != nil {
			t.Fatalf("leaf failed: %v", res.Err)
		}
	}

	t.Logf("peak concurrent nested dispatch = %d (pool capacity=%d, limiter budget=%d)",
		peak, k, budget)
	if peak < 1 {
		t.Fatalf("no leaf ran within the observation window; test is not exercising dispatch")
	}
	if peak > budget {
		t.Fatalf("peak concurrency %d exceeded the limiter budget %d: pool workers are "+
			"not acquiring from the shared limiter", peak, budget)
	}
	// All leaves must eventually complete once released.
	if n := countEntries(t, startedDir); n != k {
		t.Fatalf("only %d of %d leaves ran after release", n, k)
	}
}

// TestNestedDispatchDeepFanOutNoDeadlock is the guard for the pool's own worker
// bound. root dispatches `fan` mids; each mid dispatches `fan` leaves — so the
// number of simultaneously-blocked dispatching parents (root + the mids) reaches
// the pool capacity. The previous fixed-dispatcher design ran exactly `capacity`
// long-lived worker goroutines and would deadlock here: every worker would be a
// parent blocked on its children, leaving no worker free to run the leaves. The
// goroutine-per-job design has no such ceiling, so this must complete.
func TestNestedDispatchDeepFanOutNoDeadlock(t *testing.T) {
	const fan = 4 // fan-out == pool capacity: the deadlock-prone shape

	dir := t.TempDir()
	var b strings.Builder
	for i := range fan {
		fmt.Fprintf(&b, "global function leaf%d(args: {string})\nend\n", i)
	}
	for i := range fan {
		fmt.Fprintf(&b, "global function mid%d(args: {string})\n    magus.depends_on({", i)
		for j := range fan {
			if j > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%q", "leaf"+strconv.Itoa(j))
		}
		b.WriteString("})\nend\n")
	}
	b.WriteString("global function root(args: {string})\n    magus.depends_on({")
	for i := range fan {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q", "mid"+strconv.Itoa(i))
	}
	b.WriteString("})\nend\n")
	writeMagusfile(t, dir, b.String())
	src := findSource(t, dir)

	reg := pool.NewRegistry(fan)
	defer reg.Close()
	p := reg.Get(src)

	// Real top-level worker context: limiter + slot-held marker, plus the
	// registry and source so nested magus.depends_on routes back through the pool.
	lim := cache.NewLimiter(fan)
	ctx := interp.WithSource(
		pool.WithRegistry(
			cache.WithSlotHeld(cache.ContextWithLimiter(context.Background(), lim)),
			reg,
		),
		src,
	)

	ch := p.Submit(ctx, "root", nil)
	select {
	case res := <-ch:
		if res.Err != nil {
			t.Fatalf("deep nested dispatch failed: %v", res.Err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("deep nested dispatch deadlocked: every pool worker was a parent " +
			"blocked on its children, with no worker free to run the leaves")
	}
}

// TestNestedDispatchNoDeadlockAtBudgetOne is the deadlock guard for the global
// budget: a pool worker that holds the single limiter slot and then dispatches
// a child must yield that slot (via proc.RunChildSync, gated on the slot-held
// marker the pool sets) so the child can acquire it. Pool capacity is 2 so a
// free worker is available for the child — isolating the limiter dimension from
// the pool's own per-worker capacity bound. If the yield path regressed, the
// outer target would hold the only slot while blocking on the child and this
// test would hang until the deadline.
func TestNestedDispatchNoDeadlockAtBudgetOne(t *testing.T) {
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "inner_ran")
	writeMagusfile(t, dir, `
global function inner(args: {string})
    local f = io.open("`+sentinel+`", "w")
    if f then f:write("1") f:close() end
end
global function outer(args: {string})
    magus.depends_on(magus.target.expand_globs("inner*"))
end
`)
	src := findSource(t, dir)

	lim := cache.NewLimiter(1) // serial budget: the deadlock-prone case
	reg := pool.NewRegistry(2) // ≥2 workers so a free one can run the child
	defer reg.Close()
	p := reg.Get(src)

	// The job context must carry the limiter (so the worker acquires a slot),
	// the registry and source (so the nested magus.dispatch resolves via the
	// pool), and the slot-held marker (so the nested dispatch yields).
	ctx := interp.WithSource(
		pool.WithRegistry(
			cache.WithSlotHeld(cache.ContextWithLimiter(context.Background(), lim)),
			reg,
		),
		src,
	)

	ch := p.Submit(ctx, "outer", nil)
	select {
	case res := <-ch:
		if res.Err != nil {
			t.Fatalf("outer dispatch failed: %v", res.Err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("nested dispatch deadlocked at budget=1: outer held the only limiter " +
			"slot while waiting on inner instead of yielding it")
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Errorf("inner target did not run: %v", err)
	}
}
