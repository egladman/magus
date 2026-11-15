package watch

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// correctly split and sent on the channel.
func TestReadBatches_NewlineMode(t *testing.T) {
	t.Parallel()
	input := "a\nb\n\nc\nd\n\n"
	ch := ReadBatches(context.Background(), strings.NewReader(input), false)
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
	ch := ReadBatches(context.Background(), strings.NewReader(input), false)
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
	ch := ReadBatches(context.Background(), bytes.NewBufferString(input), true)
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
	ch := ReadBatches(ctx, pr, false)
	for range ch {
		// drain any items that may have squeezed through before cancel
	}
	// Channel must be closed; we must not block here.
}

// The leak this closes: the ctx checks inside ReadBatches only run BETWEEN reads, so a
// cancelled stream whose input had merely gone quiet left the reader goroutine parked in
// a blocking read that no cancellation could reach.
func TestReadBatches_CancelUnblocksAParkedRead(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe()
	defer pw.Close()

	ch := ReadBatches(ctx, pr, false) // nothing is ever written, so the read parks
	cancel()

	select {
	case _, ok := <-ch:
		assert.False(t, ok, "the channel closes once the parked read is released")
	case <-time.After(5 * time.Second):
		t.Fatal("ReadBatches stayed parked in a read after its ctx was cancelled")
	}
}

// TestStream_ContextCancellation verifies that streamAffected returns when ctx is

// TestMergePaths verifies dedup + lex ordering, the contract callers
// rely on for deterministic stream-driven affected output.
func TestMergePaths(t *testing.T) {
	t.Parallel()

	// merge normalizes a nil result to an empty slice so the comparison is
	// against a non-nil expected value (assert.Equal distinguishes nil/empty).
	merge := func(a, b []string) []string {
		got := MergePaths(a, b)
		if got == nil {
			got = []string{}
		}
		return got
	}

	assert.Equal(t, []string{}, merge(nil, nil), "both empty")
	assert.Equal(t, []string{"a", "x"}, merge(nil, []string{"x", "a"}), "a empty")
	assert.Equal(t, []string{"a", "x"}, merge([]string{"x", "a"}, nil), "b empty")
	assert.Equal(t, []string{"a", "b", "c"}, merge([]string{"a", "b"}, []string{"b", "c"}), "dedup across")
	assert.Equal(t, []string{"a"}, merge([]string{"a", "a"}, []string{"a"}), "all dupes")
}
