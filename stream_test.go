package magus

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"sync/atomic"
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

// TestStream_BatchErrorDoesNotStopTheLoop pins that a failed batch is reported through
// errFn and skipped rather than ending the stream: the next change still gets a turn,
// and Stream itself returns nil. Under --watch that is what turns a refused run (exit
// 75, another invocation holding the lock) into a retry on the next change.
//
// A zero Magus has no workspace, so every batch fails in AffectedFromPaths; the loop's
// handling of a failure is what this pins, whatever the cause.
func TestStream_BatchErrorDoesNotStopTheLoop(t *testing.T) {
	t.Parallel()
	m := &Magus{}
	var errs atomic.Int32
	firstFailed := make(chan struct{})
	var once sync.Once
	pr, pw := io.Pipe()
	go func() {
		_, _ = io.WriteString(pw, "a\n\n")
		// The next change arrives only after the first batch has failed, so it cannot be
		// merged into the batch it is meant to follow.
		<-firstFailed
		_, _ = io.WriteString(pw, "b\n\n")
		_ = pw.Close()
	}()
	err := m.Stream(context.Background(), pr, "build", func(error) {
		errs.Add(1)
		once.Do(func() { close(firstFailed) })
	})
	require.NoError(t, err, "a batch failure must never become Stream's own return value")
	assert.Equal(t, int32(2), errs.Load(), "both the failing batch and the next change must be attempted")
}
