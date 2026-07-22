// Package wslock provides a per-project advisory workspace lock that serializes
// mutating magus invocations against one another.
//
// The lock is held via an OS file lock (flock, github.com/gofrs/flock). The
// kernel releases it automatically when the holding process exits or crashes, so
// a killed magus never leaves a project wedged - this is deliberately NOT a
// PID/existence lockfile, which would strand a project after a crash.
//
// LIMITATION - the lock is ADVISORY. It serializes MAGUS processes and nothing
// else. It does NOT protect the working tree from a non-magus mutation: a raw
// `git clean`, an `rm`, or any other tool ignores it entirely. The guarantee it
// provides is "no two magus invocations mutate the same project at once", NOT
// "the tree is untouchable".
//
// A mutating operation on a project takes that project's EXCLUSIVE lock; a
// read-only operation takes its SHARED lock. Lock files mirror the workspace
// project tree under a single central directory (<cacheDir>/locks): project
// "libs/diag" locks <cacheDir>/locks/libs/diag/lock, and the root project locks
// <cacheDir>/locks/lock. Mirroring (rather than flattening + sanitizing into one
// level) keeps the directory navigable and avoids the collision a flattened name
// would create (e.g. "libs/diag" -> "libs-diag" colliding with a real project
// named "libs-diag").
package wslock

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

// retryDelay is how often a blocked acquire re-polls the OS lock while waiting.
const retryDelay = 100 * time.Millisecond

// Locker hands out per-project advisory locks rooted at a single central lock
// directory that mirrors the workspace project tree. It is safe for concurrent
// use; each acquire opens its own OS lock handle.
type Locker struct {
	dir    string
	noWait bool
	notify func(projectPath string) // waiting-message hook; nil prints to stderr
}

// Option configures a Locker.
type Option func(*Locker)

// WithNotify overrides where the one-shot "waiting for another magus process"
// message goes. Defaults to stderr. Used by tests to observe contention.
func WithNotify(fn func(projectPath string)) Option {
	return func(l *Locker) { l.notify = fn }
}

// New returns a Locker whose lock files live under <cacheDir>/locks, mirroring
// the workspace project tree. When noWait is true a contended acquire fails fast
// with a *Contended error instead of blocking.
func New(cacheDir string, noWait bool, opts ...Option) *Locker {
	l := &Locker{dir: filepath.Join(cacheDir, "locks"), noWait: noWait}
	for _, o := range opts {
		o(l)
	}
	return l
}

// Contended is returned by a no-wait acquire when another magus process holds
// the project's lock. It is the fail-fast signal for MAGUS_NO_WAIT.
type Contended struct{ Project string }

func (e *Contended) Error() string {
	p := e.Project
	if p == "" {
		p = "."
	}
	return fmt.Sprintf("magus: project %s is locked by another magus process; not waiting (MAGUS_NO_WAIT set)", p)
}

// Acquire takes the project's EXCLUSIVE lock, blocking until it is free. If
// another magus process holds it, one waiting message is emitted and then the
// call blocks. With noWait the call returns a *Contended error immediately. The
// returned release func unlocks; call it (defer) once the invocation's mutating
// work on the project is done.
func (l *Locker) Acquire(ctx context.Context, projectPath string) (func(), error) {
	return l.acquire(ctx, projectPath, false)
}

// AcquireShared takes the project's SHARED (read) lock. Concurrent readers share
// it; it excludes only an exclusive holder. Same contention/no-wait behavior as
// Acquire.
func (l *Locker) AcquireShared(ctx context.Context, projectPath string) (func(), error) {
	return l.acquire(ctx, projectPath, true)
}

// AcquireAll takes the EXCLUSIVE lock for every project path, acquiring them in
// canonical sorted order so two multi-project invocations can never deadlock on
// an opposing order. It returns one release func that unlocks all of them (in
// reverse). On any failure it releases whatever it already holds and returns the
// error.
func (l *Locker) AcquireAll(ctx context.Context, projectPaths []string) (func(), error) {
	sorted := append([]string(nil), projectPaths...)
	sort.Strings(sorted)
	sorted = dedup(sorted)

	releases := make([]func(), 0, len(sorted))
	releaseAll := func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}
	for _, p := range sorted {
		rel, err := l.acquire(ctx, p, false)
		if err != nil {
			releaseAll()
			return nil, err
		}
		releases = append(releases, rel)
	}
	return releaseAll, nil
}

func (l *Locker) acquire(ctx context.Context, projectPath string, shared bool) (func(), error) {
	path := l.lockPath(projectPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("wslock: create lock dir for %s: %w", projectPath, err)
	}
	fl := flock.New(path)

	tryOnce := fl.TryLock
	tryWait := fl.TryLockContext
	if shared {
		tryOnce = fl.TryRLock
		tryWait = fl.TryRLockContext
	}

	got, err := tryOnce()
	if err != nil {
		return nil, fmt.Errorf("wslock: lock %s: %w", projectPath, err)
	}
	if !got {
		if l.noWait {
			return nil, &Contended{Project: projectPath}
		}
		l.emitWaiting(projectPath)
		got, err = tryWait(ctx, retryDelay)
		if err != nil {
			return nil, fmt.Errorf("wslock: lock %s: %w", projectPath, err)
		}
		if !got {
			// TryLockContext only returns (false, nil) when ctx is done.
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("wslock: could not lock %s", projectPath)
		}
	}
	return func() { _ = fl.Unlock() }, nil
}

// lockPath maps a workspace-relative project path to its lock file, mirroring the
// project tree. The root project ("." or "") locks <dir>/lock.
func (l *Locker) lockPath(projectPath string) string {
	p := strings.TrimSpace(projectPath)
	if p == "" || p == "." {
		return filepath.Join(l.dir, "lock")
	}
	return filepath.Join(l.dir, filepath.FromSlash(p), "lock")
}

func (l *Locker) emitWaiting(projectPath string) {
	if l.notify != nil {
		l.notify(projectPath)
		return
	}
	p := projectPath
	if p == "" {
		p = "."
	}
	fmt.Fprintf(os.Stderr, "magus: waiting for another magus process to finish on project %s...\n", p)
}

// dedup returns s with adjacent duplicates removed; s must be sorted.
func dedup(s []string) []string {
	out := s[:0]
	var last string
	for i, v := range s {
		if i == 0 || v != last {
			out = append(out, v)
			last = v
		}
	}
	return out
}
