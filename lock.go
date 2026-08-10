package magus

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"

	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/json"
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
	release, err := l.acquireAll(ctx, paths)
	if err != nil {
		return nil, err
	}
	// Idempotent: the watchdog below is a SECOND caller of release. Without this, a
	// watchdog release followed by the caller's deferred release runs removeOwner
	// twice, and between them another process can take the lock and write its own
	// sidecar - which the finished run would then delete, making the live holder
	// invisible to `magus status` and to every waiter.
	var releaseOnce sync.Once
	releaseIdempotent := func() { releaseOnce.Do(release) }
	stopWatchdog := watchWorkspaceRoot(ctx, m.ws.Root, rootWatchdogInterval, releaseIdempotent)
	return func() { stopWatchdog(); releaseIdempotent() }, nil
}

// rootWatchdogInterval is how often a lock-holding run re-checks that its workspace
// still exists. Slow enough to be free, fast enough that a deleted tree does not
// block peers for long. A const, and passed in rather than read from package scope,
// so a test can pick its own cadence without mutating shared state under -race.
const rootWatchdogInterval = 30 * time.Second

// watchWorkspaceRoot releases the run's locks if the workspace root disappears
// underneath it, and returns a stop func.
//
// This is the orphan case, made harmless. A magus process keeps running when its
// checkout is deleted - a worktree removed while a dev loop is still watching it -
// and because a flock lives exactly as long as its holder, it goes on holding every
// lock it took. Nothing notices, and every later run in that workspace simply waits,
// with no indication that the holder is never coming back.
//
// It releases rather than exits: a lock exists to prevent concurrent mutation, and a
// process whose tree is gone is not mutating anything, so continuing to hold is pure
// harm to peers. Killing the process outright is the caller's decision, not the lock
// layer's.
func watchWorkspaceRoot(ctx context.Context, root string, every time.Duration, release func()) func() {
	if root == "" || every <= 0 {
		return func() {}
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	var once sync.Once
	// Joins the goroutine, mirroring startWaitHeartbeat. Closing done alone only
	// narrows the race: a goroutine already past the inner select still reaches
	// release(), and in the daemon - one long-lived process running many invocations
	// - that late release lands on whatever the NEXT run holds.
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
				// mount, withdraws a live run's exclusivity while it keeps mutating -
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
// (<cacheDir>/locks): project "libs/diagnostics" locks <cacheDir>/locks/libs/diagnostics/lock,
// and the root project locks <cacheDir>/locks/lock. Mirroring (rather than
// flattening + sanitizing into one level) keeps the directory navigable and avoids
// the collision a flattened name would create (e.g. "libs/diagnostics" -> "libs-diagnostics"
// colliding with a real project named "libs-diagnostics").
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
	l := &projectLocker{dir: filepath.Join(cacheDir, locksDirName), noWait: noWait}
	for _, o := range opts {
		o(l)
	}
	return l
}

// reentrantErr returns the MGS3007 diagnostic when the process holding projectPath's lock
// is running one of THIS invocation's ancestors, and nil for every other contention.
//
// This is the deadlock the plain wait cannot survive. A magusfile target that runs magus
// against a project its own invocation already locked - directly, through magus.run, or
// through a script several levels down - produces a holder that is waiting for the waiter.
// flock cannot tell the two cases apart and neither can a timeout; the ancestry can, and
// it is the only signal that also covers the daemon, where holder and waiter are threads
// of ONE process and every pid comparison says "myself".
//
// It reads the owner sidecar, which is best-effort by design: a holder that wrote no
// sidecar, or an ancestry that never reached this process, yields nil and the acquire
// waits exactly as it did before. Under-detecting restores the old behavior;
// over-detecting would refuse a legitimate concurrent run, so the check errs toward
// silence.
func (l *projectLocker) reentrantErr(ctx context.Context, projectPath string) error {
	rec := l.readOwner(projectPath)
	if !types.HasInvocationAncestor(ctx, rec.PID, rec.Inv) {
		return nil
	}
	// A sidecar outlives a holder that was killed between locking and cleanup, and the
	// flock behind it may since have been taken by someone else entirely. Believing a
	// corpse here would refuse a run that should queue behind that new holder, so confirm
	// the record still describes the process this acquire is actually blocked on. Same
	// question heldLocks asks, for the same reason.
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
		// Before anything else: a lock held by one of this invocation's OWN ancestors can
		// never be released, because the ancestor is blocked waiting on this process to
		// exit. Waiting is not slow here, it is permanent, so refuse instead - ahead of
		// the noWait branch, since this diagnosis is the more specific one.
		if err := l.reentrantErr(ctx, projectPath); err != nil {
			return nil, err
		}
		if l.noWait {
			return nil, &lockContendedError{Project: projectPath}
		}
		l.emitWaiting(ctx, projectPath)
		stopWaiter := l.recordWaiter(ctx, projectPath)
		stopHeartbeat := l.startWaitHeartbeat(ctx, projectPath)
		got, err = fl.TryLockContext(ctx, lockRetryDelay)
		stopHeartbeat()
		stopWaiter()
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
		l.emitResumed(ctx, projectPath)
	}
	l.recordOwner(ctx, projectPath)
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
		return filepath.Join(l.dir, lockFileName)
	}
	return filepath.Join(l.dir, filepath.FromSlash(p), lockFileName)
}

// emitWaiting tells the user, up front, that the run is not hung: another magus
// process holds the project's lock, this run will start automatically once it frees,
// and MAGUS_NO_WAIT is the fail-fast escape hatch. It goes to stderr unconditionally
// (even under -s/--silent) - a run that stalls without explanation is the exact UX we
// are avoiding. The notify hook diverts it for tests and the console.
func (l *projectLocker) emitWaiting(ctx context.Context, projectPath string) {
	if l.notify != nil {
		l.notify(projectPath)
		return
	}
	p := projectPath
	if p == "" {
		p = "."
	}
	owner := l.describeOwner(projectPath)
	held := ""
	if owner != "" {
		held = " (held by " + owner + ")"
	}
	fmt.Fprintf(os.Stderr, "magus: project %s is being changed by another magus process%s; waiting for it to finish. This run starts automatically once it does; set MAGUS_NO_WAIT=1 to fail fast instead.\n", p, held)
	// Also as a record, so the sticky terminal region can PIN the wait. The stderr
	// line above announces the event and then scrolls away; a run that is blocked
	// needs the state to stay on screen, because the alternative a reader sees is
	// silence.
	// Structured, not the rendered line above: that string is this package's own
	// stderr output, and handing it to another package to display verbatim would put
	// presentation for a surface magus cannot see inside magus. The region composes
	// its own from these fields.
	rec := l.readOwner(projectPath)
	slog.InfoContext(ctx, "lock.waiting",
		slog.String("project", p),
		slog.Int("holder_pid", rec.PID),
		slog.String("holder_command", rec.Command))
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
func (l *projectLocker) emitResumed(ctx context.Context, projectPath string) {
	if l.notify != nil {
		return
	}
	p := projectPath
	if p == "" {
		p = "."
	}
	fmt.Fprintf(os.Stderr, "magus: lock on project %s released; starting.\n", p)
	slog.InfoContext(ctx, "lock.acquired", slog.String("project", p))
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
type processRecord struct {
	PID     int    `json:"pid"`
	Command string `json:"command"`
	Dir     string `json:"dir"`
	Started string `json:"started"`
	// Inv is the invocation that took the lock. It is what makes a holder identifiable to
	// a DESCENDANT of it - a pid cannot, since under the daemon the holder and the waiter
	// share one. Empty for a subcommand with no invocation record (clean), and for a
	// sidecar written by an older magus; a waiter then has nothing to match and waits.
	Inv string `json:"inv,omitempty"`
}

// The sidecar layout, in ONE place. HeldLocks previously re-derived these by hand
// (suffix match plus filepath.Rel), so changing lockPath would have made it silently
// return nothing instead of failing.
const (
	lockFileName = "lock"
	ownerSuffix  = ".owner"
	waiterInfix  = ".waiter."
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
	if data := selfRecord(ctx); data != nil {
		_ = os.WriteFile(l.ownerPath(projectPath), data, 0o600)
	}
}

// selfRecord marshals this invocation's identity, the payload both the owner and waiter
// sidecars carry. Returns nil when it cannot be built, which every caller treats as
// "skip the sidecar": failing to say who we are must never fail the run.
func selfRecord(ctx context.Context) []byte {
	dir, _ := os.Getwd()
	// This invocation's OWN id, never the ancestry's last element. Those look the same for
	// a run - BeginInvocation appends its id there - and differ for every lock-taker that
	// mints no invocation of its own: `magus clean` inherits an ancestry and appends
	// nothing, so the tail is its PARENT's id, and stamping that on the lock would have a
	// sibling nested run refuse a lock the parent does not hold.
	data, err := json.Marshal(processRecord{
		PID:     os.Getpid(),
		Command: strings.Join(os.Args, " "),
		Dir:     dir,
		Started: time.Now().Format(time.RFC3339),
		Inv:     journal.InvocationIDFromContext(ctx),
	})
	if err != nil {
		return nil
	}
	return data
}

// describeOwner renders the current holder for a wait message, or "" when there is
// nothing trustworthy to say.
//
// A blocked acquire already proves the holder is ALIVE - the kernel would have
// released the flock otherwise - so this never needs to probe liveness. It only
// answers which process, started when, from where.
func (l *projectLocker) describeOwner(projectPath string) string {
	o := l.readOwner(projectPath)
	if o.PID == 0 {
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

// LockStaleAfter is how long a lock is held, or a wait runs, before "busy" stops
// being the likely explanation and "abandoned" starts.
//
// Exported because it is a JUDGEMENT the whole product has to agree on. It was
// previously decided twice - two minutes here and ten in the console tile - which put
// a CLI warning that a holder "may be abandoned" beside a dashboard row still styled
// as perfectly healthy. One threshold, one place; the console reads it off the wire.
const LockStaleAfter = 10 * time.Minute

// orphanHint returns the escalated advice for a wait that has gone on too long, or
// "" while the wait is still plausibly normal.
//
// The 15s heartbeat says "this run is NOT hung", which is correct at 15 seconds and
// actively misleading at six days. Past the threshold the likeliest explanation is
// no longer a busy peer but a holder nobody knows is running, and the message says
// so along with where to look.
func orphanHint(elapsed time.Duration, owner string) string {
	if elapsed < LockStaleAfter {
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

// waiterPath is this process's waiter marker for a project. One file per blocked pid,
// so several waiters coexist without coordinating.
func (l *projectLocker) waiterPath(projectPath string) string {
	return fmt.Sprintf("%s%s%d", l.lockPath(projectPath), waiterInfix, os.Getpid())
}

// recordWaiter marks this process as blocked on a project, and returns the cleanup.
//
// Holders were the first half of the picture: they answer "who is working". Waiters
// are the other half and answer "who is stalled because of it", which is the question
// asked by whoever is looking at a queue that is not moving. Best-effort, like the
// owner record, and never load-bearing.
func (l *projectLocker) recordWaiter(ctx context.Context, projectPath string) func() {
	data := selfRecord(ctx)
	if data == nil {
		return func() {}
	}
	path := l.waiterPath(projectPath)
	if os.WriteFile(path, data, 0o600) != nil {
		return func() {}
	}
	return func() { _ = os.Remove(path) }
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
func (m *Magus) HeldLocks() []types.StatusLock {
	return heldLocks(resolveCacheDir(m.ws.Root, m.cfg))
}

// heldLocks is the cacheDir-addressed form, kept separate so a test can point it at a
// temp dir without constructing a whole workspace.
func heldLocks(cacheDir string) []types.StatusLock {
	dir := filepath.Join(cacheDir, locksDirName)
	var out []types.StatusLock
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, lockFileName+ownerSuffix) {
			return nil //nolint:nilerr // a walk error on one entry must not abort the report
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil //nolint:nilerr // one unreadable sidecar must not abort the whole report
		}
		var o processRecord
		if uerr := json.Unmarshal(data, &o); uerr != nil || o.PID == 0 {
			return nil //nolint:nilerr // a malformed sidecar is skipped, not fatal to the report
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
			StaleAfterSeconds: int(LockStaleAfter / time.Second),
		}
		lock.Waiters = readWaiters(filepath.Dir(path))
		if started, perr := time.Parse(time.RFC3339, o.Started); perr == nil {
			lock.AcquireTime = started
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
	data, err := os.ReadFile(l.ownerPath(projectPath))
	if err != nil {
		return processRecord{}
	}
	var o processRecord
	if uerr := json.Unmarshal(data, &o); uerr != nil {
		return processRecord{}
	}
	return o
}

// lockIsHeld reports whether some process currently holds the flock at path.
//
// Probing by acquisition is the only honest test: a pid check would be wrong under
// pid reuse, and the sidecar cannot answer for itself. Taking the lock to answer is
// safe because it is released immediately; the race that matters (a holder acquiring
// between the probe and the report) resolves to under-reporting for one status call,
// never to naming a process that is not there.
func lockIsHeld(path string) bool {
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

// staleWaiterAfter bounds how long a waiter marker is believed. Generous relative to
// LockStaleAfter, because a genuine wait behind a slow holder is legitimate; this only
// has to be shorter than "forever".
const staleWaiterAfter = 24 * time.Hour

// readWaiters collects the waiter markers beside a lock. Best-effort: an unreadable
// marker is skipped, and a marker whose process died before cleanup can linger, so
// the list is a snapshot rather than proof.
func readWaiters(dir string) []types.StatusLockWaiter {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []types.StatusLockWaiter
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), lockFileName+waiterInfix) {
			continue
		}
		data, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if rerr != nil {
			continue
		}
		var o processRecord
		if json.Unmarshal(data, &o) != nil || o.PID == 0 {
			continue
		}
		w := types.StatusLockWaiter{PID: o.PID, Command: o.Command, Dir: o.Dir}
		if started, perr := time.Parse(time.RFC3339, o.Started); perr == nil {
			w.WaitTime = started
			// A waiter killed while blocked never runs its own cleanup, and nothing
			// else collects these, so without an upper bound the directory grows
			// forever and status reports phantom waiters. There is no flock behind a
			// waiter marker to probe, so age is the only available signal: past a
			// bound no honest wait reaches, treat it as debris and sweep it.
			if time.Since(started) > staleWaiterAfter {
				_ = os.Remove(filepath.Join(dir, e.Name()))
				continue
			}
		}
		out = append(out, w)
	}
	slices.SortFunc(out, func(a, b types.StatusLockWaiter) int { return cmp.Compare(a.PID, b.PID) })
	return out
}
