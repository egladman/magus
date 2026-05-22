package buzz

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"sync"
)

// Semaphore is the concurrency budget the pool draws from. *cache.Limiter
// satisfies this interface (Acquire/Release/Yield are defined on it).
type Semaphore interface {
	Acquire(ctx context.Context) error
	Release()
	// Yield releases one slot for the duration of fn and re-acquires it before
	// returning. The caller must hold a slot; use only when buzzSlotHeld is true.
	Yield(ctx context.Context, fn func() error) error
}

// WorkerFunc creates a pre-warmed Buzz session and target map for the pool.
// The session is owned by the pool worker and must not be used concurrently.
type WorkerFunc func(ctx context.Context) (*Session, map[string]Callable, error)

// TargetMemo is a per-invocation run-once tracker. It ensures a target executes
// at most once within one top-level dispatch, even when concurrent `depends_on`
// callers name the same target (diamond dependencies). Safe for concurrent use.
type TargetMemo struct {
	mu      sync.Mutex
	entries map[string]*memoEntry
}

type memoEntry struct {
	done chan struct{} // closed when the target finishes
	err  error
}

// NewTargetMemo returns a fresh, empty TargetMemo for one invocation scope.
func NewTargetMemo() *TargetMemo {
	return &TargetMemo{entries: make(map[string]*memoEntry)}
}

// TryRun checks whether name has already run or is running.
//
// Returns (true, nil) when name is new — caller must run the target then call
// Complete. Returns (false, waitFn) when name is already in-flight or done —
// caller invokes waitFn() to get the result. waitFn blocks until the in-flight
// execution finishes; call it WITHOUT holding a limiter slot.
func (m *TargetMemo) TryRun(name string) (isNew bool, waitFn func() error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.entries[name]; ok {
		return false, func() error { <-e.done; return e.err }
	}
	e := &memoEntry{done: make(chan struct{})}
	m.entries[name] = e
	return true, nil
}

// Complete records err for name and unblocks any waiters. Must be called
// exactly once by the goroutine that received isNew=true from TryRun.
func (m *TargetMemo) Complete(name string, err error) {
	m.mu.Lock()
	e := m.entries[name]
	m.mu.Unlock()
	e.err = err
	close(e.done)
}

// TargetMemo context plumbing.
type targetMemoKey struct{}

// WithTargetMemo returns ctx carrying m as the invocation-scoped target memo.
func WithTargetMemo(ctx context.Context, m *TargetMemo) context.Context {
	return context.WithValue(ctx, targetMemoKey{}, m)
}

// TargetMemoFromContext retrieves the TargetMemo stored by WithTargetMemo, or nil.
func TargetMemoFromContext(ctx context.Context) *TargetMemo {
	v, _ := ctx.Value(targetMemoKey{}).(*TargetMemo)
	return v
}

// Pool is a per-source bounded pool of pre-warmed Buzz sessions.
// Safe for concurrent use.
//
// Concurrency model: each Submit spawns a goroutine that acquires one semaphore
// slot (bounding real parallelism), checks out a warmed session from the idle
// list, runs the target, and returns the session. Because there is no fixed set
// of worker goroutines, a target that dispatches children via Dispatch and blocks
// until they finish never starves the children of a goroutine to run on — nested
// dispatch cannot deadlock regardless of fan-out. Parallelism is bounded by the
// semaphore, which Dispatch yields (via getSem.Yield) so a child can acquire
// the slot its parent holds, even at MAGUS_CONCURRENCY=1.
type Pool struct {
	newSession WorkerFunc
	getSem     func(ctx context.Context) Semaphore // derives semaphore from ctx; nil ok
	capacity   int

	mu     sync.Mutex
	idle   []*poolWorker
	closed bool
	wg     sync.WaitGroup
}

type poolWorker struct {
	sess    *Session
	targets map[string]Callable
}

// PoolRegistry maps string keys to per-source Pools. Safe for concurrent use.
type PoolRegistry struct {
	mu       sync.Mutex
	pools    map[string]*Pool
	getSem   func(ctx context.Context) Semaphore
	capacity int
}

// NewPoolRegistry returns an empty registry. getSem is called per-execute to
// derive the semaphore from ctx (pass nil for no concurrency budget).
// capacity<=0 defaults to NumCPU.
func NewPoolRegistry(getSem func(ctx context.Context) Semaphore, capacity int) *PoolRegistry {
	return &PoolRegistry{
		pools:    make(map[string]*Pool),
		getSem:   getSem,
		capacity: capacity,
	}
}

// Get returns the Pool for key, creating it with newSession on first call.
// newSession is ignored on cache hits.
func (r *PoolRegistry) Get(key string, newSession WorkerFunc) *Pool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.pools[key]; ok {
		return p
	}
	p := newPool(newSession, r.getSem, r.capacity)
	r.pools[key] = p
	return p
}

// Close closes every Pool in the registry.
func (r *PoolRegistry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var errs []error
	for _, p := range r.pools {
		if err := p.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// PoolRegistry context plumbing.
type poolRegistryKey struct{}

// WithPoolRegistry stores reg in ctx for retrieval inside the Buzz call stack.
func WithPoolRegistry(ctx context.Context, reg *PoolRegistry) context.Context {
	return context.WithValue(ctx, poolRegistryKey{}, reg)
}

// PoolRegistryFromContext retrieves the PoolRegistry stored by WithPoolRegistry, or nil.
func PoolRegistryFromContext(ctx context.Context) *PoolRegistry {
	v, _ := ctx.Value(poolRegistryKey{}).(*PoolRegistry)
	return v
}

func newPool(newSession WorkerFunc, getSem func(ctx context.Context) Semaphore, capacity int) *Pool {
	if capacity <= 0 {
		capacity = runtime.NumCPU()
	}
	return &Pool{newSession: newSession, getSem: getSem, capacity: capacity}
}

// Submit dispatches name and returns a channel delivering one error.
// Returns a closed-error channel if the pool is closed or a cycle is detected.
func (p *Pool) Submit(ctx context.Context, name string, ancestors []string) <-chan error {
	ch := make(chan error, 1)
	if slices.Contains(ancestors, name) {
		ch <- fmt.Errorf("buzzpool: dispatch: stack contains %q (cycle detected)", name)
		return ch
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		ch <- errors.New("buzzpool: closed")
		return ch
	}
	p.wg.Add(1)
	p.mu.Unlock()

	go func() {
		defer p.wg.Done()
		ch <- p.execute(ctx, name, ancestors)
	}()
	return ch
}

// Dispatch fans out names concurrently, yielding the caller's buzz slot if
// held so that children can acquire it (deadlock-free at MAGUS_CONCURRENCY=1).
// TargetMemo deduplication is applied when a memo is present in ctx: a target
// already in-flight is subscribed to (not re-submitted); the waitFn is called
// without holding the slot, so it cannot deadlock.
func (p *Pool) Dispatch(ctx context.Context, names []string, ancestors []string) error {
	if len(names) == 0 {
		return nil
	}
	sem := p.getSemFrom(ctx)
	if sem != nil && buzzSlotHeld(ctx) {
		childCtx := withoutBuzzSlot(ctx)
		return sem.Yield(ctx, func() error {
			return p.dispatchInner(childCtx, names, ancestors)
		})
	}
	return p.dispatchInner(ctx, names, ancestors)
}

func (p *Pool) dispatchInner(ctx context.Context, names []string, ancestors []string) error {
	memo := TargetMemoFromContext(ctx)

	type work struct {
		name   string
		ch     <-chan error // set if submitted to pool
		waitFn func() error // set if subscribing to in-flight entry
		err    error        // set if resolved immediately (e.g. a cycle)
	}
	works := make([]work, len(names))
	for i, name := range names {
		// A target that names one of its own ancestors is a dependency cycle.
		// This must be caught here, before the memo: TryRun would subscribe to the
		// still-running ancestor's waitFn, and since the ancestor is blocked waiting
		// on us, the two would deadlock instead of erroring. (Submit guards the
		// non-memo path; this guards the memo path.)
		if slices.Contains(ancestors, name) {
			works[i] = work{name: name, err: fmt.Errorf("buzzpool: dispatch: stack contains %q (cycle detected)", name)}
			continue
		}
		if memo != nil {
			isNew, waitFn := memo.TryRun(name)
			if !isNew {
				works[i] = work{name: name, waitFn: waitFn}
				continue
			}
			ch := p.submitWithMemo(ctx, name, ancestors, memo)
			works[i] = work{name: name, ch: ch}
			continue
		}
		works[i] = work{name: name, ch: p.Submit(ctx, name, ancestors)}
	}

	var errs []error
	for _, w := range works {
		var err error
		switch {
		case w.err != nil:
			err = w.err
		case w.ch != nil:
			err = <-w.ch
		case w.waitFn != nil:
			err = w.waitFn()
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", w.name, err))
		}
	}
	return errors.Join(errs...)
}

// submitWithMemo wraps Submit so the result is recorded in memo on completion.
func (p *Pool) submitWithMemo(ctx context.Context, name string, ancestors []string, memo *TargetMemo) <-chan error {
	inner := p.Submit(ctx, name, ancestors)
	ch := make(chan error, 1)
	go func() {
		err := <-inner
		memo.Complete(name, err)
		ch <- err
	}()
	return ch
}

func (p *Pool) execute(ctx context.Context, name string, ancestors []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	sem := p.getSemFrom(ctx)
	if sem != nil {
		if err := sem.Acquire(ctx); err != nil {
			return err
		}
		defer sem.Release()
		ctx = withBuzzSlot(ctx)
	}

	w, err := p.acquireWorker(ctx)
	if err != nil {
		return fmt.Errorf("buzzpool: worker: %w", err)
	}
	defer p.releaseWorker(w)

	ctx = withBuzzAncestors(ctx, append(ancestors, name))

	fn, ok := w.targets[name]
	if !ok {
		return fmt.Errorf("buzzpool: target %q not found", name)
	}
	if fn == nil {
		return nil // parse mode stub
	}
	_, err = fn(ctx, nil)
	return err
}

func (p *Pool) getSemFrom(ctx context.Context) Semaphore {
	if p.getSem == nil {
		return nil
	}
	return p.getSem(ctx)
}

func (p *Pool) acquireWorker(ctx context.Context) (*poolWorker, error) {
	p.mu.Lock()
	if n := len(p.idle); n > 0 {
		w := p.idle[n-1]
		p.idle[n-1] = nil
		p.idle = p.idle[:n-1]
		p.mu.Unlock()
		return w, nil
	}
	p.mu.Unlock()
	return p.newWorker(ctx)
}

func (p *Pool) releaseWorker(w *poolWorker) {
	p.mu.Lock()
	if !p.closed && len(p.idle) < p.capacity {
		p.idle = append(p.idle, w)
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()
	_ = w.sess.Close()
}

func (p *Pool) newWorker(ctx context.Context) (*poolWorker, error) {
	sess, targets, err := p.newSession(ctx)
	if err != nil {
		return nil, err
	}
	return &poolWorker{sess: sess, targets: targets}, nil
}

// Close shuts down all idle sessions after in-flight jobs finish and release theirs.
func (p *Pool) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()

	p.wg.Wait()

	p.mu.Lock()
	idle := p.idle
	p.idle = nil
	p.mu.Unlock()

	var errs []error
	for _, w := range idle {
		if err := w.sess.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// --- package-private context keys ---

type buzzSlotKey struct{}
type buzzAncestorKey struct{}

func withBuzzSlot(ctx context.Context) context.Context {
	return context.WithValue(ctx, buzzSlotKey{}, true)
}

func withoutBuzzSlot(ctx context.Context) context.Context {
	return context.WithValue(ctx, buzzSlotKey{}, false)
}

func buzzSlotHeld(ctx context.Context) bool {
	v, _ := ctx.Value(buzzSlotKey{}).(bool)
	return v
}

func withBuzzAncestors(ctx context.Context, stack []string) context.Context {
	return context.WithValue(ctx, buzzAncestorKey{}, stack)
}

// AncestorsFromContext returns the current dispatch ancestor stack stored by the pool.
func AncestorsFromContext(ctx context.Context) []string {
	v, _ := ctx.Value(buzzAncestorKey{}).([]string)
	return v
}
