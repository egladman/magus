package cache

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/libs/safetyscissors/journal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// eventSink is a capture handler that keeps every journal event a run emitted, so a test
// can assert on the RECORD an observer would read rather than on a log line.
type eventSink struct {
	mu      sync.Mutex
	records []journal.Record
}

func (s *eventSink) Handle(_ context.Context, r journal.Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, r)
}

// runs is the result records the sink kept, in order.
func (s *eventSink) runs() []journal.Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []journal.Run
	for _, r := range s.records {
		if run, ok := r.Run(); ok {
			out = append(out, run)
		}
	}
	return out
}

// TestRunAsideIsAccountedLikeABatchStep is the whole point of RunAside: work that runs
// OUTSIDE the batch has to appear where a batch step appears. A settle re-run dispatched
// straight at the interpreter held no slot, claimed nothing from the machine budget,
// joined no inflight set and emitted no journal event, so every observer reported an idle
// machine while every project lock stayed held.
//
// Asserted on the recorded state and events, not on prose: the inflight set and the
// limiter are read from INSIDE fn (they are unwound by the time it returns), and the
// journal events are read after.
func TestRunAsideIsAccountedLikeABatchStep(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main\nfunc main() {}\n")
	s := makeStep(root)
	s.Target = "settle"
	s.NoCache = true // a settle re-run is always a skip-cache target

	sink := &eventSink{}
	ctx := recorder.WithSinks(withResolver(t.Context()), sink.Handle)
	ctx = recorder.WithInvocationID(ctx, "invtest")
	prog := NewProgress()
	ctx = ContextWithProgress(ctx, prog)

	lim := NewLimiter(4)
	var (
		running   []inflightTarget
		heldSlots int
		markInFn  Mark
	)
	_, err := c.RunAside(ctx, s, func(context.Context) error {
		running = c.inflight.Running()
		heldSlots = lim.Snapshot().Running
		markInFn = prog.Last()
		return nil
	}, WithLimiter(lim))
	require.NoError(t, err, "RunAside")

	require.Len(t, running, 1, "the step is in the inflight set while it runs")
	assert.Equal(t, "test/pkg", running[0].Project)
	assert.Equal(t, "settle", running[0].Target)
	assert.Equal(t, 1, heldSlots, "the step holds a limiter slot while it runs")
	assert.Equal(t, "test/pkg", markInFn.Project, "the heartbeat names the running step")
	assert.NotEmpty(t, markInFn.Log, "the heartbeat carries the captured log path")

	assert.Empty(t, c.inflight.Running(), "the inflight record is cleared on the way out")
	assert.Equal(t, 0, lim.Snapshot().Running, "the slot is handed back on the way out")

	results := sink.runs()
	require.Len(t, results, 1, "one journal result event for the off-batch step")
	assert.NotEmpty(t, results[0].Ref, "the event carries the output ref its log is stored under")
	assert.Equal(t, journal.Run{
		Project: "test/pkg", Target: "settle", Level: "info", Status: journal.StatusPass,
		Ref: results[0].Ref, Duration: results[0].Duration, CacheKey: results[0].CacheKey,
		Sizing: journal.SizingNone, MaxRSSBytes: results[0].MaxRSSBytes, CPU: results[0].CPU,
		Started: results[0].Started, Waited: results[0].Waited,
	}, results[0])
}

// TestRunAsideReportsFailureLikeABatchStep pins the other half: a failing off-batch step
// must reach the journal too, or a settle that fails is as invisible as one that hangs.
func TestRunAsideReportsFailureLikeABatchStep(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main\nfunc main() {}\n")
	s := makeStep(root)
	s.Target = "settle"
	s.NoCache = true

	sink := &eventSink{}
	ctx := recorder.WithSinks(withResolver(t.Context()), sink.Handle)

	_, err := c.RunAside(ctx, s, func(context.Context) error {
		return assert.AnError
	}, WithLimiter(NewLimiter(2)))
	require.Error(t, err, "RunAside surfaces the step's error")

	var failed bool
	for _, e := range sink.runs() {
		if e.Status == journal.StatusFail {
			failed = true
			assert.Equal(t, "settle", e.Target)
		}
	}
	assert.True(t, failed, "the failure is recorded as a result event")
}

// TestProgressBeatsOnEveryCapturedLine pins the signal that separates a slow target from
// a wedged one. Without it the watchdog would fire on any target that runs longer than
// its window without finishing, which is worse than no watchdog at all.
func TestProgressBeatsOnEveryCapturedLine(t *testing.T) {
	prog := NewProgress()
	ctx := ContextWithProgress(t.Context(), prog)
	em := newLineEmitter(ctx, "test/pkg", "build")

	prog.at.Store(time.Now().Add(-time.Hour).UnixNano())
	require.Greater(t, prog.Idle(), 30*time.Minute, "fabricated quiet period")

	em.emit(journal.Stdout, "compiling")
	assert.Less(t, prog.Idle(), time.Second, "an output line is a beat")
}

// TestNilProgressIsInert keeps every accounting edge callable from a context that has no
// heartbeat installed, which is every bare cache.Run in the tree.
func TestNilProgressIsInert(t *testing.T) {
	var p *Progress
	assert.Nil(t, ProgressFromContext(t.Context()))
	p.Beat()
	p.Record(Mark{Project: "a", Target: "b"})
	assert.Equal(t, Mark{}, p.Last())
	assert.Zero(t, p.Idle(), "a nil heartbeat is never idle, so no watchdog can fire on it")
}
