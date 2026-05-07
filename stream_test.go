package magus

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
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
	if len(got) != len(want) {
		t.Fatalf("readBatches (newline): got %d batches, want %d; batches=%v", len(got), len(want), got)
	}
	for i, b := range got {
		if len(b) != len(want[i]) {
			t.Errorf("batch[%d] = %v, want %v", i, b, want[i])
			continue
		}
		for j, v := range b {
			if v != want[i][j] {
				t.Errorf("batch[%d][%d] = %q, want %q", i, j, v, want[i][j])
			}
		}
	}
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
	if len(got) != 1 || len(got[0]) != 2 {
		t.Fatalf("readBatches (trailing): got %v, want [[x y]]", got)
	}
}

// TestReadBatches_NullMode verifies NUL-separated batches with double-NUL boundaries.
// The format mirrors watch.go: each path is NUL-terminated, then a double-NUL
// batchSep follows the last path in each batch. So batch ["a","b"] followed by
// ["c"] encodes as: a\x00 b\x00 \x00\x00 c\x00 \x00\x00
func TestReadBatches_NullMode(t *testing.T) {
	t.Parallel()
	input := "a\x00b\x00\x00\x00c\x00\x00\x00"
	ch := readBatches(context.Background(), bytes.NewBufferString(input), true)
	var got [][]string
	for batch := range ch {
		got = append(got, batch)
	}
	want := [][]string{{"a", "b"}, {"c"}}
	if len(got) != len(want) {
		t.Fatalf("readBatches (null): got %d batches %v, want %d %v", len(got), got, len(want), want)
	}
	for i, b := range got {
		if len(b) != len(want[i]) {
			t.Errorf("null batch[%d] = %v, want %v", i, b, want[i])
		}
	}
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

// TestStream_ContextCancellation verifies that Stream returns nil when ctx is
// already cancelled on entry.
func TestStream_ContextCancellation(t *testing.T) {
	t.Parallel()
	m := &Magus{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pr, pw := io.Pipe()
	pw.Close()
	if err := m.Stream(ctx, pr, "build", nil); err != nil {
		t.Errorf("Stream with cancelled ctx: %v", err)
	}
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
	if err := m.Stream(context.Background(), pr, "build", func(error) { errCalled = true }); err != nil {
		t.Errorf("Stream (empty input): %v", err)
	}
	if errCalled {
		t.Error("Stream (empty input): errFn called unexpectedly")
	}
}
