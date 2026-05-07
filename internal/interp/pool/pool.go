// Package pool implements a per-magusfile pool of pre-warmed Lua VMs.
// Each worker VM has loaded tl.lua, host bindings, and the magusfile, so
// dispatching costs a Lua function call rather than a subprocess fork.
package pool

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"sync"

	buzz "github.com/egladman/gopherbuzz"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/interp/engine"
	lua "github.com/egladman/magus/internal/interp/engine/lua"
)

const maxDepth = 64

// Result is the outcome of a single Submit call.
type Result struct {
	Err error
}

// worker owns one Lua VM and runs one job at a time.
//
// A worker's VM is warmed once and reused across jobs, so target functions share
// one Lua global environment for the worker's lifetime: a target that mutates a
// global (rather than its own locals) can be observed by a later target that
// runs on the same VM. Targets must therefore not rely on mutating shared globals
// to communicate — the engine-agnostic contract is that a target is a pure unit
// of work keyed by name. Per-job _G isolation is intentionally not done; it would
// defeat the pre-warm that makes pool dispatch a function call rather than a fork.
type worker struct {
	runner  lua.Session
	targets map[string]engine.Value // snapshot of _magus_targets at init; immutable after ExecSource
}

// Pool is a per-magusfile pool of pre-warmed Lua VMs; safe for concurrent use.
//
// Each Submit runs on its own goroutine: it acquires a global limiter slot
// (bounding real parallelism and VM count), checks out a warmed VM from the
// free-list (creating one on demand), runs the target, then returns the VM.
// Because there is no fixed set of worker goroutines, a target that dispatches
// children and blocks until they finish never starves the children of a
// goroutine to run on — so nested dispatch cannot deadlock regardless of depth
// or fan-out. Parallelism (and thus live VM count) is bounded by the limiter,
// which nested dispatch yields (WithSlotHeld via proc.RunChildSync) so a child
// can acquire the slot its parent holds, even at MAGUS_CONCURRENCY=1.
type Pool struct {
	src      *interp.Source
	capacity int // soft cap on idle VMs retained in the free-list for reuse

	mu     sync.Mutex // guards idle and closed
	idle   []*worker  // free-list of warmed VMs available for reuse
	closed bool
	wg     sync.WaitGroup // tracks in-flight Submit goroutines for Close to await
}

// New creates a Pool for src (VMs are created lazily on first Submit). 0 capacity = NumCPU.
func New(src *interp.Source, capacity int) *Pool {
	if capacity <= 0 {
		capacity = runtime.NumCPU()
	}
	return &Pool{src: src, capacity: capacity}
}

// Submit dispatches name and returns a channel delivering one Result.
// Returns a closed-error result (not a panic) if the pool is already closed.
func (p *Pool) Submit(ctx context.Context, name string, ancestorStack []string) <-chan Result {
	ch := make(chan Result, 1)

	if slices.Contains(ancestorStack, name) {
		ch <- Result{Err: fmt.Errorf("pool: dispatch: stack contains %q (cycle detected)", name)}
		return ch
	}
	if len(ancestorStack) >= maxDepth {
		ch <- Result{Err: fmt.Errorf("pool: dispatch: depth exceeded (%d)", maxDepth)}
		return ch
	}

	// Register the in-flight job under the lock that Close uses, so no goroutine
	// is launched after Close has begun draining (and Close awaits this one).
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		ch <- Result{Err: errors.New("pool: closed")}
		return ch
	}
	p.wg.Add(1)
	p.mu.Unlock()

	go func() {
		defer p.wg.Done()
		ch <- p.execute(ctx, name, ancestorStack)
	}()
	return ch
}

// Dispatch fans names out through the pool and joins their errors. When a
// [buzz.TargetMemo] is present in ctx, a target already in-flight is subscribed
// to rather than re-submitted, so a dependency shared by several callers (a
// diamond) runs at most once per invocation; the subscriber's waitFn blocks
// without holding a slot, so it cannot deadlock. The caller is responsible for
// yielding its own limiter slot (see [proc.RunChildSync]) before calling
// Dispatch so the children it spawns can acquire slots.
func (p *Pool) Dispatch(ctx context.Context, names []string, ancestors []string) error {
	if len(names) == 0 {
		return nil
	}
	memo := buzz.TargetMemoFromContext(ctx)

	type work struct {
		name   string
		ch     <-chan Result // set when submitted to the pool
		waitFn func() error  // set when subscribing to an in-flight entry
	}
	works := make([]work, len(names))
	for i, name := range names {
		if memo != nil {
			isNew, waitFn := memo.TryRun(name)
			if !isNew {
				works[i] = work{name: name, waitFn: waitFn}
				continue
			}
			works[i] = work{name: name, ch: p.submitWithMemo(ctx, name, ancestors, memo)}
			continue
		}
		works[i] = work{name: name, ch: p.Submit(ctx, name, ancestors)}
	}

	var errs []error
	for _, w := range works {
		var err error
		if w.ch != nil {
			err = (<-w.ch).Err
		} else if w.waitFn != nil {
			err = w.waitFn()
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", w.name, err))
		}
	}
	return errors.Join(errs...)
}

// submitWithMemo wraps Submit so the result is recorded in memo on completion,
// unblocking any subscribers waiting on this target.
func (p *Pool) submitWithMemo(ctx context.Context, name string, ancestors []string, memo *buzz.TargetMemo) <-chan Result {
	inner := p.Submit(ctx, name, ancestors)
	ch := make(chan Result, 1)
	go func() {
		res := <-inner
		memo.Complete(name, res.Err)
		ch <- res
	}()
	return ch
}

// execute acquires a limiter slot, checks out a VM, runs name, then releases both.
// The slot is acquired before the VM so a wide fan-out cannot spawn unbounded VMs:
// at most limiter-capacity jobs hold a slot (and therefore a VM) at once. Nested
// dispatch yields the slot (WithSlotHeld), so a parent blocked on its children does
// not pin the budget — and, with no fixed worker pool, never pins a goroutine either.
func (p *Pool) execute(ctx context.Context, name string, ancestors []string) Result {
	if err := ctx.Err(); err != nil {
		return Result{Err: err}
	}

	if lim := cache.LimiterFromContext(ctx); lim != nil {
		if err := lim.Acquire(ctx); err != nil {
			return Result{Err: err}
		}
		defer lim.Release()
		ctx = cache.WithSlotHeld(ctx)
	}

	w, err := p.acquireWorker(ctx)
	if err != nil {
		return Result{Err: fmt.Errorf("pool: worker init: %w", err)}
	}
	defer p.releaseWorker(w)

	var runErr error
	func() {
		defer func() {
			if r := recover(); r != nil {
				runErr = fmt.Errorf("pool: worker panic: %v", r)
			}
		}()
		runErr = p.runJob(ctx, name, ancestors, w)
	}()
	return Result{Err: runErr}
}

// acquireWorker returns a warmed VM from the free-list, or creates one on demand.
func (p *Pool) acquireWorker(ctx context.Context) (*worker, error) {
	p.mu.Lock()
	if n := len(p.idle); n > 0 {
		w := p.idle[n-1]
		p.idle[n-1] = nil
		p.idle = p.idle[:n-1]
		p.mu.Unlock()
		return w, nil
	}
	p.mu.Unlock()
	return p.newWorker(ctx) // expensive (chdir + compile + exec); done outside the lock
}

// releaseWorker returns w to the free-list for reuse, or closes it when the pool
// is closed or the free-list is already at capacity (the high-water mark of
// concurrent jobs can exceed capacity during nested dispatch).
func (p *Pool) releaseWorker(w *worker) {
	p.mu.Lock()
	if !p.closed && len(p.idle) < p.capacity {
		p.idle = append(p.idle, w)
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()
	_ = w.runner.Close()
}

// runJob invokes name on w. ctx already carries the (held) limiter slot; it is
// threaded into the VM so nested dispatch sees the ancestor chain and yields.
func (p *Pool) runJob(ctx context.Context, name string, ancestors []string, w *worker) error {
	if cs, ok := w.runner.(engine.ContextSetter); ok {
		chain := make([]string, len(ancestors)+1)
		copy(chain, ancestors)
		chain[len(ancestors)] = name
		cs.SetContext(WithAncestors(ctx, chain))
	}

	key := interp.NormalizeTarget(ctx, name)
	fn, ok := w.targets[key]
	if !ok {
		return fmt.Errorf("pool: target %q not found in worker VM", name)
	}
	emptyArgs := w.runner.NewTable()
	if err := w.runner.Call(engine.CallParams{Fn: fn, NRet: 0, Protect: true}, emptyArgs); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// Close shuts down all idle VMs after in-flight jobs finish and release theirs.
func (p *Pool) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()

	p.wg.Wait() // in-flight jobs finish; their releaseWorker closes the VM (closed=true)

	p.mu.Lock()
	idle := p.idle
	p.idle = nil
	p.mu.Unlock()

	var errs []error
	for _, w := range idle {
		if err := w.runner.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (p *Pool) newWorker(ctx context.Context) (*worker, error) {
	r, err := interp.NewLuaSession(ctx)
	if err != nil {
		return nil, fmt.Errorf("pool: new runner: %w", err)
	}
	if err := interp.InstallReplPrelude(ctx, r); err != nil {
		_ = r.Close()
		return nil, fmt.Errorf("pool: install prelude: %w", err)
	}
	r.SetGlobal("_magus_targets", r.NewTable())
	if err := interp.ExecSource(ctx, r, p.src); err != nil {
		_ = r.Close()
		return nil, fmt.Errorf("pool: exec %s: %w", p.src.Dir, err)
	}
	// Snapshot target callables; _magus_targets is fully populated and immutable after ExecSource.
	targets := make(map[string]engine.Value)
	if registry, ok := r.GetGlobal("_magus_targets").AsTable(); ok {
		registry.ForEach(func(k, v engine.Value) {
			if name, ok := k.AsString(); ok {
				targets[name] = v
			}
		})
	}

	return &worker{runner: r, targets: targets}, nil
}

// Registry maps source directory to a lazily created *Pool.
type Registry struct {
	mu       sync.Mutex
	pools    map[string]*Pool
	capacity int
}

// NewRegistry returns an empty Registry; capacity is forwarded to each Pool.
func NewRegistry(capacity int) *Registry {
	return &Registry{pools: make(map[string]*Pool), capacity: capacity}
}

// Get returns the Pool for src, creating it lazily if it does not exist.
// The key includes the engine, not just the directory: a single directory may
// hold coexisting magusfiles for different engines (FindAll supports .tl + .bzz
// side by side), and each engine needs its own pool of matching VMs.
func (r *Registry) Get(src *interp.Source) *Pool {
	key := src.Dir + "\x00" + src.Engine
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.pools[key]; ok {
		return p
	}
	p := New(src, r.capacity)
	r.pools[key] = p
	return p
}

// Close closes every Pool in the registry.
func (r *Registry) Close() error {
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

type registryKey struct{}

// WithRegistry stores reg in ctx for retrieval inside the Lua call stack.
func WithRegistry(ctx context.Context, reg *Registry) context.Context {
	return context.WithValue(ctx, registryKey{}, reg)
}

// RegistryFromContext retrieves the Registry stored by WithRegistry, or nil.
func RegistryFromContext(ctx context.Context) *Registry {
	v, _ := ctx.Value(registryKey{}).(*Registry)
	return v
}

type ancestorKey struct{}

// WithAncestors stores the current dispatch stack in ctx.
func WithAncestors(ctx context.Context, stack []string) context.Context {
	return context.WithValue(ctx, ancestorKey{}, stack)
}

// AncestorsFromContext retrieves the ancestor stack, or nil.
func AncestorsFromContext(ctx context.Context) []string {
	v, _ := ctx.Value(ancestorKey{}).([]string)
	return v
}

// WithAncestor returns a new context with name appended to the ancestor stack.
func WithAncestor(ctx context.Context, name string) context.Context {
	existing := AncestorsFromContext(ctx)
	next := make([]string, len(existing)+1)
	copy(next, existing)
	next[len(existing)] = name
	return WithAncestors(ctx, next)
}
