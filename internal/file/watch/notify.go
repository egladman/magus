package watch

import (
	"io/fs"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// notifier is the internal interface shared by both notification backends.
type notifier interface {
	Add(path string) error
	Remove(path string) error
	Events() <-chan fsnotify.Event
	Errors() <-chan error
	Close() error
}

// fsnotifyNotifier wraps *fsnotify.Watcher, which exposes Events and Errors
// as channel fields rather than methods, so a thin adapter is needed.
type fsnotifyNotifier struct {
	w *fsnotify.Watcher
}

func newFsnotifyNotifier() (*fsnotifyNotifier, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &fsnotifyNotifier{w: w}, nil
}

func (n *fsnotifyNotifier) Add(path string) error         { return n.w.Add(path) }
func (n *fsnotifyNotifier) Remove(path string) error      { return n.w.Remove(path) }
func (n *fsnotifyNotifier) Events() <-chan fsnotify.Event { return n.w.Events }
func (n *fsnotifyNotifier) Errors() <-chan error          { return n.w.Errors }
func (n *fsnotifyNotifier) Close() error                  { return n.w.Close() }

// poller is a polling-based notifier for environments where OS-level events
// are unavailable or unreliable (NFS mounts, FUSE, some WSL2 configurations).
// It scans watched directories every second and emits synthetic events for
// any files that appear, change, or disappear between scans.
type poller struct {
	events    chan fsnotify.Event
	errors    chan error
	done      chan struct{}
	closeOnce sync.Once
	mu        sync.Mutex
	roots     []string
	ignore    func(string) bool
	state     map[string]fileState
	interval  time.Duration
}

type fileState struct {
	size    int64
	modTime time.Time
}

func newPoller(roots []string, ignore func(string) bool) *poller {
	p := &poller{
		events:   make(chan fsnotify.Event, 256),
		errors:   make(chan error, 16),
		done:     make(chan struct{}),
		roots:    append([]string(nil), roots...),
		ignore:   ignore,
		state:    map[string]fileState{},
		interval: time.Second,
	}
	go p.run()
	return p
}

// Add is a no-op for the poller — it walks from roots on each tick.
func (*poller) Add(_ string) error { return nil }

// Remove is a no-op for the poller — it walks from roots on each tick.
func (*poller) Remove(_ string) error { return nil }

func (p *poller) Events() <-chan fsnotify.Event { return p.events }
func (p *poller) Errors() <-chan error          { return p.errors }

func (p *poller) Close() error {
	p.closeOnce.Do(func() { close(p.done) })
	return nil
}

func (p *poller) run() {
	defer close(p.events)
	defer close(p.errors)
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-p.done:
			return
		case <-ticker.C:
			p.scan()
		}
	}
}

func (p *poller) scan() {
	p.mu.Lock()
	roots := append([]string(nil), p.roots...)
	p.mu.Unlock()

	newState := make(map[string]fileState, len(p.state))
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil //nolint:nilerr // WalkDir: skip unreadable entries, continue walking
			}
			if d.IsDir() {
				if p.ignore(path) {
					return filepath.SkipDir
				}
				return nil
			}
			if p.ignore(path) {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil //nolint:nilerr // skip entries whose info is unavailable
			}
			newState[path] = fileState{size: info.Size(), modTime: info.ModTime()}
			return nil
		})
	}

	for path, cur := range newState {
		prev, exists := p.state[path]
		if !exists {
			p.emit(path, fsnotify.Create)
		} else if prev != cur {
			p.emit(path, fsnotify.Write)
		}
	}
	for path := range p.state {
		if _, ok := newState[path]; !ok {
			p.emit(path, fsnotify.Remove)
		}
	}

	p.state = newState
}

func (p *poller) emit(path string, op fsnotify.Op) {
	select {
	case p.events <- fsnotify.Event{Name: path, Op: op}:
	default: // drop if consumer is behind
	}
}

// Compile-time interface checks.
var (
	_ notifier = (*fsnotifyNotifier)(nil)
	_ notifier = (*poller)(nil)
)
