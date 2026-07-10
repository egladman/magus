package console

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/internal/file/watch"
	"github.com/stretchr/testify/require"
)

// TestWatchInvalidateSignalsOnGraphRelevantChange drives a real poll-backed watcher over a
// temp dir and confirms WatchInvalidate forwards a graph-relevant (.buzz) change onto its
// capacity-1 channel. The write is re-fired on a ticker because there is no portable signal
// that the watch is "hot", mirroring the watch package's own test pattern.
func TestWatchInvalidateSignalsOnGraphRelevantChange(t *testing.T) {
	dir := t.TempDir()
	buzz := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(buzz, []byte("// v0\n"), 0o644))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w, err := watch.New(ctx,
		watch.WithRoot(dir),
		watch.WithBackend(watch.PollBackend),
		watch.WithDebounce(50*time.Millisecond),
	)
	require.NoError(t, err)
	defer w.Close()

	invalidate := WatchInvalidate(ctx, w)

	retick := time.NewTicker(200 * time.Millisecond)
	defer retick.Stop()
	deadline := time.After(5 * time.Second)
	rewrite := func() { _ = os.WriteFile(buzz, []byte("// changed "+time.Now().String()+"\n"), 0o644) }
	rewrite()
	for {
		select {
		case <-invalidate:
			return // graph-relevant change surfaced; done
		case <-retick.C:
			rewrite()
		case <-deadline:
			t.Fatal("timeout: no invalidate signal for a .buzz change")
		}
	}
}

// TestWatchInvalidateExitsOnContextCancel confirms the goroutine returns (and does not signal)
// once the context is cancelled.
func TestWatchInvalidateExitsOnContextCancel(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	w, err := watch.New(ctx,
		watch.WithRoot(dir),
		watch.WithBackend(watch.PollBackend),
		watch.WithDebounce(50*time.Millisecond),
	)
	require.NoError(t, err)
	defer w.Close()

	invalidate := WatchInvalidate(ctx, w)
	cancel()

	// After cancellation the channel must never receive a signal.
	select {
	case <-invalidate:
		t.Fatal("no invalidate signal expected after context cancel")
	case <-time.After(300 * time.Millisecond):
		// expected: goroutine exited quietly
	}
}
