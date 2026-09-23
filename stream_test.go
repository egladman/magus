package magus

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReadBatches_NewlineMode verifies that blank-line-delimited batches are
// correctly split and sent on the channel.
func TestReadBatches_NewlineMode(t *testing.T) {
	t.Parallel()
	input := "a\nb\n\nc\nd\n\n"
	ch := readBatches(context.Background(), strings.NewReader(input), false)
	var got [][]string
	for batch := range ch {
		got = append(got, batch)
	}
	want := [][]string{{"a", "b"}, {"c", "d"}}
	assert.Equal(t, want, got)
}

// TestReadBatches_TrailingNoBoundary verifies that a trailing batch with no
// terminating blank line is still flushed at EOF.
func TestReadBatches_TrailingNoBoundary(t *testing.T) {
	t.Parallel()
	input := "x\ny"
	ch := readBatches(context.Background(), strings.NewReader(input), false)
	var got [][]string
	for batch := range ch {
		got = append(got, batch)
	}
	assert.Equal(t, [][]string{{"x", "y"}}, got)
}

// TestReadBatches_NullMode verifies NUL-separated batches with double-NUL boundaries.
// The format mirrors watch.go: each path is NUL-terminated, then a double-NUL
// batchSep follows the last path in each batch. So batch ["a","b"] followed by
// ["c"] encodes as:
//
//	a\x00 b\x00 \x00\x00 c\x00 \x00\x00
func TestReadBatches_NullMode(t *testing.T) {
	t.Parallel()
	input := "a\x00b\x00\x00\x00c\x00\x00\x00"
	ch := readBatches(context.Background(), bytes.NewBufferString(input), true)
	var got [][]string
	for batch := range ch {
		got = append(got, batch)
	}
	want := [][]string{{"a", "b"}, {"c"}}
	assert.Equal(t, want, got)
}

// TestReadBatches_ContextCancel verifies that the channel is closed when ctx
// is cancelled before the reader yields any data.
func TestReadBatches_ContextCancel(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pr, pw := io.Pipe()
	defer pw.Close()
	ch := readBatches(ctx, pr, false)
	for range ch {
		// drain any items that may have squeezed through before cancel
	}
	// Channel must be closed; we must not block here.
}

// The leak this closes: the ctx checks inside readBatches only run BETWEEN reads, so a
// cancelled stream whose input had merely gone quiet left the reader goroutine parked in
// a blocking read that no cancellation could reach.
func TestReadBatches_CancelUnblocksAParkedRead(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe()
	defer pw.Close()

	ch := readBatches(ctx, pr, false) // nothing is ever written, so the read parks
	cancel()

	select {
	case _, ok := <-ch:
		assert.False(t, ok, "the channel closes once the parked read is released")
	case <-time.After(5 * time.Second):
		t.Fatal("readBatches stayed parked in a read after its ctx was cancelled")
	}
}

// TestStream_ContextCancellation verifies that Stream returns nil when ctx is
// already cancelled on entry.
func TestStream_ContextCancellation(t *testing.T) {
	t.Parallel()
	m := &Magus{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pr, pw := io.Pipe()
	pw.Close()
	assert.NoError(t, m.Stream(ctx, pr, "build", nil), "Stream with cancelled ctx")
}

// TestStream_EmptyBatchSkipped verifies that an input containing only blank
// lines produces no handler invocations (and no error).
func TestStream_EmptyBatchSkipped(t *testing.T) {
	t.Parallel()
	m := &Magus{}
	var errCalled bool
	pr, pw := io.Pipe()
	go func() {
		io.WriteString(pw, "\n\n\n")
		pw.Close()
	}()
	require.NoError(t, m.Stream(context.Background(), pr, "build", func(error) { errCalled = true }), "Stream (empty input)")
	assert.False(t, errCalled, "Stream (empty input): errFn called unexpectedly")
}

// TestStream_BatchErrorDoesNotStopTheLoop pins the --watch contract: magus never waits
// on another magus process, so a batch that hits a contended lock or a full machine
// budget now fails a whole target run (exit 75) instead of blocking. That failure must
// be reported through errFn and skipped, not treated as fatal to the stream: a later
// batch (the next change) still gets a turn, and Stream itself still returns nil.
//
// A zero Magus has no workspace, so every batch's AffectedFromPaths errors the same way
// a real contention would surface to errFn; the loop's behavior on that error is what
// this test pins, not the specific cause.
func TestStream_BatchErrorDoesNotStopTheLoop(t *testing.T) {
	t.Parallel()
	m := &Magus{}
	var mu sync.Mutex
	var errs int
	pr, pw := io.Pipe()
	go func() {
		io.WriteString(pw, "a\n\n")
		// Long enough that the first batch is picked up, fails, and the worker goes
		// idle again before the second one (the "next change") arrives.
		time.Sleep(100 * time.Millisecond)
		io.WriteString(pw, "b\n\n")
		pw.Close()
	}()
	err := m.Stream(context.Background(), pr, "build", func(error) {
		mu.Lock()
		errs++
		mu.Unlock()
	})
	require.NoError(t, err, "a batch failure must never become Stream's own return value")
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 2, errs, "both the failing batch and the next change must be attempted")
}
