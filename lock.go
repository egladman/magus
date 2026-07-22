package magus

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"github.com/egladman/magus/types"
)

// noWaitLocks reports whether MAGUS_NO_WAIT asks a contended workspace lock to
// fail fast instead of blocking.
func noWaitLocks() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MAGUS_NO_WAIT"))) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

// acquireProjectLocks takes the per-project EXCLUSIVE workspace lock for every
// project this invocation will mutate, in canonical sorted order (deadlock-safe),
// and returns a single release func.
//
// The lock is held ONCE for the whole invocation, at the boundary where the
// invocation begins mutating the project set - NOT around each target. The
// intra-process target scheduler fans out beneath the held lock and never
// contends on it (it is the same lock-holding process); the lock's only job is to
// keep a SEPARATE magus process from mutating the same project concurrently. This
// is the complement of the per-target `exclusive` scheduling policy, which is a
// different, intra-process concern and is left untouched.
func (m *Magus) acquireProjectLocks(ctx context.Context, projects []*types.Project) (func(), error) {
	paths := make([]string, 0, len(projects))
	for _, p := range projects {
		paths = append(paths, p.Path)
	}
	l := newProjectLocker(resolveCacheDir(m.ws.Root, m.cfg), noWaitLocks())
	return l.acquireAll(ctx, paths)
}

// A projectLocker hands out per-project advisory workspace locks that serialize
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
// Lock files mirror the workspace project tree under a single central directory
// (<cacheDir>/locks): project "libs/diag" locks <cacheDir>/locks/libs/diag/lock,
// and the root project locks <cacheDir>/locks/lock. Mirroring (rather than
// flattening + sanitizing into one level) keeps the directory navigable and avoids
// the collision a flattened name would create (e.g. "libs/diag" -> "libs-diag"
// colliding with a real project named "libs-diag").
//
// It is safe for concurrent use; each acquire opens its own OS lock handle.
type projectLocker struct {
	dir    string
	noWait bool
	notify func(projectPath string) // waiting-message hook; nil prints to stderr
}

// lockRetryDelay is how often a blocked acquire re-polls the OS lock while waiting.
const lockRetryDelay = 100 * time.Millisecond

// lockerOption configures a projectLocker.
type lockerOption func(*projectLocker)

// withLockNotify overrides where the one-shot "waiting for another magus process"
// message goes. Defaults to stderr. Used by tests to observe contention.
func withLockNotify(fn func(projectPath string)) lockerOption {
	return func(l *projectLocker) { l.notify = fn }
}

// newProjectLocker returns a projectLocker whose lock files live under
// <cacheDir>/locks, mirroring the workspace project tree. When noWait is true a
// contended acquire fails fast with a *lockContendedError instead of blocking.
func newProjectLocker(cacheDir string, noWait bool, opts ...lockerOption) *projectLocker {
	l := &projectLocker{dir: filepath.Join(cacheDir, "locks"), noWait: noWait}
	for _, o := range opts {
		o(l)
	}
	return l
}

// lockContendedError is returned by a no-wait acquire when another magus process holds
// the project's lock. It is the fail-fast signal for MAGUS_NO_WAIT.
type lockContendedError struct{ Project string }

func (e *lockContendedError) Error() string {
	p := e.Project
	if p == "" {
		p = "."
	}
	return fmt.Sprintf("magus: project %s is locked by another magus process; not waiting (MAGUS_NO_WAIT set)", p)
}

// acquire takes the project's EXCLUSIVE lock, blocking until it is free. If
// another magus process holds it, one waiting message is emitted and then the
// call blocks. With noWait the call returns a *lockContendedError immediately.
// The returned release func unlocks; call it (defer) once the invocation's
// mutating work on the project is done.
func (l *projectLocker) acquire(ctx context.Context, projectPath string) (func(), error) {
	path := l.lockPath(projectPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("workspace lock: create lock dir for %s: %w", projectPath, err)
	}
	fl := flock.New(path)

	got, err := fl.TryLock()
	if err != nil {
		return nil, fmt.Errorf("workspace lock: lock %s: %w", projectPath, err)
	}
	if !got {
		if l.noWait {
			return nil, &lockContendedError{Project: projectPath}
		}
		l.emitWaiting(projectPath)
		got, err = fl.TryLockContext(ctx, lockRetryDelay)
		if err != nil {
			return nil, fmt.Errorf("workspace lock: lock %s: %w", projectPath, err)
		}
		if !got {
			// TryLockContext only returns (false, nil) when ctx is done.
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("workspace lock: could not lock %s", projectPath)
		}
	}
	return func() { _ = fl.Unlock() }, nil
}

// acquireAll takes the EXCLUSIVE lock for every project path, acquiring them in
// canonical sorted order so two multi-project invocations can never deadlock on
// an opposing order. It returns one release func that unlocks all of them (in
// reverse). On any failure it releases whatever it already holds and returns the
// error.
func (l *projectLocker) acquireAll(ctx context.Context, projectPaths []string) (func(), error) {
	sorted := slices.Clone(projectPaths)
	slices.Sort(sorted)
	sorted = slices.Compact(sorted)

	releases := make([]func(), 0, len(sorted))
	releaseAll := func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}
	for _, p := range sorted {
		rel, err := l.acquire(ctx, p)
		if err != nil {
			releaseAll()
			return nil, err
		}
		releases = append(releases, rel)
	}
	return releaseAll, nil
}

// lockPath maps a workspace-relative project path to its lock file, mirroring the
// project tree. The root project ("." or "") locks <dir>/lock.
func (l *projectLocker) lockPath(projectPath string) string {
	p := strings.TrimSpace(projectPath)
	if p == "" || p == "." {
		return filepath.Join(l.dir, "lock")
	}
	return filepath.Join(l.dir, filepath.FromSlash(p), "lock")
}

func (l *projectLocker) emitWaiting(projectPath string) {
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
