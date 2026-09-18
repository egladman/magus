package console

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/file/watch"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
)

// A change under a job's declared write lane is attributed to that job with nothing asked
// of the worker: the filesystem reported the path, and the lane named the holder.
func TestJobFeedAttributesAChangeToTheLaneThatCoversIt(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "internal", "trail"), 0o755))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w, err := watch.New(ctx,
		watch.WithRoot(dir),
		watch.WithBackend(watch.PollBackend),
		watch.WithDebounce(50*time.Millisecond),
	)
	require.NoError(t, err)
	defer w.Close()

	rows := []types.Job{{ID: "pwa/job-watch", State: types.StateRunning, WritePaths: []string{"internal/trail"}}}
	changes := NewJobFeed(ctx, w, dir, func() []types.Job { return rows }).Subscribe(ctx)

	target := filepath.Join(dir, "internal", "trail", "trail.go")
	retick := time.NewTicker(200 * time.Millisecond)
	defer retick.Stop()
	deadline := time.After(5 * time.Second)
	rewrite := func() { _ = os.WriteFile(target, []byte("// "+time.Now().String()+"\n"), 0o644) }
	rewrite()
	for {
		select {
		case e := <-changes:
			if e.Action != "internal/trail/trail.go" {
				continue // the watcher may report the directory alongside the file
			}
			require.Equal(t, "pwa/job-watch", e.Job)
			require.Equal(t, "internal/trail", e.Lane, "the reader is told WHICH declaration answered")
			return
		case <-retick.C:
			rewrite()
		case <-deadline:
			t.Fatal("timeout: the change never reached a subscriber")
		}
	}
}

// TestJobFeedInvalidateSignalsOnGraphRelevantChange drives a real poll-backed watcher over a
// temp dir and confirms the feed forwards a graph-relevant (.buzz) change onto its
// capacity-1 channel. The write is re-fired on a ticker because there is no portable signal
// that the watch is "hot", mirroring the watch package's own test pattern.
func TestJobFeedInvalidateSignalsOnGraphRelevantChange(t *testing.T) {
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

	feed := NewJobFeed(ctx, w, dir, func() []types.Job { return nil })
	invalidate := feed.Invalidate()
	// A subscriber is opened alongside, because the same batch feeds both halves and the
	// bug worth pinning is one arm starving the other: an unread subscriber must not stop
	// the graph signal arriving.
	changes := feed.Subscribe(ctx)

	retick := time.NewTicker(200 * time.Millisecond)
	defer retick.Stop()
	deadline := time.After(5 * time.Second)
	rewrite := func() { _ = os.WriteFile(buzz, []byte("// changed "+time.Now().String()+"\n"), 0o644) }
	rewrite()
	for {
		select {
		case <-invalidate:
			return // graph-relevant change surfaced; done
		case <-changes:
			// A change also reaches a subscriber. Drained rather than asserted on: which
			// arm of this select wins is a race the watcher's debounce owns.
		case <-retick.C:
			rewrite()
		case <-deadline:
			t.Fatal("timeout: no invalidate signal for a .buzz change")
		}
	}
}

// TestJobFeedExitsOnContextCancel confirms the goroutine returns (and does not signal)
// once the context is cancelled.
func TestJobFeedExitsOnContextCancel(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	w, err := watch.New(ctx,
		watch.WithRoot(dir),
		watch.WithBackend(watch.PollBackend),
		watch.WithDebounce(50*time.Millisecond),
	)
	require.NoError(t, err)
	defer w.Close()

	feed := NewJobFeed(ctx, w, dir, func() []types.Job { return nil })
	invalidate := feed.Invalidate()
	changes := feed.Subscribe(ctx)
	cancel()

	// After cancellation the channel must never receive a signal.
	select {
	case <-invalidate:
		t.Fatal("no invalidate signal expected after context cancel")
	case <-time.After(300 * time.Millisecond):
		// expected: goroutine exited quietly
	}
	// A subscriber is CLOSED rather than left dangling, so a reader sees the end instead of
	// blocking on a feed that will never speak again.
	_, open := <-changes
	require.False(t, open, "a cancelled feed closes its subscribers")
}

// ONE BATCH FEEDS BOTH HALVES, and this is the regression it exists to stop.
//
// A Go channel has one reader. Before the feed, graph invalidation had its own loop over
// the watcher's Events(); adding a second loop for the job feed would have given each of
// them alternate batches, halving both signals with nothing failing and nothing logged. The
// test drives publish directly rather than through a real watcher, so the property is
// asserted rather than raced for: every batch that is graph-relevant signals invalidation,
// AND every batch reaches every subscriber.
func TestJobFeedServesInvalidationAndSubscribersFromTheSameBatch(t *testing.T) {
	rows := []types.Job{{ID: "pwa/job-watch", State: types.StateRunning, WritePaths: []string{"internal/trail"}}}
	f := &JobFeed{
		subs:       map[chan job.FeedEvent]struct{}{},
		root:       "/repo",
		rows:       func() []types.Job { return rows },
		invalidate: make(chan struct{}, 1),
	}
	first := make(chan job.FeedEvent, 8)
	second := make(chan job.FeedEvent, 8)
	f.subs[first] = struct{}{}
	f.subs[second] = struct{}{}

	f.publish(watch.Batch{At: time.UnixMilli(7), Paths: []string{"/repo/magusfile.buzz", "/repo/internal/trail/trail.go"}})

	select {
	case <-f.Invalidate():
	default:
		t.Fatal("a batch touching a .buzz file must still signal graph invalidation")
	}
	for name, ch := range map[string]chan job.FeedEvent{"first": first, "second": second} {
		var paths []string
		for len(ch) > 0 {
			paths = append(paths, (<-ch).Action)
		}
		require.Equal(t, []string{"magusfile.buzz", "internal/trail/trail.go"}, paths,
			name+" subscriber must see the whole batch, not every other one")
	}

	// A second batch, to pin that the first one did not consume the watcher for everybody
	// else: this is exactly what two read loops over one channel would have broken.
	f.publish(watch.Batch{At: time.UnixMilli(8), Paths: []string{"/repo/magus.yaml"}})
	select {
	case <-f.Invalidate():
	default:
		t.Fatal("the second batch must signal too")
	}
	require.Len(t, first, 1)
	require.Len(t, second, 1)
}

// A path outside the root is not attributed to anything here. It is skipped rather than
// reported unattributed: a lane is declared repo-relative, so no declaration in this plan
// could honestly cover a file from another tree.
func TestJobFeedSkipsPathsOutsideTheRoot(t *testing.T) {
	f := &JobFeed{
		subs:       map[chan job.FeedEvent]struct{}{},
		root:       "/repo",
		rows:       func() []types.Job { return nil },
		invalidate: make(chan struct{}, 1),
	}
	only := make(chan job.FeedEvent, 4)
	f.subs[only] = struct{}{}

	f.publish(watch.Batch{At: time.UnixMilli(9), Paths: []string{"/elsewhere/trail.go"}})
	require.Empty(t, only)
}
