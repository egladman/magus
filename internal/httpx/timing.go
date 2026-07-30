package httpx

import (
	"context"
	"sync/atomic"
	"time"
)

// Why measure this at all: a target's wall clock says how long you waited, not what
// you waited ON. "6.1s" reads as a slow build; "6.1s, 4.2s of it fetching the remote
// cache" reads as a slow network, and those have opposite fixes. Without the split,
// a flaky link is indistinguishable from a genuinely expensive build, and people
// chase the wrong one.
//
// This measures only what magus itself does. A subprocess (go, pnpm, buf) does its own
// I/O behind an opaque wall-clock number, and nothing here can see it - so the honest
// framing is "magus's time versus your toolchain's time", never "network versus compute".
// Reporting a subprocess-heavy target as 0% network would be worse than reporting nothing.

// Recorder accumulates time spent waiting on magus's own remote I/O for one unit of
// work. Safe for concurrent use: a target may fan out several fetches at once.
type Recorder struct {
	nanos atomic.Int64
	calls atomic.Int64
}

// Add records one completed remote operation.
func (r *Recorder) Add(d time.Duration) {
	if r == nil {
		return
	}
	r.nanos.Add(int64(d))
	r.calls.Add(1)
}

// Elapsed is the total time recorded. Concurrent fetches are summed, not merged, so
// this is time-spent-waiting rather than wall clock, and it can exceed the target's
// own duration when several run in parallel.
func (r *Recorder) Elapsed() time.Duration {
	if r == nil {
		return 0
	}
	return time.Duration(r.nanos.Load())
}

// Calls is how many remote operations were recorded.
func (r *Recorder) Calls() int64 {
	if r == nil {
		return 0
	}
	return r.calls.Load()
}

type recorderKey struct{}

// WithRecorder returns ctx carrying rec, so instrumentation deep in a call chain can
// attribute its time without every layer between passing a parameter it does not use.
func WithRecorder(ctx context.Context, rec *Recorder) context.Context {
	return context.WithValue(ctx, recorderKey{}, rec)
}

// RecorderFrom returns the Recorder on ctx, or nil when none is installed. A nil
// Recorder is usable: every method tolerates it, so uninstrumented paths cost nothing
// and need no guard.
func RecorderFrom(ctx context.Context) *Recorder {
	rec, _ := ctx.Value(recorderKey{}).(*Recorder)
	return rec
}
