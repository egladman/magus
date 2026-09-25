package magus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"

	"github.com/egladman/magus/internal/file/record"
	"github.com/egladman/magus/internal/journal"
	procrun "github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/internal/sys/pid"
	"github.com/egladman/magus/types"
)

// A projectHold is one invocation's grip on its project locks, plus what a supersede
// watch needs in order to see a later gate asking for them.
type projectHold struct {
	locker *projectLocker
	paths  []string
	// unlock is the locker's release of every lock taken; stopWatchdog joins the
	// root-vanished watcher. Both run once, through release.
	unlock       func()
	stopWatchdog func()
	once         sync.Once
}

// release unlocks every project this hold took and stops watching the root. Idempotent:
// the stall watchdog is a second caller. Without that, a watchdog release followed by
// the deferred release runs removeOwner twice, and between them another process can take
// the lock and write its own sidecar, which the finished run would then delete, making
// the live holder invisible to `magus status` and to every refusal that names it.
func (h *projectHold) release() {
	if h.stopWatchdog != nil {
		h.stopWatchdog()
	}
	h.unlockOnce()
}

// unlockOnce is release without the join, for the root watcher itself: it runs on the
// goroutine stopWatchdog would wait for.
func (h *projectHold) unlockOnce() { h.once.Do(h.unlock) }

// yieldRequested reports the first held project a later gate is asking this run to give
// up, and the gate asking. See watchForSupersede for what the caller does with it.
func (h *projectHold) yieldRequested() (string, processRecord, bool) {
	for _, p := range h.paths {
		if rec, ok := h.locker.pendingYield(p); ok {
			return p, rec, true
		}
	}
	return "", processRecord{}, false
}

// acquireProjectLocks takes the per-project EXCLUSIVE workspace lock for every
// project this invocation will mutate, in canonical sorted order (deadlock-safe),
// and returns the hold, whose release func unlocks all of them.
//
// The lock is held ONCE for the whole invocation, at the boundary where the
// invocation begins mutating the project set, NOT around each target. The
// invocation's own target scheduler fans out beneath the held lock and never
// contends on it; the lock's job is to keep two invocations from mutating the same
// project concurrently. This is the complement of the per-target `exclusive`
// scheduling policy, which is a different, intra-invocation concern.
//
// gate marks this invocation the workspace's gate, which is what admits it to
// supersession in both directions: it may take a lock from an earlier gate on this same
// tree, and a later one may take its locks (MGS3014). See projectLocker.acquire for
// which other contentions queue and which are refused.
//
// stdio, when the invocation runs in this process, first holds the acquisition back
// while a magus upstream of it in a shell pipe still needs one of these projects (see
// ProcessStdio), and publishes the finished lock set for the stage downstream.
func (m *Magus) acquireProjectLocks(ctx context.Context, projects []*types.Project, gate bool, rw *report.Writer, stdio *ProcessStdio) (*projectHold, error) {
	paths := make([]string, 0, len(projects))
	for _, p := range projects {
		paths = append(paths, p.Path)
	}
	// The CLI and the server stamp invocation ancestry at their own entry points; a
	// LIBRARY caller (a Go test driving magus in-process) has none, so reentrantErr
	// could never fire for it and a re-entrant acquire hung instead of reporting
	// MGS3007. The env var is already in this process; read it here so the third
	// entry point is covered too, and only when nothing upstream stamped one.
	if len(types.InvocationAncestorsFromContext(ctx)) == 0 {
		ctx = types.WithInvocationAncestors(ctx, procrun.AncestorsFromEnv())
	}
	lopts := []lockerOption{withReportWriter(rw)}
	if gate {
		lopts = append(lopts, asGate())
	}
	if stdio != nil {
		lopts = append(lopts, withStdio(stdio))
	}
	l := newProjectLocker(resolveCacheDir(m.ws.Root, m.cfg), m.ws.Root, lopts...)
	unlock, _, err := l.takeRunLocks(ctx, paths)
	if err != nil {
		return nil, err
	}
	hold := &projectHold{locker: l, paths: paths, unlock: unlock}
	hold.stopWatchdog = watchWorkspaceRoot(ctx, m.ws.Root, rootWatchdogInterval, hold.unlockOnce)
	return hold, nil
}

// takeRunLocks is one invocation's whole acquisition: wait out a pipe upstream (see
// awaitUpstream), take every lock, then publish the set for the stage downstream. The
// spool is non-nil when stdin was held back and is now relayed from it.
func (l *projectLocker) takeRunLocks(ctx context.Context, paths []string) (func(), *stdinSpool, error) {
	sp, ups, err := l.awaitUpstream(ctx, paths)
	if err != nil {
		return nil, nil, err
	}
	unlock, err := l.acquireAll(ctx, paths)
	// An upstream that has stopped writing is exiting, and the kernel closes its pipe
	// before its lock files. The flock it still holds for that instant is not contention.
	for deadline := time.Now().Add(upstreamExitGrace); err != nil && l.heldByUpstream(err, ups) && time.Now().Before(deadline); {
		select {
		case <-ctx.Done():
			return nil, sp, fmt.Errorf("workspace lock: gave up waiting on an exiting upstream: %w", ctx.Err())
		case <-time.After(lockPollEvery):
		}
		unlock, err = l.acquireAll(ctx, paths)
	}
	if err != nil {
		return nil, sp, err
	}
	retract := l.publishHolds(ctx, paths)
	return func() { retract(); unlock() }, sp, nil
}

// upstreamExitGrace bounds how long a run retries a lock still held by an upstream stage
// that has stopped writing its pipe. Exit takes milliseconds; an upstream that closed
// its stdout and kept running past this is refused like any other holder.
const upstreamExitGrace = 2 * time.Second

// heldByUpstream reports a refusal whose holder is one of this run's proven upstream
// stages.
func (l *projectLocker) heldByUpstream(err error, ups []upstreamStage) bool {
	var c *lockContendedError
	if !errors.As(err, &c) {
		return false
	}
	holder := l.readOwner(c.Project).PID
	return holder != 0 && slices.ContainsFunc(ups, func(u upstreamStage) bool { return u.pid == holder })
}

// rootWatchdogInterval is how often a lock-holding run re-checks that its workspace
// still exists. Slow enough to be free, fast enough that a deleted tree does not
// block peers for long. A const, and passed in rather than read from package scope,
// so a test can pick its own cadence without mutating shared state under -race.
const rootWatchdogInterval = 30 * time.Second

// watchWorkspaceRoot releases the run's locks if the workspace root disappears
// underneath it, and returns a stop func.
//
// The orphan case, made harmless: a magus process outlives a deleted checkout and, since
// a flock lives exactly as long as its holder, goes on holding every lock it took while
// later runs are refused by a holder that is never coming back.
//
// It releases rather than exits: a process whose tree is gone is not mutating anything,
// so holding is pure harm to peers. Killing it is the caller's decision.
func watchWorkspaceRoot(ctx context.Context, root string, every time.Duration, release func()) func() {
	if root == "" || every <= 0 {
		return func() {}
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	var once sync.Once
	// Joins the goroutine. Closing done alone only narrows the race: a goroutine already past the inner select still reaches
	// release(), and in the server (one long-lived process running many invocations),
	// that late release lands on whatever the NEXT run holds.
	stop := func() {
		once.Do(func() { close(done) })
		<-stopped
	}
	go func() {
		defer close(stopped)
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				// A pending tick and a close can be ready together, and select picks
				// among ready cases at random, so re-check before acting: releasing a
				// finished run's locks would be worse than a late exit.
				select {
				case <-done:
					return
				default:
				}
				// Only a genuine absence counts. Treating every stat error as "gone"
				// means an EACCES after a permissions change, or an EIO on a network
				// mount, withdraws a live run's exclusivity while it keeps mutating:
				// the precise thing the lock exists to prevent.
				if _, err := os.Stat(root); err == nil || !errors.Is(err, fs.ErrNotExist) {
					continue
				}
				slog.WarnContext(ctx, "lock.root_vanished",
					slog.String("root", root),
					slog.String("action", "released this run's locks so peers are not blocked behind a tree that no longer exists"))
				release()
				// Returning is the whole shutdown: the deferred close(stopped) below
				// unblocks any later stop(). Calling stop() here would wait on the
				// channel this goroutine has not closed yet, and deadlock.
				return
			}
		}
	}()
	return stop
}

// A projectLocker hands out per-project advisory workspace locks that serialize
// mutating magus invocations against one another.
//
// The lock is held via an OS file lock (flock, github.com/gofrs/flock). The
// kernel releases it automatically when the holding process exits or crashes, so
// a killed magus never leaves a project wedged: this is deliberately NOT a
// PID/existence lockfile, which would strand a project after a crash.
//
// LIMITATION: the lock is ADVISORY. It serializes MAGUS processes and nothing
// else. It does NOT protect the working tree from a non-magus mutation: a raw
// `git clean`, an `rm`, or any other tool ignores it entirely. The guarantee it
// provides is "no two magus invocations mutate the same project at once", NOT
// "the tree is untouchable".
//
// Lock files mirror the workspace project tree under <cacheDir>/locks/<workspace>, so
// "libs/diagnostics" locks <dir>/libs/diagnostics/lock and the root locks <dir>/lock.
// The <workspace> segment keeps a shared cache dir from merging two trees' locks. Mirroring rather than flattening avoids the collision a
// sanitized name would create ("libs/diagnostics" -> "libs-diagnostics").
//
// It is safe for concurrent use. Each acquire opens its own OS lock handle, and the
// invocations of one process first queue on processLocks, so only one of them ever holds
// a handle on a given lock.
type projectLocker struct {
	dir string
	// root is the resolved workspace root, recorded in every sidecar. It is what makes
	// "the same tree" a comparison of paths rather than of project names: a sibling
	// worktree serves a project called "." too, and its gate judges different files.
	root string
	// started is when this invocation began taking its locks. Both sides of a supersede
	// read it off the same clock, so "later" is a comparison and never a guess.
	started time.Time
	// gate marks this invocation the whole ci target. Only a gate supersedes, and only a
	// gate is superseded.
	gate bool
	// out is where the lock's decision lines go: os.Stderr in every real run, a buffer
	// under test so it can read the exact bytes a user sees. The lines are written
	// straight to the stream, never through the console, which is why -s/--silent
	// cannot suppress any of them: a decision made without explanation is the failure
	// they exist to prevent.
	out io.Writer
	// rw is the run's record stream under a recording format (-o jsonl); when non-nil
	// the supersede decisions go there as typed events instead of the prose lines above,
	// which would otherwise be free text on a stream a caller is parsing. The lock is
	// taken before Run wraps ctx with the writer, so it is threaded in directly.
	rw *report.Writer
	// stdio is the invocation's own standard streams, set only when it runs in this
	// process. See ProcessStdio.
	stdio *ProcessStdio
}

// lockPollEvery paces the two acquires that wait on another process's flock, which has no
// wakeup to offer: a supersede, and a sibling of this invocation's own run.
const lockPollEvery = 100 * time.Millisecond

// supersedeYieldBound is how long a later gate asks an earlier gate in another process to
// stop before refusing, as it would any other contention.
//
// A bound rather than a wait, because a holder that cannot answer (wedged in a syscall,
// stopped, or too old a magus to know what a yield request is) would otherwise turn a
// supersede into the hang the whole path exists to remove. Generous against what the
// abort actually costs: cancelling a run unwinds through killing its subprocesses.
//
// A var, not a const, so a test can shorten it: this path only exists to be observed.
var supersedeYieldBound = 30 * time.Second

// lockerOption configures a projectLocker.
type lockerOption func(*projectLocker)

// asGate marks this invocation the workspace's gate. See projectLocker.gate.
func asGate() lockerOption { return func(l *projectLocker) { l.gate = true } }

// withReportWriter records the supersede decisions on w instead of printing them. nil is
// a no-op, so callers can pass the run's writer unconditionally.
func withReportWriter(w *report.Writer) lockerOption {
	return func(l *projectLocker) { l.rw = w }
}

// writingTo redirects the lock's decision lines, for a test that reads them.
func writingTo(w io.Writer) lockerOption { return func(l *projectLocker) { l.out = w } }

// newProjectLocker returns a projectLocker whose lock files live under
// <cacheDir>/locks/<workspace>, mirroring the workspace project tree.
//
// The workspace segment is what keeps the lock namespace per-WORKSPACE rather than
// per-cache-dir. An absolute cache.dir (or MAGUS_CACHE_DIR) is returned unchanged by
// resolveCacheDir for every root, which is the point (one shared cache), but without
// this it also collapses every workspace's locks together, so an unrelated tree's
// project "." contended with this one's. That is a false conflict between projects that
// share nothing, and it presents as a spurious refusal rather than an error.
func newProjectLocker(cacheDir, workspaceRoot string, opts ...lockerOption) *projectLocker {
	l := &projectLocker{
		dir:     filepath.Join(cacheDir, locksDirName, workspaceLockKey(workspaceRoot)),
		root:    resolvedRoot(workspaceRoot),
		started: time.Now(),
		out:     os.Stderr,
	}
	for _, o := range opts {
		o(l)
	}
	return l
}

// reentrantErr returns the MGS3007 diagnostic when the process holding projectPath's lock
// is running one of THIS invocation's ancestors, and nil for every other contention.
//
// The deadlock a plain wait cannot survive: a target running magus against a project its
// own invocation already locked produces a holder waiting for the waiter. Neither flock
// nor a timeout can tell that from ordinary contention; ancestry can, and it is the only
// signal that also covers the server, where holder and waiter are threads of one process.
//
// Best-effort by design: no sidecar, or an ancestry that never reached this process,
// yields nil and the contention is judged like any other. Over-detecting would refuse a
// legitimate concurrent run.
func (l *projectLocker) reentrantErr(ctx context.Context, projectPath string) error {
	rec := l.readOwner(projectPath)
	if !types.HasInvocationAncestor(ctx, rec.PID, rec.Inv) {
		return nil
	}
	// A sidecar outlives a holder that was killed between locking and cleanup, and the
	// flock behind it may since have been taken by someone else entirely. Believing a
	// corpse here would diagnose re-entry against a holder that is not an ancestor, so
	// confirm the lock is still held. Same question heldLocks asks, for the same reason.
	if !lockIsHeld(l.lockPath(projectPath)) {
		return nil
	}
	p := projectPath
	if p == "" {
		p = "."
	}
	return types.DiagnosticErrorf(types.ProjectLockHeldByAncestor,
		"project %s is locked by the magus run this one is nested inside (%s), which cannot finish until this one does."+
			" Waiting would never end, so it is refused instead."+
			" Either target a project the outer run does not hold, or express the dependency with ctx.needs(<target>)"+
			" so one invocation runs both.", p, l.describeOwner(projectPath))
}

// lockContendedError is returned when a magus process outside this invocation's own run
// holds the project's lock. It is the fail-fast signal: magus never waits on another
// magus invocation.
type lockContendedError struct {
	Project string
	Owner   string // describeOwner's rendering of the holder, "" when the sidecar says nothing
}

// lockContendedExit is the process status a contended acquire carries.
//
// 75 is EX_TEMPFAIL from BSD sysexits: the work was never attempted and the same command
// succeeds once the holder finishes. 1 leaves a caller unable to tell a busy machine from
// a broken build, so a harness has to retry genuine failures or never retry at all.
const lockContendedExit = 75

// ExitCode is read by the local exit-code seam and by the server, which forwards an
// adopted run's status by asking the error rather than naming a type it cannot import.
func (e *lockContendedError) ExitCode() int { return lockContendedExit }

func (e *lockContendedError) Error() string {
	p := e.Project
	if p == "" {
		p = "."
	}
	// Named because a fail-fast that cannot say who won leaves the caller nothing to
	// act on.
	return fmt.Sprintf("magus: project %s is locked by another magus process%s; not waiting", p, heldBy(e.Owner))
}

// heldBy renders a describeOwner string as a parenthetical, or "" when there is nothing
// trustworthy to say.
func heldBy(owner string) string {
	if owner == "" {
		return ""
	}
	return " (held by " + owner + ")"
}

// acquire takes the project's EXCLUSIVE lock. The returned release func unlocks; call it
// (defer) once the invocation's mutating work on the project is done.
//
// Who holds the lock decides what happens:
//
//   - one of this invocation's own ancestors: refused with MGS3007, since the ancestor
//     is blocked on this invocation and would never release;
//   - an earlier gate on this same tree, when this invocation is a gate: asked to stop
//     (MGS3014), and the lock is taken when it does;
//   - another invocation of this process, or a sibling of this invocation's own run in
//     another process: queued behind, until ctx ends;
//   - any other magus process: refused at once with a *lockContendedError (exit 75), and
//     so is an earlier gate in another process that does not stop within
//     supersedeYieldBound. magus never waits on another magus invocation.
func (l *projectLocker) acquire(ctx context.Context, projectPath string) (func(), error) {
	path := l.lockPath(projectPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("workspace lock: create lock dir for %s: %w", projectPath, err)
	}
	local := processLocks.join(path)
	if err := l.takeLocal(ctx, projectPath, local); err != nil {
		processLocks.leave(path, local)
		return nil, err
	}

	fl := flock.New(path)
	got, err := fl.TryLock()
	switch {
	case err != nil:
		err = fmt.Errorf("workspace lock: lock %s: %w", projectPath, err)
	case !got:
		got, err = l.contend(ctx, projectPath, fl)
		if err == nil && !got {
			err = &lockContendedError{Project: projectPath, Owner: l.describeOwner(projectPath)}
		}
	}
	if err != nil {
		local.give()
		processLocks.leave(path, local)
		return nil, err
	}
	l.recordOwner(ctx, projectPath)
	return func() {
		l.removeOwner(projectPath)
		_ = fl.Unlock()
		local.give()
		processLocks.leave(path, local)
	}, nil
}

// takeLocal takes this process's slot for a lock, queueing behind whichever invocation
// of this process holds it. The exceptions mirror contend's: an ancestor holder is
// refused, and an earlier gate is asked to yield before the wait.
func (l *projectLocker) takeLocal(ctx context.Context, projectPath string, local *localLock) error {
	if local.tryTake() {
		return nil
	}
	if err := l.reentrantErr(ctx, projectPath); err != nil {
		return err
	}
	holder, superseding := l.supersedes(projectPath)
	if superseding {
		defer l.askHolderToYield(ctx, projectPath)()
	}
	if err := local.take(ctx); err != nil {
		return fmt.Errorf("workspace lock: gave up waiting for %s: %w", projectPath, err)
	}
	if superseding {
		l.emitSuperseded(ctx, projectPath, holder)
	}
	return nil
}

// contend decides a lock another process holds, and reports whether this acquire now
// has it. false with a nil error is ordinary contention, which the caller refuses.
func (l *projectLocker) contend(ctx context.Context, projectPath string, fl *flock.Flock) (bool, error) {
	// A lock held by one of this invocation's OWN ancestors can never be released,
	// because the ancestor is blocked waiting on this process to exit.
	if err := l.reentrantErr(ctx, projectPath); err != nil {
		return false, err
	}
	// A later gate does not queue behind an earlier one on the same tree: the earlier
	// verdict is about a tree that has since changed, so ask it to stop.
	if holder, ok := l.supersedes(projectPath); ok {
		return l.takeBySuperseding(ctx, projectPath, fl, holder)
	}
	if !l.sameRun(ctx, projectPath) {
		return false, nil
	}
	// A sibling finishes without anything from this invocation, so this wait ends.
	// TODO: two siblings that each run a nested magus against the lock the other holds
	// still deadlock; neither holder records that it is blocked.
	got, err := fl.TryLockContext(ctx, lockPollEvery)
	if err != nil {
		return false, fmt.Errorf("workspace lock: gave up waiting for %s: %w", projectPath, err)
	}
	return got, nil
}

// sameRun reports a holder that belongs to this invocation's own run: another
// invocation under the same root, such as a sibling step's nested magus.
func (l *projectLocker) sameRun(ctx context.Context, projectPath string) bool {
	run := types.InvocationRoot(ctx)
	return run != "" && l.readOwner(projectPath).Run == run
}

// processLocks arbitrates project locks among the invocations of THIS process. flock
// conflicts per open file description, so without it two invocations of one process
// (the server's adopted runs, its symbol indexer) refuse each other as strangers. Only
// the invocation holding a lock's slot touches that lock's flock.
//
// Package state on purpose: the contention it arbitrates spans every Magus the process
// holds.
var processLocks = localLocks{byPath: map[string]*localLock{}}

type localLocks struct {
	mu     sync.Mutex
	byPath map[string]*localLock
}

// A localLock is one lock file's slot within this process.
type localLock struct {
	slot chan struct{} // capacity 1; full while an invocation of this process holds the lock
	refs int           // holders and waiters, guarded by localLocks.mu
}

// join returns path's slot, counting the caller until it calls leave.
func (ls *localLocks) join(path string) *localLock {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	e := ls.byPath[path]
	if e == nil {
		e = &localLock{slot: make(chan struct{}, 1)}
		ls.byPath[path] = e
	}
	e.refs++
	return e
}

// leave drops the caller's count, and the slot with the last one, so a server serving
// many worktrees keeps no slot for a lock nobody is using.
func (ls *localLocks) leave(path string, e *localLock) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	e.refs--
	if e.refs == 0 {
		delete(ls.byPath, path)
	}
}

func (e *localLock) tryTake() bool {
	select {
	case e.slot <- struct{}{}:
		return true
	default:
		return false
	}
}

func (e *localLock) take(ctx context.Context) error {
	select {
	case e.slot <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *localLock) give() { <-e.slot }

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

// workspaceLockKey identifies a workspace inside a shared lock directory. The absolute
// root is hashed rather than embedded: it is the identity that matters, a path is not a
// legal single directory name, and a digest keeps the lock tree shallow.
func workspaceLockKey(root string) string {
	sum := sha256.Sum256([]byte(resolvedRoot(root)))
	return hex.EncodeToString(sum[:8])
}

// resolvedRoot is a workspace root reduced to the one spelling every comparison uses.
// Two runs are on the same tree when this agrees, which is a stricter question than
// whether they name the same project: a sibling worktree of the same repository has the
// same project paths and different files.
func resolvedRoot(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	return filepath.Clean(abs)
}

// lockPath maps a workspace-relative project path to its lock file, mirroring the
// project tree. The root project ("." or "") locks <dir>/lock.
func (l *projectLocker) lockPath(projectPath string) string {
	p := strings.TrimSpace(projectPath)
	if p == "" || p == "." {
		return filepath.Join(l.dir, lockFileName)
	}
	return filepath.Join(l.dir, filepath.FromSlash(p), lockFileName)
}

// lockOwner is the best-effort record of which process holds a project lock,
// written beside the lock file on acquire.
//
// Purely informational: the flock alone decides exclusion, so staleness here is harmless
// and a caller that cannot read it proceeds as before. A lock whose correctness depended
// on a hand-written pid file would go stale the moment a process died.
//
// It exists because flock carries no identity. Without it a refusal can only say
// "another magus process", which turned a six-day-old orphaned `magus run serve` in a
// deleted worktree into an investigation instead of one line of output.
type processRecord struct {
	PID     int       `record:"pid"`
	Command string    `record:"command"`
	Dir     string    `record:"dir"`
	Started time.Time `record:"started,omitempty"`
	// Inv is the invocation that took the lock. It is what makes a holder identifiable to
	// a DESCENDANT of it: a pid cannot, since under the server the holder and the waiter
	// share one. Empty for a subcommand with no invocation record (clean), and for a
	// sidecar written by an older magus; an acquirer then has nothing to match.
	Inv string `record:"invocation,omitempty"`
	// Run is the outermost invocation the holder runs underneath (types.InvocationRoot).
	// A holder with the same Run as the acquirer is a sibling in the same run, and is
	// waited for rather than refused. Empty when the holder carried no ancestry, which
	// matches nothing.
	Run string `record:"run,omitempty"`
	// Root is the resolved workspace root, and Gate says the invocation is the whole ci
	// target. Together they are the supersede qualifier: same tree, both gates.
	//
	// omitempty is what makes two magus versions safe to run against one lock directory,
	// which two checkouts sharing a cache dir already do. A sidecar written by a magus
	// that predates supersession lacks both lines and decodes to the zero value, which
	// disqualifies its holder, leaving it refused like any other; a sidecar carrying
	// them decodes in the older magus too, because a record reader looks up the field
	// names it knows and ignores every other line.
	Root string `record:"root,omitempty"`
	Gate bool   `record:"gate,omitempty"`
}

// The sidecar layout, in ONE place. HeldLocks previously re-derived these by hand
// (suffix match plus filepath.Rel), so changing lockPath would have made it silently
// return nothing instead of failing.
const (
	lockFileName = "lock"
	ownerSuffix  = ".owner"
	yieldSuffix  = ".yield"
	locksDirName = "locks"
)

// ownerPath is the sidecar beside the lock file itself, so it inherits the same
// per-project directory layout and is removed with it.
func (l *projectLocker) ownerPath(projectPath string) string {
	return l.lockPath(projectPath) + ownerSuffix
}

// recordOwner writes the sidecar after a successful acquire. Every failure is
// swallowed: not being able to say who holds a lock must never fail a run that
// already holds it.
func (l *projectLocker) recordOwner(ctx context.Context, projectPath string) {
	// The owner's Started is when this invocation began LOCKING, not when the sidecar was
	// written, so a supersede compares two runs against one clock.
	_ = record.Write(l.ownerPath(projectPath), l.selfRecord(ctx, l.started))
	l.clearStaleYield(projectPath)
}

// selfRecord builds this invocation's identity, the payload both the owner and
// yield sidecars carry. Stored as one cattable file, so a stuck run is diagnosable
// with cat alone:
//
//	$ cat .magus/locks/*/lock.owner
//	command	magus run ci .
//	gate	true
//	pid	41221
func (l *projectLocker) selfRecord(ctx context.Context, started time.Time) processRecord {
	dir, _ := os.Getwd()
	// This invocation's OWN id, never the ancestry's last element. Those look the same for
	// a run (BeginInvocation appends its id there), and differ for every lock-taker that
	// mints no invocation of its own: `magus clean` inherits an ancestry and appends
	// nothing, so the tail is its PARENT's id, and stamping that on the lock would have a
	// sibling nested run refuse a lock the parent does not hold.
	return processRecord{
		PID:     os.Getpid(),
		Command: strings.Join(os.Args, " "),
		Dir:     dir,
		Started: started,
		Inv:     journal.InvocationIDFromContext(ctx),
		Run:     types.InvocationRoot(ctx),
		Root:    l.root,
		Gate:    l.gate,
	}
}

// describeOwner renders the current holder for a refusal, or "" when there is nothing
// trustworthy to say.
//
// A contended acquire already proves the holder is ALIVE (the kernel would have
// released the flock otherwise), so this never needs to probe liveness. It only
// answers which process, started when, from where.
func (l *projectLocker) describeOwner(projectPath string) string {
	return l.readOwner(projectPath).describe()
}

// describe is describeOwner's rendering, over a record the caller already read. One
// holder reads one way whether the reader is a fail-fast or a supersede.
func (o processRecord) describe() string {
	if o.PID == 0 {
		return ""
	}
	desc := fmt.Sprintf("pid %d", o.PID)
	if o.Command != "" {
		desc += fmt.Sprintf(" (%s)", o.Command)
	}
	if !o.Started.IsZero() {
		desc += fmt.Sprintf(", running %s", time.Since(o.Started).Round(time.Second))
	}
	if o.Dir != "" {
		desc += fmt.Sprintf(", in %s", o.Dir)
	}
	return desc
}

// lockStaleAfter is how long a lock is held before "busy" stops being the likely
// explanation and "abandoned" starts.
//
// It is a judgment every renderer has to share, so it rides the wire as
// StaleAfterSeconds rather than being decided again in each one: two thresholds once put
// a CLI warning that a holder "may be abandoned" beside a console row styled as healthy.
const lockStaleAfter = 10 * time.Minute

// yieldPath is the supersede request beside the lock file: one marker per project, naming
// the later gate that wants it.
func (l *projectLocker) yieldPath(projectPath string) string {
	return l.lockPath(projectPath) + yieldSuffix
}

// supersedes reports the holder of projectPath when this invocation should TAKE the lock
// from it, and the holder to name when it does.
//
// The qualifier is narrow on purpose, because the cost of a false positive is a killed
// run that was doing real work. All four must hold: this invocation is a gate, the holder
// is a gate, both resolve to the same workspace root, and the holder began first. A
// sibling worktree is a different tree, a `run build` meeting a `run test` is not a pair
// of gates, and a holder with no start time on record cannot be proven earlier; none of
// them is superseded, because a supersede that cannot prove it is the later run is a
// coin toss.
//
// An ancestor holding the lock never reaches here: reentrantErr answers that one first,
// and MGS3007 stays the more specific diagnosis.
func (l *projectLocker) supersedes(projectPath string) (processRecord, bool) {
	if !l.gate || l.root == "" {
		return processRecord{}, false
	}
	rec := l.readOwner(projectPath)
	if rec.PID == 0 || !rec.Gate || rec.Root != l.root {
		return processRecord{}, false
	}
	if rec.Started.IsZero() || !rec.Started.Before(l.started) {
		return processRecord{}, false
	}
	return rec, true
}

// pendingYield reports a LIVE supersede request against a lock this invocation holds: a
// later gate on this same tree, still running, waiting on it.
//
// The mirror of supersedes, read from the holder's side, and the same start-time
// comparison is what makes a marker self-expiring. A request from a gate that began
// before this holder cannot be for this holder, so a marker left behind by a waiter that
// died is inert rather than a trap for whoever takes the lock next. A marker from a
// LATER gate that died while parked is the trap that comparison cannot catch, so the
// requester's pid is probed too: a request nobody is waiting on stops nothing, and is
// swept here so the next holder does not read it either.
func (l *projectLocker) pendingYield(projectPath string) (processRecord, bool) {
	if !l.gate || l.root == "" {
		return processRecord{}, false
	}
	rec := readRecord(l.yieldPath(projectPath))
	if rec.PID == 0 || !rec.Gate || rec.Root != l.root {
		return processRecord{}, false
	}
	if !l.started.Before(rec.Started) {
		return processRecord{}, false
	}
	if !pid.Alive(rec.PID) {
		_ = record.Remove(l.yieldPath(projectPath))
		return processRecord{}, false
	}
	return rec, true
}

// askHolderToYield publishes this gate's claim on a held lock and returns the retraction.
//
// A marker file rather than a signal, and the reason is what the abort has to SAY. A
// signal carries no identity, so the aborted run could not name who superseded it, and
// nothing could tell this from the Ctrl-C or the supervisor SIGTERM the CLI already
// handles as an interrupt; SIGTERM is not deliverable on Windows at all; and under the
// server the holder and the waiter can be threads of one process, where signalling the
// pid means signalling yourself. The marker carries the successor's pid, command and
// start time, which is the whole of MGS3014's message.
func (l *projectLocker) askHolderToYield(ctx context.Context, projectPath string) func() {
	path := l.yieldPath(projectPath)
	if record.Write(path, l.selfRecord(ctx, l.started)) != nil {
		return func() {}
	}
	return func() { _ = record.Remove(path) }
}

// clearStaleYield drops a supersede request that can no longer be for this holder, so a
// waiter killed between writing the marker and taking the lock leaves no debris.
//
// Deliberately NOT gated on this invocation being a gate, unlike pendingYield: whoever
// wins the lock does the sweeping, and an ordinary run that took it must not delete a
// request a gate is still waiting on. The start-time comparison is the whole test, and it
// is the same one that makes a marker self-expiring.
func (l *projectLocker) clearStaleYield(projectPath string) {
	rec := readRecord(l.yieldPath(projectPath))
	if rec.PID != 0 && l.started.Before(rec.Started) {
		return
	}
	_ = record.Remove(l.yieldPath(projectPath))
}

// takeBySuperseding asks the holder to stop, waits a BOUNDED time for the lock, and
// reports whether it got it. A false return means the caller refuses the acquire as
// ordinary contention (exit 75), the same as a holder that never qualified for
// supersession; the error is non-nil only when the CALLER's context ended, since the
// bound expiring is a fallback rather than a failure.
func (l *projectLocker) takeBySuperseding(ctx context.Context, projectPath string, fl *flock.Flock, holder processRecord) (bool, error) {
	retract := l.askHolderToYield(ctx, projectPath)
	waitCtx, cancel := context.WithTimeout(ctx, supersedeYieldBound)
	defer cancel()
	got, err := fl.TryLockContext(waitCtx, lockPollEvery)
	retract()
	if err != nil || !got {
		if ctx.Err() != nil {
			return false, fmt.Errorf("workspace lock: gave up waiting for %s: %w", projectPath, ctx.Err())
		}
		l.emitSupersedeRefused(projectPath, holder)
		return false, nil
	}
	l.emitSuperseded(ctx, projectPath, holder)
	return true, nil
}

// emitSupersedeRefused says the earlier gate did not stop within the bound, so this run
// is refused: a record of its own, because nothing was superseded.
func (l *projectLocker) emitSupersedeRefused(projectPath string, holder processRecord) {
	p := projectPath
	if p == "" {
		p = "."
	}
	if l.rw != nil {
		_ = report.Record(l.rw, report.LockSupersedeRefused{
			Project: p, HolderPID: holder.PID, Command: holder.Command, BoundMs: supersedeYieldBound.Milliseconds(),
		})
		return
	}
	fmt.Fprintf(l.out, "magus: the earlier gate on project %s did not stop within %s; refusing instead of waiting for it.\n", p, supersedeYieldBound)
}

// emitSuperseded states the decision, once, on the run that made it. It is a decision and
// not progress, so it goes to l.out like every other lock line and -s cannot suppress
// it: a gate that silently killed another run is worse than one that never did.
func (l *projectLocker) emitSuperseded(ctx context.Context, projectPath string, holder processRecord) {
	p := projectPath
	if p == "" {
		p = "."
	}
	if l.rw != nil {
		_ = report.Record(l.rw, report.LockSuperseded{Project: p, HolderPID: holder.PID, Command: holder.Command})
		return
	}
	fmt.Fprintf(l.out, "magus: superseded the earlier gate on project %s%s; its verdict would have described a tree that has since changed.\n",
		p, heldBy(holder.describe()))
	slog.InfoContext(ctx, "lock.superseded",
		slog.String("project", p),
		slog.Int("holder_pid", holder.PID),
		slog.String("holder_command", holder.Command))
}

// removeOwner clears the sidecar on release so a later reader never attributes a
// lock to a process that has finished. Best-effort, like the write.
func (l *projectLocker) removeOwner(projectPath string) {
	_ = record.Remove(l.ownerPath(projectPath))
}

// HeldLocks reports every per-project workspace lock currently held under cacheDir,
// read from the owner sidecars.
//
// Reported as state, not as a fault: a held lock is what a normal mutating run looks
// like, but one held by a process nobody remembers starting refuses every other run.
// Naming the holder makes that a fact instead of a mystery.
//
// Best-effort throughout: an unreadable sidecar is skipped rather than failing the
// caller. A sidecar can outlive its flock if a holder was killed between unlocking and
// cleanup, so treat an entry as a strong hint, never proof.
func (m *Magus) HeldLocks() []types.StatusLock {
	return heldLocks(resolveCacheDir(m.ws.Root, m.cfg), m.ws.Root)
}

// heldLocks is the cacheDir-addressed form, kept separate so a test can point it at a
// temp dir without constructing a whole workspace. It reports only THIS workspace's
// locks: under a shared cache dir the others are unrelated runs, and their project
// paths mean nothing here.
func heldLocks(cacheDir, workspaceRoot string) []types.StatusLock {
	dir := filepath.Join(cacheDir, locksDirName, workspaceLockKey(workspaceRoot))
	var out []types.StatusLock
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		// The sidecar is a FILE holding the whole record, so this matches on the name and
		// takes only files: a directory of that name is not one of ours.
		if err != nil || d.IsDir() || !strings.HasSuffix(path, lockFileName+ownerSuffix) {
			return nil //nolint:nilerr // a walk error on one entry must not abort the report
		}
		o := readRecord(path)
		if o.PID == 0 {
			return nil // unreadable or malformed: skipped, not fatal to the report
		}
		rel, rerr := filepath.Rel(dir, filepath.Dir(path))
		if rerr != nil {
			return nil //nolint:nilerr // an unrelatable path is skipped, not fatal to the report
		}
		project := filepath.ToSlash(rel)
		if project == "." || project == "" {
			project = "."
		}
		// A sidecar outlives a SIGKILLed holder, because only removeOwner deletes it
		// and a killed process never runs it. Ask the kernel instead: if the flock can
		// be taken, nothing holds it and the sidecar is a corpse. Reporting a dead pid
		// as the holder is worse than reporting nothing, because the escalated hint
		// then points a user at a process that does not exist.
		if !lockIsHeld(strings.TrimSuffix(path, ownerSuffix)) {
			return nil
		}
		lock := types.StatusLock{
			Project: project, PID: o.PID, Command: o.Command, Dir: o.Dir,
			StaleAfterSeconds: int(lockStaleAfter / time.Second),
			AcquireTime:       o.Started,
		}
		out = append(out, lock)
		return nil
	})
	slices.SortFunc(out, func(a, b types.StatusLock) int { return strings.Compare(a.Project, b.Project) })
	return out
}

// readOwner decodes the owner sidecar, or returns a zero record when there is nothing
// trustworthy to read. The structured form both the stderr line and the sticky region
// are built from, so neither has to parse the other's text.
func (l *projectLocker) readOwner(projectPath string) processRecord {
	return readRecord(l.ownerPath(projectPath))
}

// readRecord decodes a sidecar, or returns a zeroed record when there is nothing
// trustworthy to read. PID == 0 is what every caller already tests for "nothing to
// say", so an absent, malformed, or unreadable sidecar collapse to one answer here,
// which is safe only because these records are informational. The flock decides
// exclusion; nothing branches on this being present.
func readRecord(dir string) processRecord {
	var rec processRecord
	if err := record.Read(dir, &rec); err != nil {
		return processRecord{}
	}
	return rec
}

// lockIsHeld reports whether some process currently holds the flock at path.
//
// Probing by acquisition is the only honest test: a pid check would be wrong under
// pid reuse, and the sidecar cannot answer for itself. Taking the lock to answer is
// safe because it is released immediately; the race that matters (a holder acquiring
// between the probe and the report) resolves to under-reporting for one status call,
// never to naming a process that is not there.
//
// It probes through processLocks: an invocation of this process holding the slot is an
// answer on its own, and a probe that took the flock outside the slot would read to an
// in-process acquirer as another process holding it.
func lockIsHeld(path string) bool {
	local := processLocks.join(path)
	defer processLocks.leave(path, local)
	if !local.tryTake() {
		return true
	}
	defer local.give()
	fl := flock.New(path)
	got, err := fl.TryLock()
	if err != nil {
		return true // cannot tell; assume held rather than erase a real holder
	}
	if got {
		_ = fl.Unlock()
		return false
	}
	return true
}
