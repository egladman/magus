package watch

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// closedBatches is a closed channel holding batches, in order.
func closedBatches(batches ...[]string) <-chan []string {
	ch := make(chan []string, len(batches))
	for _, b := range batches {
		ch <- b
	}
	close(ch)
	return ch
}

func TestRunBatchesReturnsWhenTheContextIsAlreadyCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	RunBatches(ctx, make(chan []string), func([]string) error {
		t.Error("a cancelled stream ran a batch")
		return nil
	}, nil)
}

func TestRunBatchesSkipsAnEmptyBatch(t *testing.T) {
	t.Parallel()
	var ran, failed bool
	RunBatches(context.Background(), closedBatches(nil, []string{}), func([]string) error {
		ran = true
		return nil
	}, func(error) { failed = true })
	assert.False(t, ran, "an empty batch has nothing to run")
	assert.False(t, failed)
}

// A failed batch is reported and skipped rather than ending the stream: the next change
// still gets a turn. Under --watch that is what turns a refused run (exit 75, another
// invocation holding the lock) into a retry on the next change.
func TestRunBatchesKeepsGoingAfterAFailedBatch(t *testing.T) {
	t.Parallel()
	var (
		mu   sync.Mutex
		ran  [][]string
		errs []error
	)
	RunBatches(context.Background(), closedBatches([]string{"a"}, []string{"b"}), func(p []string) error {
		mu.Lock()
		defer mu.Unlock()
		ran = append(ran, p)
		return errors.New("refused")
	}, func(err error) {
		mu.Lock()
		defer mu.Unlock()
		errs = append(errs, err)
	})

	assert.Len(t, errs, len(ran), "every attempted batch reports its failure")
	assert.Equal(t, []string{"a", "b"}, slices.Concat(ran...),
		"the change after a failed batch must still be attempted")
}

func TestRunBatchesReportsAPanicAndRunsWhatWasPending(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	batches := make(chan []string)
	var (
		mu   sync.Mutex
		ran  [][]string
		errs []error
	)
	go func() {
		batches <- []string{"a"}
		// "b" and "c" arrive while "a" runs, so they merge into one pending batch. The
		// empty batch is received only after "b" merged, so it orders the release.
		batches <- []string{"c"}
		batches <- []string{"b"}
		batches <- []string{}
		close(release)
		close(batches)
	}()
	RunBatches(context.Background(), batches, func(p []string) error {
		mu.Lock()
		ran = append(ran, p)
		first := len(ran) == 1
		mu.Unlock()
		if first {
			<-release
			panic("boom")
		}
		return nil
	}, func(err error) {
		mu.Lock()
		defer mu.Unlock()
		errs = append(errs, err)
	})

	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Error(), "panic: boom")
	assert.Equal(t, [][]string{{"a"}, {"b", "c"}}, ran,
		"a panicking batch must not orphan the batches merged while it ran")
}

// TestMergePaths verifies dedup + lex ordering, the contract callers
// rely on for deterministic stream-driven affected output.
func TestMergePaths(t *testing.T) {
	t.Parallel()

	// merge normalizes a nil result to an empty slice so the comparison is
	// against a non-nil expected value (assert.Equal distinguishes nil/empty).
	merge := func(a, b []string) []string {
		got := mergePaths(a, b)
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
