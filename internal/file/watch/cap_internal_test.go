package watch

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/assert"
)

// chanNotifier is a notifier whose event stream the test drives directly,
// so the pending-cap behavior can be exercised without depending on real
// fsnotify delivery — which drops events under backpressure (see
// notify_linux.go) and so cannot be relied on to deliver a precise count
// under CPU load.
type chanNotifier struct {
	events chan fsnotify.Event
	errors chan error
}

func (n *chanNotifier) Add(string) error              { return nil }
func (n *chanNotifier) Remove(string) error           { return nil }
func (n *chanNotifier) Events() <-chan fsnotify.Event { return n.events }
func (n *chanNotifier) Errors() <-chan error          { return n.errors }
func (n *chanNotifier) Close() error                  { return nil }

// newTestWatcher assembles a Watcher around fake, bypassing newDefaultNotifier,
// and starts its loop with cfg. It mirrors the channel sizing in New.
func newTestWatcher(ctx context.Context, fake notifier, cfg watchConfig) *Watcher {
	w := &Watcher{
		events:  make(chan Batch, cfg.bufferSize),
		errors:  make(chan error, 16),
		done:    make(chan struct{}),
		n:       fake,
		walkSem: make(chan struct{}, walkWorkers),
		synth:   make(chan string, maxPending*2),
	}
	go w.loop(ctx, cfg)
	return w
}

// TestPendingCapFlushesImmediately verifies that crossing maxPending forces a
// flush rather than waiting for the debounce timer. It drives events through a
// fake notifier so the count is exact and lossless: with a 30s debounce, any
// batch that arrives promptly can only have come from the cap path.
func TestPendingCapFlushesImmediately(t *testing.T) {
	t.Parallel()
	fake := &chanNotifier{
		events: make(chan fsnotify.Event, maxPending*2),
		errors: make(chan error, 16),
	}
	cfg := watchConfig{
		debounce:   30 * time.Second, // long enough that a timely flush must be the cap
		ignore:     func(string) bool { return false },
		bufferSize: 256,
	}
	w := newTestWatcher(context.Background(), fake, cfg)
	defer w.Close()

	// Feed exactly maxPending+8 distinct Write events. Write (not Create) avoids
	// the loop's os.Stat dir probe, keeping the path purely in-memory.
	for i := 0; i < maxPending+8; i++ {
		fake.events <- fsnotify.Event{
			Name: fmt.Sprintf("/virtual/f%04d.txt", i),
			Op:   fsnotify.Write,
		}
	}

	select {
	case batch := <-w.Events():
		assert.GreaterOrEqual(t, len(batch.Paths), maxPending, "cap flush batch too small")
	case <-time.After(5 * time.Second):
		t.Fatal("timeout: no flush received; pending cap is not flushing on overflow")
	}
}
