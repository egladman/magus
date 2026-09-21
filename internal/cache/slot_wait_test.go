package cache

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSlotWaitMeter pins wall-time union semantics: overlapping waits count once,
// a gap between them counts not at all, and an immediate grant adds only its instant.
func TestSlotWaitMeter(t *testing.T) {
	t0 := time.Unix(1_000, 0)
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }

	var m SlotWaitMeter
	m.addLocked(+1, at(0))  // A queues
	m.addLocked(+2, at(4))  // B queues for two slots, overlapping A
	m.addLocked(-1, at(10)) // A granted
	m.addLocked(-2, at(12)) // B granted
	m.addLocked(+1, at(13)) // C granted at once
	m.addLocked(-1, at(13))
	m.addLocked(+1, at(20)) // D queues after a gap
	m.addLocked(-1, at(25))

	assert.Equal(t, 17*time.Second, m.Waited())
}

// TestSlotWaitMeterGrantOrder pins the case a per-grant interval sweep lost: one release
// wakes A and B, and B's hook reaches the meter first. The wait still runs from A's start.
func TestSlotWaitMeterGrantOrder(t *testing.T) {
	t0 := time.Unix(1_000, 0)
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }

	var m SlotWaitMeter
	m.addLocked(+1, at(0))     // A queues
	m.addLocked(+1, at(4000))  // B queues
	m.addLocked(-1, at(10001)) // B's grant arrives first
	m.addLocked(-1, at(10002)) // then A's

	assert.Equal(t, 10002*time.Millisecond, m.Waited())
}

// TestSlotWaitMeterFedByLimiter pins the wiring: a caller blocked behind a held slot is
// measured, and the queue drains back to empty.
func TestSlotWaitMeterFedByLimiter(t *testing.T) {
	var m SlotWaitMeter
	lim := NewLimiter(1)
	require.NoError(t, lim.Acquire(context.Background()))

	queued := make(chan struct{})
	lim.SetHooks(nil, nil, func(delta int) {
		m.Add(delta)
		if delta > 0 {
			close(queued)
		}
	})
	acquired := make(chan error, 1)
	go func() { acquired <- lim.Acquire(context.Background()) }()
	<-queued
	const held = 50 * time.Millisecond
	time.Sleep(held)
	lim.Release()
	require.NoError(t, <-acquired)
	lim.Release()

	assert.GreaterOrEqual(t, m.Waited(), held)
	assert.Equal(t, 0, m.depth)
}
