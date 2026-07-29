package magus

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gofrs/flock"

	"github.com/egladman/magus/internal/codec"
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
		stopHeartbeat := l.startWaitHeartbeat(ctx, projectPath)
		got, err = fl.TryLockContext(ctx, lockRetryDelay)
		stopHeartbeat()
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
		l.emitResumed(projectPath)
	}
	l.recordOwner(projectPath)
	return func() { l.removeOwner(projectPath); _ = fl.Unlock() }, nil
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

// emitWaiting tells the user, up front, that the run is not hung: another magus
// process holds the project's lock, this run will start automatically once it frees,
// and MAGUS_NO_WAIT is the fail-fast escape hatch. It goes to stderr unconditionally
// (even under -s/--silent) - a run that stalls without explanation is the exact UX we
// are avoiding. The notify hook diverts it for tests and the console.
func (l *projectLocker) emitWaiting(projectPath string) {
	if l.notify != nil {
		l.notify(projectPath)
		return
	}
	p := projectPath
	if p == "" {
		p = "."
	}
	held := ""
	if owner := l.describeOwner(projectPath); owner != "" {
		held = " (held by " + owner + ")"
	}
	fmt.Fprintf(os.Stderr, "magus: project %s is being changed by another magus process%s; waiting for it to finish. This run starts automatically once it does; set MAGUS_NO_WAIT=1 to fail fast instead.\n", p, held)
}

// lockWaitHeartbeat is how often a blocked acquire reprints that it is still waiting.
// Long enough not to spam a short contention, short enough that a watcher never sits in
// silence wondering.
// A var, not a const, so a test can shorten it: the heartbeat only exists to be
// observed, so it has to be reachable in a test that finishes in milliseconds.
var lockWaitHeartbeat = 15 * time.Second

// startWaitHeartbeat reprints the wait periodically until the returned stop func is
// called.
//
// emitWaiting alone is not enough, and this is the single worst usability failure magus
// has: ONE message followed by unbounded silence is indistinguishable from a hang. A
// human tabs away and later kills the "stuck" command; an agent, which cannot see a
// terminal at all and often has the stream buffered behind a pipe, concludes magus wedged
// and kills it too - then reports that magus hangs. Both happened repeatedly against a
// perfectly healthy run that was politely queued behind `magus run serve`.
//
// A periodic line is the evidence of liveness a one-shot notice cannot give, and the
// elapsed counter is what distinguishes "the holder is working" from "the holder is
// itself stuck", which is the question the operator actually has. Suppressed when a
// notify hook is installed so tests keep counting exactly one waiting signal.
func (l *projectLocker) startWaitHeartbeat(ctx context.Context, projectPath string) func() {
	if l.notify != nil {
		return func() {}
	}
	p := projectPath
	if p == "" {
		p = "."
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		t := time.NewTicker(lockWaitHeartbeat)
		defer t.Stop()
		start := time.Now()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				elapsed := time.Since(start)
				fmt.Fprintf(os.Stderr,
					"magus: still waiting for the lock on project %s (%s elapsed); this run is NOT hung. Set MAGUS_NO_WAIT=1 to fail fast instead.%s\n",
					p, elapsed.Round(time.Second), orphanHint(elapsed, l.describeOwner(projectPath)))
			}
		}
	}()
	// Wait for the goroutine to exit so a caller that returns immediately after cannot
	// interleave a heartbeat line after the resumed/failed message.
	return func() { close(done); <-stopped }
}

// emitResumed closes the loop the waiting message opened: the other process finished
// and this run now holds the lock and proceeds. Only reached after a wait, so it never
// prints on the common uncontended path. Suppressed when a notify hook is installed so
// the hook stays the single waiting signal tests count.
func (l *projectLocker) emitResumed(projectPath string) {
	if l.notify != nil {
		return
	}
	p := projectPath
	if p == "" {
		p = "."
	}
	fmt.Fprintf(os.Stderr, "magus: lock on project %s released; starting.\n", p)
}

// lockOwner is the best-effort record of which process holds a project lock,
// written beside the lock file on acquire.
//
// Purely informational. The flock is the ONLY thing that decides exclusion; this
// record never gates, and a caller that cannot read it proceeds exactly as before.
// That split matters: a lock whose correctness depended on a hand-written pid file
// would go stale the moment a process died, whereas the kernel releases a flock
// then. Here staleness is harmless, because it is only ever read to answer "who am
// I waiting on".
//
// It exists because flock carries no identity. Without it the wait message can only
// say "another magus process", which is exactly what turned a six-day-old orphaned
// `magus run serve` - running in a worktree that had since been deleted - into an
// investigation instead of one line of output.
type lockOwner struct {
	PID     int    `json:"pid"`
	Command string `json:"command"`
	Dir     string `json:"dir"`
	Started string `json:"started"`
}

// ownerPath is the sidecar beside the lock file itself, so it inherits the same
// per-project directory layout and is removed with it.
func (l *projectLocker) ownerPath(projectPath string) string {
	return l.lockPath(projectPath) + ".owner"
}

// recordOwner writes the sidecar after a successful acquire. Every failure is
// swallowed: not being able to say who holds a lock must never fail a run that
// already holds it.
func (l *projectLocker) recordOwner(projectPath string) {
	dir, _ := os.Getwd()
	data, err := codec.Marshal(lockOwner{
		PID:     os.Getpid(),
		Command: strings.Join(os.Args, " "),
		Dir:     dir,
		Started: time.Now().Format(time.RFC3339),
	})
	if err != nil {
		return
	}
	_ = os.WriteFile(l.ownerPath(projectPath), data, 0o600)
}

// describeOwner renders the current holder for a wait message, or "" when there is
// nothing trustworthy to say.
//
// A blocked acquire already proves the holder is ALIVE - the kernel would have
// released the flock otherwise - so this never needs to probe liveness. It only
// answers which process, started when, from where.
func (l *projectLocker) describeOwner(projectPath string) string {
	data, err := os.ReadFile(l.ownerPath(projectPath))
	if err != nil {
		return ""
	}
	var o lockOwner
	if codec.Unmarshal(data, &o) != nil || o.PID == 0 {
		return ""
	}
	desc := fmt.Sprintf("pid %d", o.PID)
	if o.Command != "" {
		desc += fmt.Sprintf(" (%s)", o.Command)
	}
	if started, perr := time.Parse(time.RFC3339, o.Started); perr == nil {
		desc += fmt.Sprintf(", running %s", time.Since(started).Round(time.Second))
	}
	if o.Dir != "" {
		desc += fmt.Sprintf(", in %s", o.Dir)
	}
	return desc
}

// lockOrphanHint is how long a wait runs before the message stops reassuring and
// starts suggesting the holder may be abandoned. Well past any honest build, so a
// normal contention never sees it.
var lockOrphanHint = 2 * time.Minute

// orphanHint returns the escalated advice for a wait that has gone on too long, or
// "" while the wait is still plausibly normal.
//
// The 15s heartbeat says "this run is NOT hung", which is correct at 15 seconds and
// actively misleading at six days. Past the threshold the likeliest explanation is
// no longer a busy peer but a holder nobody knows is running, and the message says
// so along with where to look.
func orphanHint(elapsed time.Duration, owner string) string {
	if elapsed < lockOrphanHint {
		return ""
	}
	where := "the holder"
	if owner != "" {
		where = owner
	}
	return fmt.Sprintf(" This has waited %s, which is long enough that %s may be abandoned"+
		" rather than busy; check it with `magus doctor`, and note a process whose worktree"+
		" was deleted keeps running and keeps its lock.", elapsed.Round(time.Second), where)
}

// removeOwner clears the sidecar on release so a later reader never attributes a
// lock to a process that has finished. Best-effort, like the write.
func (l *projectLocker) removeOwner(projectPath string) {
	_ = os.Remove(l.ownerPath(projectPath))
}

// HeldLocks reports every per-project workspace lock currently held under cacheDir,
// read from the owner sidecars.
//
// Reported as state, not as a fault. A held lock is what a normal mutating run looks
// like; the reason to surface it is that a lock is held for exactly as long as its
// holder lives, so one held by a process nobody remembers starting is invisible and
// every other run just waits. Naming the holder makes that a fact instead of a hang.
//
// Best-effort throughout: an unreadable or malformed sidecar is skipped rather than
// failing the caller, because status must never be the thing that breaks. A sidecar
// with no live flock behind it can linger if a holder was killed between unlocking
// and cleanup, so treat an entry as a strong hint, never as proof.
func HeldLocks(cacheDir string) []types.StatusLock {
	dir := filepath.Join(cacheDir, "locks")
	var out []types.StatusLock
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, "lock.owner") {
			return nil //nolint:nilerr // a walk error on one entry must not abort the report
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		var o lockOwner
		if codec.Unmarshal(data, &o) != nil || o.PID == 0 {
			return nil
		}
		rel, rerr := filepath.Rel(dir, filepath.Dir(path))
		if rerr != nil {
			return nil
		}
		project := filepath.ToSlash(rel)
		if project == "." || project == "" {
			project = "."
		}
		lock := types.StatusLock{Project: project, PID: o.PID, Command: o.Command, Dir: o.Dir}
		if started, perr := time.Parse(time.RFC3339, o.Started); perr == nil {
			lock.Since = started
		}
		out = append(out, lock)
		return nil
	})
	slices.SortFunc(out, func(a, b types.StatusLock) int { return strings.Compare(a.Project, b.Project) })
	return out
}
