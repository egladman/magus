package watch

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Debounce coalescing, asserted exactly.
//
// This drives synthetic events through chanNotifier for the same reason
// TestPendingCapFlushesImmediately does, stated in that file: real fsnotify drops
// events under backpressure and cannot deliver a precise count under CPU load. An
// earlier version of this assertion went through the real filesystem and needed a
// warm-up write, a drain, a dropped t.Parallel, and three tuned constants
// (100ms/300ms/10s) - and still failed in CI. It was measuring the OS scheduler.
//
// The debounce here is longer than the test can possibly take, so the timer is
// guaranteed NOT to fire mid-burst. Closing the event channel then makes loop
// flush whatever is pending as one final batch. That turns "10 rapid events
// coalesce into one batch" into an exact equality with no wall-clock in it: if
// coalescing broke, the count is 10, not "more than 3".
func TestDebounceCoalescesBurstIntoOneBatch(t *testing.T) {
	const burst = 10

	fake := &chanNotifier{
		events: make(chan fsnotify.Event, burst),
		errors: make(chan error, 1),
	}
	cfg := watchConfig{
		debounce:   time.Hour, // never fires; the close below is what flushes
		bufferSize: 8,
		ignore:     func(string) bool { return false },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newTestWatcher(ctx, fake, cfg)

	for range burst {
		fake.events <- fsnotify.Event{Name: "/tmp/a.go", Op: fsnotify.Write}
	}
	close(fake.events)

	batch, ok := <-w.Events()
	require.True(t, ok, "closing the event stream must flush the pending set")
	assert.Equal(t, []string{"/tmp/a.go"}, batch.Paths,
		"%d writes to one path must coalesce into a single deduplicated entry", burst)

	_, more := <-w.Events()
	assert.False(t, more, "the burst must produce exactly one batch, not one per event")
}

// Distinct paths in one window are coalesced into a single batch too, and every
// path survives. Deduplication must not become deletion.
func TestDebounceKeepsEveryDistinctPathInOneBatch(t *testing.T) {
	const paths = 5

	fake := &chanNotifier{
		events: make(chan fsnotify.Event, paths*2),
		errors: make(chan error, 1),
	}
	cfg := watchConfig{
		debounce:   time.Hour,
		bufferSize: 8,
		ignore:     func(string) bool { return false },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := newTestWatcher(ctx, fake, cfg)

	want := make([]string, 0, paths)
	for i := range paths {
		p := fmt.Sprintf("/tmp/f%d.go", i)
		want = append(want, p)
		// Twice each, so dedup and coalescing are both exercised.
		fake.events <- fsnotify.Event{Name: p, Op: fsnotify.Write}
		fake.events <- fsnotify.Event{Name: p, Op: fsnotify.Write}
	}
	close(fake.events)

	batch, ok := <-w.Events()
	require.True(t, ok)
	assert.ElementsMatch(t, want, batch.Paths,
		"each path appears once, and none is dropped")
}
