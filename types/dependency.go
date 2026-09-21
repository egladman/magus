package types

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

type dependencyWaitKey struct{}

// DependencyWait is one target body's relationship to the targets it composes: the work
// it has asked for and has not received yet, and how long it has spent waiting in total.
//
// One type for both halves, because they are one event. They start together (a body
// reaches ctx.needs), end together, and scope together (per body). Held apart, the time
// had to be recorded by hand at each site that ran a dependency, and a site that forgot
// simply stopped reporting, silently. Do is the only way to run the work, and Do is
// what measures it.
//
// Safe for concurrent use: the elapsed total is atomic, and the pending request is
// guarded, because a body's dependencies fan out across goroutines.
type DependencyWait struct {
	elapsed atomic.Int64

	mu      sync.Mutex
	request func(context.Context) error
}

// Request records work the body wants run before it continues. At most one is
// outstanding: a body asks, is served, and resumes before it can ask again.
func (w *DependencyWait) Request(run func(context.Context) error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.request = run
}

// Do performs the outstanding request and books the time against this body. It reports
// whether there was one, so a caller can tell "nothing asked" from "asked and done".
//
// Do, after http.Client.Do: a request is made, and Do performs it. NOT Serve, which in
// Go means handling requests in a loop (http.Server.Serve) and would promise one that
// is not here; and not Resume, which is the fiber driver's word for a different act.
//
// The measurement is not optional here, which is the point of folding it in: dependency
// time is counted because running a dependency goes through this method.
func (w *DependencyWait) Do(ctx context.Context) (did bool, err error) {
	w.mu.Lock()
	run := w.request
	w.request = nil
	w.mu.Unlock()
	if run == nil {
		return false, nil
	}
	started := time.Now()
	defer func() { w.elapsed.Add(int64(time.Since(started))) }()
	return true, run(ctx)
}

// Elapsed is how long this body has spent on the targets it composes: dispatching them,
// queueing for their admission, and running them. Zero for a leaf target and for a body
// that has not reached a ctx.needs yet.
func (w *DependencyWait) Elapsed() time.Duration {
	if w == nil {
		return 0
	}
	return time.Duration(w.elapsed.Load())
}

// Add books d against this body directly, for a caller that ran dependency work without
// going through Serve.
//
// The blocking ctx.needs path is the one such caller: it runs its dependencies inline
// rather than parking, so nothing serves them. It exists for the paths that do not
// drive a body as a fiber, which must still report their split.
func (w *DependencyWait) Add(d time.Duration) {
	if w == nil {
		return
	}
	w.elapsed.Add(int64(d))
}

// Nested returns the accumulator a CHILD body gets: a fresh one, never this.
//
// Not inherited, because a composed target's own dependency time belongs to its body,
// and the parent already counts the whole child (queueing, work and all) as one span of
// its own. Sharing would double it. A method rather than a constructor so the call site
// reads as derivation from a parent, the way a scoped client derives from its client.
func (w *DependencyWait) Nested() *DependencyWait { return &DependencyWait{} }

// WithDependencyWait gives ctx a fresh accumulator for one target body. Every body gets
// one, whether or not it declares a ceiling.
//
// INSTALLING ONE IS A PROMISE TO DRIVE THE BODY. Its presence is what tells ctx.needs it
// may Request work and be resumed rather than running dependencies inline, so a caller
// that installs one and then never calls Do leaves the body parked forever. The only
// installer is the target-body driver (interp.runTargetBody), and a body reached any
// other way correctly finds none and runs its dependencies itself.
func WithDependencyWait(ctx context.Context) context.Context {
	return context.WithValue(ctx, dependencyWaitKey{}, &DependencyWait{})
}

// DependencyWaitFromContext returns the accumulator for the nearest enclosing body, or
// nil outside one. Every method tolerates a nil receiver, so a caller need not check.
func DependencyWaitFromContext(ctx context.Context) *DependencyWait {
	w, _ := ctx.Value(dependencyWaitKey{}).(*DependencyWait)
	return w
}
