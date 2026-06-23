// Package watch provides a cross-platform filesystem watcher with recursive
// directory tracking, debouncing, and ignore filtering.
package watch

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const maxPending = 512 // flush immediately when pending exceeds this to prevent unbounded growth
const walkWorkers = 8  // max concurrent goroutines walking newly-created directories

// Backend selects the underlying filesystem notification mechanism.
type Backend int

const (
	FsnotifyBackend Backend = iota // OS-level events (inotify, kqueue, FSEvents)
	PollBackend                    // fixed-interval polling; use for NFS/FUSE/WSL2
)

type watchConfig struct {
	roots      []string
	ignore     func(absPath string) bool
	debounce   time.Duration
	backend    Backend
	bufferSize int
}

// Option configures a [Watcher].
type Option func(*watchConfig)

// WithRoot adds a directory to watch recursively.
func WithRoot(root string) Option { return func(c *watchConfig) { c.roots = append(c.roots, root) } }

// WithIgnore sets the ignore predicate. Returning true skips a path entirely.
// Compose multiple predicates with [Compose].
func WithIgnore(fn func(absPath string) bool) Option {
	return func(c *watchConfig) { c.ignore = fn }
}

// WithDebounce sets the quiet-window before a [Batch] is emitted. Defaults to 200ms.
func WithDebounce(d time.Duration) Option { return func(c *watchConfig) { c.debounce = d } }

// WithBackend selects the notification mechanism. Defaults to [FsnotifyBackend].
func WithBackend(b Backend) Option { return func(c *watchConfig) { c.backend = b } }

// Batch is one debounced, deduplicated set of changed absolute paths.
type Batch struct {
	Paths []string
	At    time.Time
}

// Watcher watches directory trees and emits debounced [Batch] values after filesystem changes.
type Watcher struct {
	events  chan Batch
	errors  chan error
	done    chan struct{}
	once    sync.Once
	n       notifier
	walkSem chan struct{} // semaphore limiting concurrent walkAndWatch goroutines
	synth   chan string   // synthetic file-path events injected by walk goroutines
}

// New constructs and starts a Watcher, walking roots immediately. ctx cancellation is equivalent to Close.
func New(ctx context.Context, opts ...Option) (*Watcher, error) {
	cfg := watchConfig{
		debounce:   200 * time.Millisecond,
		bufferSize: 256,
		ignore:     func(string) bool { return false },
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	var n notifier
	if cfg.backend == PollBackend {
		n = newPoller(cfg.roots, cfg.ignore)
	} else {
		dn, err := newDefaultNotifier(ctx, cfg.roots, cfg.ignore)
		if err != nil {
			return nil, err
		}
		n = dn
	}

	w := &Watcher{
		events:  make(chan Batch, cfg.bufferSize),
		errors:  make(chan error, 16),
		done:    make(chan struct{}),
		n:       n,
		walkSem: make(chan struct{}, walkWorkers),
		synth:   make(chan string, 256),
	}
	go func() {
		select {
		case <-ctx.Done():
			_ = w.Close() // idempotent
		case <-w.done:
			// Close() was called directly; nothing to do.
		}
	}()
	go w.loop(ctx, cfg)
	return w, nil
}

// Events returns the channel of debounced change batches. The channel is
// closed when the Watcher is closed.
func (w *Watcher) Events() <-chan Batch { return w.events }

// Errors returns non-fatal watcher errors (e.g. inotify limit reached).
// The channel is closed when the Watcher is closed.
func (w *Watcher) Errors() <-chan error { return w.errors }

// Close stops the watcher and releases resources. It is idempotent.
func (w *Watcher) Close() error {
	var err error
	w.once.Do(func() {
		close(w.done)
		err = w.n.Close()
	})
	return err
}

func (w *Watcher) loop(ctx context.Context, cfg watchConfig) {
	defer close(w.events)
	defer close(w.errors)

	pending := map[string]struct{}{}
	var timer *time.Timer
	var timerC <-chan time.Time

	resetTimer := func() {
		if timer == nil {
			timer = time.NewTimer(cfg.debounce)
			timerC = timer.C
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(cfg.debounce)
	}

	flush := func() {
		if len(pending) == 0 {
			return
		}
		paths := make([]string, 0, len(pending))
		for p := range pending {
			paths = append(paths, p)
		}
		slices.Sort(paths)
		select {
		case w.events <- Batch{Paths: paths, At: time.Now()}:
			clear(pending) // reuse the map; paths was copied into the Batch above
		default:
			resetTimer() // consumer slow: retain and retry
		}
	}

	// flushFinal delivers the last batch on shutdown, blocking (unlike flush) so it
	// isn't dropped, bounded by a short grace so a gone consumer can't pin us open.
	// ctx is not the escape: it's usually already cancelled at shutdown.
	flushFinal := func() {
		if len(pending) == 0 {
			return
		}
		paths := make([]string, 0, len(pending))
		for p := range pending {
			paths = append(paths, p)
		}
		slices.Sort(paths)
		grace := time.NewTimer(200 * time.Millisecond)
		defer grace.Stop()
		select {
		case w.events <- Batch{Paths: paths, At: time.Now()}:
		case <-grace.C:
		}
	}

	for {
		select {
		case <-w.done:
			if timer != nil {
				timer.Stop()
			}
			flushFinal()
			return

		case ev, ok := <-w.n.Events():
			if !ok {
				flushFinal()
				return
			}
			if cfg.ignore(ev.Name) {
				continue
			}
			if ev.Has(fsnotify.Chmod) {
				continue
			}
			// Auto-watch newly created directories off the loop goroutine. The walk
			// must never run synchronously here: a deep tree (e.g. a dropped
			// node_modules) would block draining w.n.Events() long enough to overflow
			// the kernel inotify queue and lose events. The goroutine waits for a
			// walkSem slot itself, so concurrent walks stay bounded without stalling
			// the loop.
			if ev.Has(fsnotify.Create) {
				if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
					name := ev.Name
					go func() {
						select {
						case w.walkSem <- struct{}{}:
						case <-w.done:
							return
						}
						defer func() { <-w.walkSem }()
						files := walkAndWatchCollect(ctx, name, cfg.ignore, w.n)
						for _, f := range files { // synthesize events for pre-existing files
							select {
							case w.synth <- f:
							case <-w.done:
								return
							}
						}
					}()
				}
			}
			if ev.Has(fsnotify.Rename) || ev.Has(fsnotify.Remove) {
				_ = w.n.Remove(ev.Name)
			}
			pending[ev.Name] = struct{}{}
			if len(pending) >= maxPending {
				flush()
			} else {
				resetTimer()
			}

		case path := <-w.synth:
			if !cfg.ignore(path) {
				pending[path] = struct{}{}
				if len(pending) >= maxPending {
					flush()
				} else {
					resetTimer()
				}
			}

		case err, ok := <-w.n.Errors():
			if !ok {
				return
			}
			select {
			case w.errors <- err:
			default:
			}

		case <-timerC:
			timer = nil
			timerC = nil
			flush()
		}
	}
}

// ProbeBackend returns FsnotifyBackend when OS-level events work under root, PollBackend otherwise.
func ProbeBackend(root string) Backend {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return PollBackend
	}
	defer func() { _ = w.Close() }()
	if err := w.Add(root); err != nil {
		return PollBackend
	}
	return FsnotifyBackend
}

// walkAndWatch recursively adds every non-ignored directory under root to n.
func walkAndWatch(ctx context.Context, root string, ignore func(string) bool, n notifier) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			return nil //nolint:nilerr // skip unreadable entries, continue walking
		}
		if !d.IsDir() {
			return nil
		}
		if ignore(path) {
			return filepath.SkipDir
		}
		return n.Add(path)
	})
}

// walkAndWatchCollect adds dirs and returns non-dir files for synthetic Create events.
func walkAndWatchCollect(ctx context.Context, root string, ignore func(string) bool, n notifier) []string {
	var files []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			return nil //nolint:nilerr // skip unreadable entries, continue walking
		}
		if d.IsDir() {
			if ignore(path) {
				return filepath.SkipDir
			}
			_ = n.Add(path)
			return nil
		}
		if !ignore(path) {
			files = append(files, path)
		}
		return nil
	})
	return files
}
