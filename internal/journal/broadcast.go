package journal

import (
	"context"
	"log/slog"
	"sync"
)

// Broadcaster is a capture handler (slog.Handler) that fans each event out to any number of
// live subscribers while retaining a backlog of everything seen so far. It is the engine
// side of live log streaming: the invocation's capture logger includes one Broadcaster, and
// the loopback SSE server (internal/service/viewer) subscribes to it. A browser that connects mid-run
// first replays the backlog (so it sees the run from the start) and then receives new events
// as they are emitted, until [Broadcaster.Close] marks the run finished.
//
// All methods are safe for concurrent use: events arrive from one goroutine per project
// while subscribers come and go on HTTP handler goroutines.
type Broadcaster struct {
	mu      sync.Mutex
	backlog []Event
	subs    map[int]chan Event
	nextID  int
	done    bool
	doneCh  chan struct{}
}

// NewBroadcaster returns an empty Broadcaster ready to accept events and subscribers.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{subs: make(map[int]chan Event), doneCh: make(chan struct{})}
}

// Enabled reports whether the broadcaster still accepts events (false once closed), letting
// the capture logger skip building a record for a finished run.
func (b *Broadcaster) Enabled(context.Context, slog.Level) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return !b.done
}

// Handle records the event a capture record carries into the backlog and delivers it to
// every current subscriber. A subscriber whose buffer is full is skipped for that event
// rather than blocking the run: capture must never slow execution.
func (b *Broadcaster) Handle(_ context.Context, r slog.Record) error {
	e, ok := eventFrom(r)
	if !ok {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.done {
		return nil
	}
	b.backlog = append(b.backlog, e)
	for _, ch := range b.subs {
		select {
		case ch <- e:
		default:
			// Subscriber is behind; it will still get the backlog on reconnect.
		}
	}
	return nil
}

func (b *Broadcaster) WithAttrs([]slog.Attr) slog.Handler { return b }
func (b *Broadcaster) WithGroup(string) slog.Handler      { return b }

// Subscribe registers a new live subscriber. It returns a snapshot of the backlog (the
// events emitted before this call), a channel that receives events emitted after it, and an
// unsubscribe function the caller must invoke when it stops reading. The channel is buffered
// so a briefly-slow reader does not drop events under normal load.
func (b *Broadcaster) Subscribe() (backlog []Event, ch <-chan Event, cancel func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	snapshot := make([]Event, len(b.backlog))
	copy(snapshot, b.backlog)

	c := make(chan Event, 256)
	id := b.nextID
	b.nextID++
	b.subs[id] = c

	return snapshot, c, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if ch, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(ch)
		}
	}
}

// Close marks the run finished: it closes the done channel (which [Done] exposes so a server
// can end its SSE streams) and refuses further events. Subscriber channels are left for
// their readers to drain and cancel. Close is idempotent.
func (b *Broadcaster) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.done {
		return
	}
	b.done = true
	close(b.doneCh)
}

// Done returns a channel closed when the run finishes ([Close] is called), so a live server
// can send a terminal event and stop once no more events will arrive.
func (b *Broadcaster) Done() <-chan struct{} { return b.doneCh }
