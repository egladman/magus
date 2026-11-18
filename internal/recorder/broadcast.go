package recorder

import (
	"context"
	"sync"

	"github.com/egladman/magus/libs/safetyscissors/journal"
)

// Broadcaster keeps what it has seen and fans each record out to live
// subscribers. A subscriber that is behind misses a record rather than
// blocking the run. internal/handler/viewer subscribes to it: a browser that
// connects mid-run replays the backlog, then receives new records until
// [Broadcaster.Close] marks the run finished.
//
// All methods are safe for concurrent use: events arrive from one goroutine per project
// while subscribers come and go on HTTP handler goroutines.
type Broadcaster struct {
	mu      sync.Mutex
	backlog []journal.Record
	subs    map[int]chan journal.Record
	nextID  int
	done    bool
	doneCh  chan struct{}
}

// NewBroadcaster returns an empty Broadcaster ready to accept events and subscribers.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{subs: make(map[int]chan journal.Record), doneCh: make(chan struct{})}
}

// Handle records r into the backlog and delivers it to every current
// subscriber; it is a [Sink]. A subscriber whose buffer is full is skipped for
// that record rather than blocking the run.
func (b *Broadcaster) Handle(_ context.Context, r journal.Record) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.done {
		return
	}
	b.backlog = append(b.backlog, r)
	for _, ch := range b.subs {
		select {
		case ch <- r:
		default:
			// Subscriber is behind; it will still get the backlog on reconnect.
		}
	}
}

// Subscribe registers a new live subscriber. It returns a snapshot of the backlog (the
// events emitted before this call), a channel that receives events emitted after it, and an
// unsubscribe function the caller must invoke when it stops reading. The channel is buffered
// so a briefly-slow reader does not drop events under normal load.
func (b *Broadcaster) Subscribe() (backlog []journal.Record, ch <-chan journal.Record, cancel func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	snapshot := make([]journal.Record, len(b.backlog))
	copy(snapshot, b.backlog)

	c := make(chan journal.Record, 256)
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
