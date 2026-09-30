package cache

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/egladman/magus/types"
)

// TargetRuns is one run's record of the targets it has run, each at most once. Whoever
// reaches a target first runs it; everyone after waits on that run and gets its outcome,
// error included, however many composers, projects or passes reach it. It is the live
// counterpart of what `magus status` reports as a StatusRun's target runs. A body's own
// record (buzz.TargetRuns) is seeded from it, never the other way round.
//
// A failed run is not retried within the run: a second composer reaching a target that
// failed gets the same error rather than a second attempt.
type TargetRuns struct {
	mu   sync.Mutex
	runs map[types.TargetRef]*TargetRun
}

// TargetRun is one target's run: done closes once err holds its outcome.
type TargetRun struct {
	done chan struct{}
	once sync.Once
	err  error
}

// NewTargetRuns returns an empty record for one run.
func NewTargetRuns() *TargetRuns {
	return &TargetRuns{runs: map[types.TargetRef]*TargetRun{}}
}

type targetRunsKey struct{}

// WithTargetRuns stores r in ctx.
func WithTargetRuns(ctx context.Context, r *TargetRuns) context.Context {
	return context.WithValue(ctx, targetRunsKey{}, r)
}

// TargetRunsFromContext returns the record WithTargetRuns stored, or nil outside a run,
// where nothing may be dispatched.
func TargetRunsFromContext(ctx context.Context) *TargetRuns {
	r, _ := ctx.Value(targetRunsKey{}).(*TargetRuns)
	return r
}

// TryRun reports whether the caller is first to reach ref, and so runs it. Either way the
// returned TargetRun is ref's; a caller that is first must Complete it, deferred, or every
// later caller waits forever. Once does both for a caller that runs in place.
func (r *TargetRuns) TryRun(ref types.TargetRef) (*TargetRun, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if run, ok := r.runs[ref]; ok {
		return run, false
	}
	run := &TargetRun{done: make(chan struct{})}
	r.runs[ref] = run
	return run, true
}

// Once runs fn for ref if the caller is first to reach it, and otherwise waits on the run
// that is. ctx bounds only the wait: a cancelled waiter returns and the run goes on. A
// panic in fn completes ref with an error before it re-raises, so no waiter is stranded.
func (r *TargetRuns) Once(ctx context.Context, ref types.TargetRef, fn func() error) (err error) {
	run, first := r.TryRun(ref)
	if !first {
		return run.Wait(ctx)
	}
	defer func() {
		if p := recover(); p != nil {
			run.Complete(fmt.Errorf("%s panicked: %v", ref.Ref(), p))
			panic(p)
		}
		run.Complete(err)
	}()
	return fn()
}

// MarkDone records ref as run to completion by a pass that already finished it. A ref
// already reached keeps its own outcome.
func (r *TargetRuns) MarkDone(ref types.TargetRef) {
	if run, first := r.TryRun(ref); first {
		run.Complete(nil)
	}
}

// Passed lists project's targets that completed without error, sorted: the names a body's
// record in that project is seeded with.
func (r *TargetRuns) Passed(project string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for ref, run := range r.runs {
		if ref.Project == project && run.passed() {
			out = append(out, ref.Target)
		}
	}
	slices.Sort(out)
	return out
}

// Complete publishes err as the run's outcome and releases every waiter. Only the first
// call counts, so a deferred Complete after an explicit one is harmless.
func (t *TargetRun) Complete(err error) {
	t.once.Do(func() {
		t.err = err
		close(t.done)
	})
}

// Wait returns the run's outcome once it completes, or ctx's error if ctx ends first.
func (t *TargetRun) Wait(ctx context.Context) error {
	select {
	case <-t.done:
		return t.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *TargetRun) passed() bool {
	select {
	case <-t.done:
		return t.err == nil
	default:
		return false
	}
}
