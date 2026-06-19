package interp

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"

	buzz "github.com/egladman/gopherbuzz"
)

type crossDispatchCtxKey struct{}
type crossAncestorCtxKey struct{}

// CrossDispatch runs cross-project target dependencies (declared via a project
// import, then referenced as <alias>.<target>) at most once per run and detects
// cross-project cycles.
// One instance is installed in the run context and shared across every target, so
// two targets that both need the same remote target run it once; Dispatch is safe
// for concurrent use.
type CrossDispatch struct {
	mu  sync.Mutex
	m   map[string]*crossEntry
	run func(ctx context.Context, dir, target string) error // RunDir; swappable in tests
}

type crossEntry struct {
	done chan struct{}
	err  error
}

// NewCrossDispatch returns an empty coordinator for one run.
func NewCrossDispatch() *CrossDispatch {
	return &CrossDispatch{
		m:   make(map[string]*crossEntry),
		run: func(ctx context.Context, dir, target string) error { return RunDir(ctx, dir, target, nil) },
	}
}

// WithCrossDispatch stores c in ctx; bindings retrieve it to run external deps.
func WithCrossDispatch(ctx context.Context, c *CrossDispatch) context.Context {
	return context.WithValue(ctx, crossDispatchCtxKey{}, c)
}

// CrossDispatchFromContext returns the coordinator stored by WithCrossDispatch, or
// nil — e.g. in describe/parse, where external deps stay graph-only and must not run.
func CrossDispatchFromContext(ctx context.Context) *CrossDispatch {
	c, _ := ctx.Value(crossDispatchCtxKey{}).(*CrossDispatch)
	return c
}

func withCrossAncestor(ctx context.Context, key string) context.Context {
	prev, _ := ctx.Value(crossAncestorCtxKey{}).([]string)
	next := append(append([]string(nil), prev...), key)
	return context.WithValue(ctx, crossAncestorCtxKey{}, next)
}

func crossAncestors(ctx context.Context) []string {
	a, _ := ctx.Value(crossAncestorCtxKey{}).([]string)
	return a
}

// Dispatch runs target in the project rooted at dir, at most once per run. A second
// caller for the same (dir, target) blocks on and shares the first run's result. A
// (dir, target) already on the current call stack is a cross-project cycle and
// errors instead of deadlocking.
//
// The caller is responsible for yielding any concurrency slot it holds before
// calling Dispatch (the remote run needs slots of its own); see the binding's use
// of proc.RunChildSync.
func (c *CrossDispatch) Dispatch(ctx context.Context, dir, target string) error {
	key := dir + "\x00" + target
	if slices.Contains(crossAncestors(ctx), key) {
		return fmt.Errorf("cross-project cycle: %s target %q", dir, target)
	}

	c.mu.Lock()
	if e, ok := c.m[key]; ok {
		c.mu.Unlock()
		slog.DebugContext(ctx, "interp: cross-project dispatch (awaiting in-flight run)", "dir", dir, "target", target)
		<-e.done
		return e.err
	}
	slog.DebugContext(ctx, "interp: cross-project dispatch", "dir", dir, "target", target)
	e := &crossEntry{done: make(chan struct{})}
	c.m[key] = e
	c.mu.Unlock()

	// A fresh memo so the remote project's internal depends_on dedups within its own
	// run without colliding with the caller's (target names are per-project). The
	// ancestor key guards a cycle back through this same remote target.
	rctx := buzz.WithTargetMemo(ctx, buzz.NewTargetMemo())
	rctx = withCrossAncestor(rctx, key)
	// e.done is the publication point: e.err is written before close, and a waiter
	// only reads it after <-e.done, so the write is visible without a data race.
	// close is deferred and panic-recovering: c.run does work outside the buzz VM's
	// own recover (file I/O, chdir, child-process plumbing), and a panic that left
	// e.done unclosed would hang every waiter on this key — and, transitively, the
	// run's errgroup.Wait — forever. Convert the panic to an error and re-raise so
	// the caller's goroutine still unwinds.
	defer func() {
		if r := recover(); r != nil {
			e.err = fmt.Errorf("cross-dispatch panic: %s target %q: %v", dir, target, r)
			close(e.done)
			panic(r)
		}
	}()
	e.err = c.run(rctx, dir, target)
	close(e.done)
	return e.err
}
