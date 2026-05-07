package race

import (
	"context"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// interval tracks the wall-clock window of one (project, target) execution.
type interval struct {
	Project      string
	Target       string
	StartedAt    time.Time
	EndedAt      time.Time // zero until endInterval is called
	WrittenPaths []string  // files written by this project; set by setWrittenPaths
}

// fsEvent is a single filesystem modification event recorded by the watcher.
type fsEvent struct {
	Path       string
	ObservedAt time.Time
}

// recorder watches a directory tree for filesystem events and records the
// concurrent execution intervals of (project, target) pairs.
type recorder struct {
	mu        sync.Mutex
	watcher   *fsnotify.Watcher
	events    []fsEvent
	intervals []interval
	done      chan struct{} // closed by drain when it exits
}

// snapshot is the data the detector needs, captured after the run ends.
type snapshot struct {
	events    []fsEvent
	intervals []interval
}

func (r *recorder) start(ctx context.Context, root string) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.watcher = w
	r.done = make(chan struct{})
	r.mu.Unlock()

	// Walk the workspace root recursively. New directories created during the
	// run won't be tracked, which is acceptable for race detection.
	if err := addDir(w, root); err != nil {
		_ = w.Close()
		r.mu.Lock()
		r.watcher = nil
		r.mu.Unlock()
		return err
	}

	go r.drain(ctx)
	return nil
}

func (r *recorder) drain(ctx context.Context) {
	defer close(r.done)
	for {
		select {
		case ev, ok := <-r.watcher.Events:
			if !ok {
				return
			}
			// Only track write-type operations; skip chmod/stat-only events.
			if ev.Has(fsnotify.Write) || ev.Has(fsnotify.Create) ||
				ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename) {
				r.mu.Lock()
				r.events = append(r.events, fsEvent{
					Path:       ev.Name,
					ObservedAt: time.Now(),
				})
				r.mu.Unlock()
			}
		case <-r.watcher.Errors:
			// Non-fatal; silently drop.
		case <-ctx.Done():
			return
		}
	}
}

func (r *recorder) startInterval(project, target string) {
	r.mu.Lock()
	r.intervals = append(r.intervals, interval{
		Project:   project,
		Target:    target,
		StartedAt: time.Now(),
	})
	r.mu.Unlock()
}

func (r *recorder) endInterval(project, target string) {
	now := time.Now()
	r.mu.Lock()
	for i := len(r.intervals) - 1; i >= 0; i-- {
		iv := &r.intervals[i]
		if iv.Project == project && iv.Target == target && iv.EndedAt.IsZero() {
			iv.EndedAt = now
			break
		}
	}
	r.mu.Unlock()
}

// setWrittenPaths stores the paths written by (project, target) for attribution.
// It searches backwards because the matching interval was just added.
func (r *recorder) setWrittenPaths(project, target string, paths []string) {
	if len(paths) == 0 {
		return
	}
	r.mu.Lock()
	for i := len(r.intervals) - 1; i >= 0; i-- {
		iv := &r.intervals[i]
		if iv.Project == project && iv.Target == target {
			iv.WrittenPaths = paths
			break
		}
	}
	r.mu.Unlock()
}

// close stops the watcher and waits for the drain goroutine to exit before
// returning, so snapshot() is guaranteed to see all events.
func (r *recorder) close() {
	r.mu.Lock()
	w := r.watcher
	done := r.done
	r.mu.Unlock()
	if w != nil {
		_ = w.Close()
		<-done // not held under the mutex: drain needs it to append final events
	}
}

func (r *recorder) snapshot() snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	evCopy := make([]fsEvent, len(r.events))
	copy(evCopy, r.events)
	ivCopy := make([]interval, len(r.intervals))
	copy(ivCopy, r.intervals)
	// Seal any intervals that weren't explicitly ended (e.g. on error paths).
	now := time.Now()
	for i := range ivCopy {
		if ivCopy[i].EndedAt.IsZero() {
			ivCopy[i].EndedAt = now
		}
	}
	return snapshot{events: evCopy, intervals: ivCopy}
}

// addDir adds dir and all non-skipped subdirectories to w recursively.
func addDir(w *fsnotify.Watcher, dir string) error {
	if err := w.Add(dir); err != nil {
		return err
	}
	entries, err := readDirNames(dir)
	if err != nil {
		return nil //nolint:nilerr // best-effort scan: an unreadable dir contributes no race records
	}
	for _, name := range entries {
		if shouldSkipDir(name) {
			continue
		}
		child := dir + "/" + name
		if isDir(child) {
			if err := addDir(w, child); err != nil {
				continue // non-fatal: some dirs may be inaccessible
			}
		}
	}
	return nil
}
