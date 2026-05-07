//go:build linux

package watch

import (
	"bytes"
	"context"
	"io/fs"
	"path/filepath"
	"runtime"
	"sync"
	"unsafe"

	"github.com/fsnotify/fsnotify"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"
)

// inotifyMask excludes IN_ATTRIB/IN_ACCESS/IN_OPEN/IN_CLOSE (noisy, no cache impact).
// IN_EXCL_UNLINK: suppress events for already-deleted files; IN_DONT_FOLLOW: treat symlinks as opaque.
const inotifyMask = unix.IN_CREATE |
	unix.IN_DELETE |
	unix.IN_MODIFY |
	unix.IN_MOVED_FROM |
	unix.IN_MOVED_TO |
	unix.IN_DELETE_SELF |
	unix.IN_MOVE_SELF |
	unix.IN_DONT_FOLLOW |
	unix.IN_EXCL_UNLINK

// inotifyBufSize is sized for 4096 events at minimum cost; real events with filenames are larger.
const inotifyBufSize = 4096 * unix.SizeofInotifyEvent

// inotifyNotifier implements notifier via a direct inotify(7) fd (Linux default backend).
type inotifyNotifier struct {
	fd     int
	pipe   [2]int // pipe[0]=read, pipe[1]=write; used to unblock Poll on Close
	mu       sync.RWMutex
	wds      map[int32]string // wd → abs-dir-path
	paths    map[string]int32 // abs-dir-path → wd
	fdClosed bool             // set under mu by readLoop before it closes fd
	events   chan fsnotify.Event
	errors   chan error
	done     chan struct{}
	once     sync.Once
}

func newInotifyNotifier() (*inotifyNotifier, error) {
	fd, err := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
	if err != nil {
		return nil, err
	}
	var pipe [2]int
	if err := unix.Pipe2(pipe[:], unix.O_CLOEXEC); err != nil {
		unix.Close(fd)
		return nil, err
	}
	n := &inotifyNotifier{
		fd:     fd,
		pipe:   pipe,
		wds:    make(map[int32]string),
		paths:  make(map[string]int32),
		events: make(chan fsnotify.Event, 256),
		errors: make(chan error, 16),
		done:   make(chan struct{}),
	}
	go n.readLoop()
	return n, nil
}

// Add registers an inotify watch on path. The mutex is held across the syscall so
// it cannot race readLoop closing n.fd on shutdown: readLoop takes the same lock to
// set fdClosed before closing the fd, so Add either runs the syscall on the live fd
// or observes fdClosed and bails (a walk goroutine may still call Add during Close).
func (n *inotifyNotifier) Add(path string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.fdClosed {
		return fs.ErrClosed
	}
	wd, err := unix.InotifyAddWatch(n.fd, path, inotifyMask)
	if err != nil {
		return err
	}
	n.wds[int32(wd)] = path
	n.paths[path] = int32(wd)
	return nil
}

// Remove deregisters the inotify watch for path.
func (n *inotifyNotifier) Remove(path string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	wd, ok := n.paths[path]
	if !ok {
		return nil
	}
	delete(n.paths, path)
	delete(n.wds, wd)
	if n.fdClosed {
		return nil
	}
	_, err := unix.InotifyRmWatch(n.fd, uint32(wd))
	return err
}

func (n *inotifyNotifier) Events() <-chan fsnotify.Event { return n.events }
func (n *inotifyNotifier) Errors() <-chan error          { return n.errors }

// Close shuts down the notifier and releases the inotify fd.
func (n *inotifyNotifier) Close() error {
	n.once.Do(func() {
		close(n.done)
		_, _ = unix.Write(n.pipe[1], []byte{0}) // unblock Poll in readLoop
		unix.Close(n.pipe[1])
	})
	return nil
}

// addTree registers inotify watches for every non-ignored directory under root using a parallel worker pool.
func (n *inotifyNotifier) addTree(ctx context.Context, root string, ignore func(string) bool) error {
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(min(walkWorkers, runtime.NumCPU()))

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if cerr := gctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			return nil //nolint:nilerr // skip unreadable entries (same as walkAndWatch)
		}
		if !d.IsDir() {
			return nil
		}
		if path != root && ignore(path) {
			return filepath.SkipDir
		}
		p := path
		g.Go(func() error { return n.Add(p) })
		return nil
	})
	if gerr := g.Wait(); gerr != nil {
		return gerr
	}
	return walkErr
}

func (n *inotifyNotifier) readLoop() {
	defer close(n.events)
	defer close(n.errors)
	defer func() {
		// Mark closed and close the fd under the lock so an in-flight Add/Remove
		// never operates on a closed (and possibly reused) fd. See Add.
		n.mu.Lock()
		n.fdClosed = true
		unix.Close(n.fd)
		n.mu.Unlock()
	}()
	defer unix.Close(n.pipe[0])

	buf := make([]byte, inotifyBufSize)
	pfd := [2]unix.PollFd{
		{Fd: int32(n.fd), Events: unix.POLLIN},
		{Fd: int32(n.pipe[0]), Events: unix.POLLIN},
	}

	for {
		_, err := unix.Poll(pfd[:], -1)
		if err == unix.EINTR {
			continue
		}
		if err != nil || pfd[1].Revents&(unix.POLLIN|unix.POLLHUP) != 0 { // quit: pipe signal or error
			return
		}
		if pfd[0].Revents&unix.POLLIN == 0 {
			continue
		}

		nread, err := unix.Read(n.fd, buf)
		if nread > 0 {
			n.parseEvents(buf[:nread])
		}
		if err != nil {
			if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
				continue
			}
			select {
			case <-n.done:
				return
			default:
			}
			select {
			case n.errors <- err:
			default:
			}
			return
		}
	}
}

// parseEvents decodes raw inotify_event bytes and emits fsnotify.Events.
func (n *inotifyNotifier) parseEvents(data []byte) {
	const hdrSize = unix.SizeofInotifyEvent
	for len(data) >= hdrSize {
		ev := (*unix.InotifyEvent)(unsafe.Pointer(&data[0])) // kernel guarantees 4-byte alignment
		nameLen := int(ev.Len)
		total := hdrSize + nameLen
		if len(data) < total {
			break
		}

		var name string
		if nameLen > 0 {
			raw := data[hdrSize:total]
			if i := bytes.IndexByte(raw, 0); i >= 0 {
				name = string(raw[:i])
			}
		}
		data = data[total:]

		if ev.Mask&unix.IN_IGNORED != 0 { // wd removed: clean up maps, no event needed
			n.mu.Lock()
			if dir, ok := n.wds[ev.Wd]; ok {
				delete(n.wds, ev.Wd)
				delete(n.paths, dir)
			}
			n.mu.Unlock()
			continue
		}

		if ev.Mask&unix.IN_Q_OVERFLOW != 0 { // kernel queue overflow; wd is -1
			select {
			case n.errors <- unix.EOVERFLOW:
			default:
			}
			continue
		}

		op := inotifyMaskToOp(ev.Mask)
		if op == 0 {
			continue
		}

		n.mu.RLock()
		dir, ok := n.wds[ev.Wd]
		n.mu.RUnlock()
		if !ok {
			continue
		}

		absPath := dir
		if name != "" {
			absPath = filepath.Join(dir, name)
		}

		select {
		case n.events <- fsnotify.Event{Name: absPath, Op: op}:
		case <-n.done:
			return
		default: // drop if consumer is behind (same policy as fsnotify)
		}
	}
}

// inotifyMaskToOp maps an inotify mask to fsnotify.Op; returns 0 for ignored masks.
func inotifyMaskToOp(mask uint32) fsnotify.Op {
	var op fsnotify.Op
	if mask&unix.IN_CREATE != 0 {
		op |= fsnotify.Create
	}
	if mask&unix.IN_MOVED_TO != 0 {
		op |= fsnotify.Create // moved-in = created from watcher's view
	}
	if mask&unix.IN_DELETE != 0 {
		op |= fsnotify.Remove
	}
	if mask&unix.IN_DELETE_SELF != 0 {
		op |= fsnotify.Remove
	}
	if mask&unix.IN_MODIFY != 0 {
		op |= fsnotify.Write
	}
	if mask&unix.IN_MOVED_FROM != 0 {
		op |= fsnotify.Rename
	}
	if mask&unix.IN_MOVE_SELF != 0 {
		op |= fsnotify.Rename
	}
	return op
}

var _ notifier = (*inotifyNotifier)(nil) // compile-time interface check
