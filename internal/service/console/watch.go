package console

import (
	"context"
	"path/filepath"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/file/watch"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
)

// isGraphRelevant reports whether any changed path feeds the knowledge graph. Mirrors
// magus.graphRelevant (warm_graph.go) without importing the root package to avoid an import
// cycle.
func isGraphRelevant(paths []string) bool {
	for _, p := range paths {
		if strings.HasSuffix(p, ".buzz") || strings.HasSuffix(p, ".md") {
			return true
		}
		switch filepath.Base(p) {
		case "magus.yaml", "magus.yml", "magusfiles":
			return true
		}
	}
	return false
}

// JobFeed turns the daemon's one file watcher into a feed any number of readers can
// subscribe to, with each changed path already attributed to the job whose declared write
// paths cover it.
//
// THIS IS THE HALF THAT NEEDS NO COOPERATION FROM THE WORKER. A guard event exists because
// an agent host ran a hook; a run exists because somebody recorded one. A file change is
// the filesystem reporting a fact, and the write paths are what turn it into "that worker is
// editing this". Nothing the worker does can make it quiet.
//
// One watcher, many subscribers: two readers pulling from one channel would each get half
// the events, which looks exactly like a worker that went quiet.
// It is also the watcher's ONLY consumer. A channel has one reader: two goroutines ranging
// over w.Events() would take alternate batches, so the graph-invalidation signal the SSE
// stream already depended on is served from here rather than from a second read loop.
type JobFeed struct {
	mu   sync.Mutex
	subs map[chan job.FeedEvent]struct{}
	root string
	rows func() []types.Job
	// invalidate carries the graph-relevance signal the console's SSE stream reads. Capacity
	// one with a non-blocking send: a consumer that has not drained the pending notice is
	// already going to rebuild, so a second notice tells it nothing.
	invalidate chan struct{}
}

// NewJobFeed starts the fan-out over w and returns it. rows is read at each batch rather
// than captured: jobs are declared, released and ended while the daemon runs, and a plan
// taken at mount time would attribute against one that stopped being true.
//
// The goroutine exits when ctx is cancelled or the watcher closes its channel, and closes
// every subscriber on the way out so a reader sees the end rather than blocking on it.
func NewJobFeed(ctx context.Context, w *watch.Watcher, root string, rows func() []types.Job) *JobFeed {
	f := &JobFeed{subs: map[chan job.FeedEvent]struct{}{}, root: root, rows: rows, invalidate: make(chan struct{}, 1)}
	go func() {
		defer f.closeAll()
		events := w.Events()
		for {
			select {
			case <-ctx.Done():
				return
			case batch, ok := <-events:
				if !ok {
					return
				}
				f.publish(batch)
			}
		}
	}()
	return f
}

// Subscribe returns a channel of attributed changes, closed when ctx is cancelled or the
// feed ends. The buffer absorbs a burst (a branch switch touches thousands of files); past
// it, events are DROPPED rather than blocking the watcher, because one stalled browser tab
// must not stop every other reader from seeing anything.
func (f *JobFeed) Subscribe(ctx context.Context) <-chan job.FeedEvent {
	ch := make(chan job.FeedEvent, jobFeedBuffer)
	f.mu.Lock()
	f.subs[ch] = struct{}{}
	f.mu.Unlock()
	go func() {
		<-ctx.Done()
		f.mu.Lock()
		if _, live := f.subs[ch]; live {
			delete(f.subs, ch)
			close(ch)
		}
		f.mu.Unlock()
	}()
	return ch
}

// jobFeedBuffer is how far behind one reader may fall before it starts missing events. A
// person watching a worker reads a handful of changes a minute; this is sized for the
// branch switch, not for the reading.
const jobFeedBuffer = 256

// Invalidate is the graph-relevance signal: one notice per batch that touched a file the
// knowledge graph is built from. The console's SSE stream reads it. It is never closed, so
// a consumer holding it past the feed's life blocks rather than seeing a spurious signal.
func (f *JobFeed) Invalidate() <-chan struct{} { return f.invalidate }

func (f *JobFeed) publish(batch watch.Batch) {
	if isGraphRelevant(batch.Paths) {
		select {
		case f.invalidate <- struct{}{}:
		default: // see JobFeed.invalidate
		}
	}
	rows := f.rows()
	ts := batch.At.UnixMilli()
	rel := make([]string, 0, len(batch.Paths))
	for _, p := range batch.Paths {
		// The watcher reports absolute paths and a write path is declared repo-relative, so a
		// path that will not relativize is one from outside this root: skipped rather than
		// attributed, since no declaration here could honestly cover it.
		r, err := filepath.Rel(f.root, p)
		if err != nil || strings.HasPrefix(r, "..") {
			continue
		}
		rel = append(rel, filepath.ToSlash(r))
	}
	if len(rel) == 0 {
		return
	}
	events := job.FileEvents(rows, ts, rel)
	f.mu.Lock()
	defer f.mu.Unlock()
	for ch := range f.subs {
		for _, e := range events {
			select {
			case ch <- e:
			default: // see Subscribe: a full reader misses events rather than stalling the watcher
			}
		}
	}
}

func (f *JobFeed) closeAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for ch := range f.subs {
		delete(f.subs, ch)
		close(ch)
	}
}
