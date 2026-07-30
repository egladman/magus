package types

import (
	"context"
	"sync"
)

// Returns holds one target's return value per project path. That is the shape
// every consumer wants, because a run selects ONE target across a project set;
// the sink underneath is keyed more finely (see [returnKey]).
type Returns map[string]any

// returnKey identifies the single target invocation a value belongs to.
//
// The project path alone is not enough. Two targets can run against one project
// in a single process, and a project-keyed sink lets the second target's
// snapshot inherit the first target's value - written into a DURABLE cache
// entry, so the wrong value then replays on every later hit.
type returnKey struct {
	project string
	target  string
}

// returnCapture collects each (project, target) return value during one run.
type returnCapture struct {
	mu   sync.Mutex
	vals map[returnKey]any
}

type returnCaptureKey struct{}

// WithReturnCapture installs a sink for target return values and returns a
// reader that narrows the collected set to one target.
//
// This mirrors [WithExitCapture], with one difference that matters: a run fans
// out across projects concurrently, so the sink is mutex-guarded and the reader
// hands back a copy. The exit capture needs neither, because it is scoped to a
// single target invocation on one goroutine.
//
// Callers that only need the sink installed - every engine entry point, so that
// the cache snapshots a value regardless of who dispatched the run - want
// [EnsureReturnCapture] instead.
func WithReturnCapture(ctx context.Context) (context.Context, func(target string) Returns) {
	r := &returnCapture{vals: map[returnKey]any{}}
	return context.WithValue(ctx, returnCaptureKey{}, r), func(target string) Returns {
		r.mu.Lock()
		defer r.mu.Unlock()
		out := Returns{}
		for k, v := range r.vals {
			if k.target == target {
				out[k.project] = v
			}
		}
		return out
	}
}

// EnsureReturnCapture installs a sink only when ctx has none, and is what the
// engine calls on the way in.
//
// Without it the sink existed only where the CLI happened to install it (`run`
// and `affected`), so every other entry point - the MCP run tool, `magus x`,
// the merge driver, the symbol indexer - executed with no sink and the cache
// snapshotted Value: nil into a durable entry. Warming a target through MCP and
// then asking `magus run <target> -o json` for its value reported none, because
// the entry that replayed had never recorded one.
//
// It leaves an existing sink alone: the CLI installs its own so it can read the
// values back, and a second sink deeper in would collect them where nobody looks.
func EnsureReturnCapture(ctx context.Context) context.Context {
	if _, ok := ctx.Value(returnCaptureKey{}).(*returnCapture); ok {
		return ctx
	}
	ctx, _ = WithReturnCapture(ctx)
	return ctx
}

// RecordReturn records what project's target invocation returned, if a sink is
// present. It is a no-op outside a captured run.
//
// A nil value is dropped rather than stored, and that is load-bearing twice
// over. It keeps a `> void` target - nearly every target that exists - from
// making "returned null" indistinguishable from "returned nothing at all",
// which is the difference between a field being absent and being
// present-but-null in the rendered result. It also lets the several spells
// serving one target fan in: the spell that returned something wins over the
// ones that returned nothing, whatever order they finish in. Do NOT "fix" this
// into clearing the key - that would have the last void spell erase the value a
// sibling produced. Staleness across DIFFERENT targets is prevented by the key,
// not by clearing.
func RecordReturn(ctx context.Context, project, target string, v any) {
	if v == nil {
		return
	}
	r, ok := ctx.Value(returnCaptureKey{}).(*returnCapture)
	if !ok {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.vals[returnKey{project: project, target: target}] = normalizeReturn(v)
}

// normalizeReturn restores the Go type a fresh run produces, for a value that came
// back through the cache manifest.
//
// A [str] return is a []string on the run that executed the target, but the same
// value replayed from a cache HIT has been through JSON and arrives as []any. Left
// alone, the two runs render differently - the text form printed "[alpha beta]" for
// the replay where the fresh run printed one item per line - which defeats the point
// of storing it, since a hit is supposed to be indistinguishable from a miss.
func normalizeReturn(v any) any {
	items, ok := v.([]any)
	if !ok {
		return v
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		s, ok := it.(string)
		if !ok {
			return v // not the [str] shape; hand it back untouched
		}
		out = append(out, s)
	}
	return out
}

// ReturnFor reads back what project's target invocation recorded, for the cache
// to store alongside the entry it is snapshotting.
//
// The cache needs this because a HIT never invokes the target: without the value
// on the entry, a target would print its result on the first run and nothing on
// the second, which is worse than not returning values at all. Store on snapshot,
// re-capture on replay, and the two runs agree.
func ReturnFor(ctx context.Context, project, target string) (any, bool) {
	r, ok := ctx.Value(returnCaptureKey{}).(*returnCapture)
	if !ok {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.vals[returnKey{project: project, target: target}]
	return v, ok
}
