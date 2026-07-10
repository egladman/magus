package journal

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// emit sends one event to bc through the real capture path (logger -> handler).
func emit(bc *Broadcaster, ev Event) {
	Emit(WithLogger(context.Background(), NewLogger(bc)), ev)
}

// TestBroadcasterBacklogThenLive confirms a subscriber gets the events emitted before it
// subscribed (backlog) and then events emitted after (live), and that Done fires on Close.
func TestBroadcasterBacklogThenLive(t *testing.T) {
	bc := NewBroadcaster()
	emit(bc, Event{Text: "before-1"})
	emit(bc, Event{Text: "before-2"})

	backlog, ch, cancel := bc.Subscribe()
	defer cancel()
	require.Len(t, backlog, 2)
	assert.Equal(t, "before-1", backlog[0].Text)

	emit(bc, Event{Text: "after-1"})
	assert.Equal(t, "after-1", (<-ch).Text)

	select {
	case <-bc.Done():
		t.Fatal("Done should not be closed before Close")
	default:
	}
	bc.Close()
	<-bc.Done() // closed
}

// TestBroadcasterEmitAfterCloseNoop confirms Close is idempotent and an event after Close
// neither panics nor grows the backlog.
func TestBroadcasterEmitAfterCloseNoop(t *testing.T) {
	bc := NewBroadcaster()
	emit(bc, Event{Text: "one"})
	bc.Close()
	bc.Close() // idempotent
	emit(bc, Event{Text: "dropped"})

	backlog, _, cancel := bc.Subscribe()
	defer cancel()
	require.Len(t, backlog, 1)
	assert.Equal(t, "one", backlog[0].Text)
}

// TestBroadcasterMultipleSubscribers confirms each live subscriber receives an emitted event
// and unsubscribing one does not affect the other.
func TestBroadcasterMultipleSubscribers(t *testing.T) {
	bc := NewBroadcaster()
	_, ch1, cancel1 := bc.Subscribe()
	_, ch2, cancel2 := bc.Subscribe()
	defer cancel2()

	emit(bc, Event{Text: "hi"})
	assert.Equal(t, "hi", (<-ch1).Text)
	assert.Equal(t, "hi", (<-ch2).Text)

	cancel1()
	emit(bc, Event{Text: "again"})
	assert.Equal(t, "again", (<-ch2).Text)
}
