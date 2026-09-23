package magus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egladman/magus/internal/file/record"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/types"
)

// TestSameProjectExclusiveFailsFastThenSucceedsAfterRelease proves two exclusive
// holders of the SAME project's lock cannot overlap: the second is refused immediately
// while the first holds it (magus never waits on another magus process), and succeeds
// once the first releases. Uses two OS processes (a subprocess holds the lock) so it
// exercises the real kernel flock, not an in-process handle.
func TestSameProjectExclusiveFailsFastThenSucceedsAfterRelease(t *testing.T) {
	cacheDir := t.TempDir()
	lockDir := filepath.Join(cacheDir, "locks", workspaceLockKey(testWorkspaceRoot))

	holdMS := 300
	cmd := helperHold(t, cacheDir, "app", holdMS)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start holder: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	// Wait until the subprocess has actually taken the lock (it signals by
	// creating a ready file), so we know our acquire genuinely contends.
	waitForFile(t, filepath.Join(lockDir, "app", "ready"), 3*time.Second)

	locker := newProjectLocker(cacheDir, testWorkspaceRoot)
	start := time.Now()
	_, err := locker.acquire(context.Background(), "app")
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("acquire against a held lock took %v; want an immediate refusal", elapsed)
	}
	var c *lockContendedError
	if !errors.As(err, &c) {
		t.Fatalf("want *lockContendedError while the subprocess holds it, got %v", err)
	}

	if err := cmd.Wait(); err != nil {
		t.Fatalf("holder subprocess failed: %v", err)
	}
	release, err := locker.acquire(context.Background(), "app")
	if err != nil {
		t.Fatalf("acquire after the holder released: %v", err)
	}
	release()
}

// TestDifferentProjectsNoContention proves two DIFFERENT projects' exclusive
// locks are held concurrently, with no false contention.
func TestDifferentProjectsNoContention(t *testing.T) {
	locker := newProjectLocker(t.TempDir(), testWorkspaceRoot)

	relA, err := locker.acquire(context.Background(), "libs/diagnostics")
	if err != nil {
		t.Fatalf("acquire A: %v", err)
	}
	defer relA()

	relB, err := locker.acquire(context.Background(), "libs/textsearch")
	if err != nil {
		t.Fatalf("acquire B (different project) should not contend: %v", err)
	}
	defer relB()
}

// TestReleaseFrees proves a released lock can be re-taken immediately.
func TestReleaseFrees(t *testing.T) {
	locker := newProjectLocker(t.TempDir(), testWorkspaceRoot)

	rel, err := locker.acquire(context.Background(), "docs")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	rel()

	rel2, err := locker.acquire(context.Background(), "docs")
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	rel2()
}

// TestReleasedOnProcessExit proves the OS releases the lock when the holding
// process exits (crash-safety), without any explicit unlock: the subprocess is
// killed while holding the lock, and we then acquire it.
func TestReleasedOnProcessExit(t *testing.T) {
	cacheDir := t.TempDir()
	lockDir := filepath.Join(cacheDir, "locks", workspaceLockKey(testWorkspaceRoot))

	// Hold effectively forever; we kill it.
	cmd := helperHold(t, cacheDir, "svc", 60_000)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start holder: %v", err)
	}
	waitForFile(t, filepath.Join(lockDir, "svc", "ready"), 3*time.Second)

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill holder: %v", err)
	}
	_, _ = cmd.Process.Wait()

	// The kernel must have dropped the lock on process death, so this acquires cleanly
	// rather than being refused as still-held.
	locker := newProjectLocker(cacheDir, testWorkspaceRoot)
	rel, err := locker.acquire(context.Background(), "svc")
	if err != nil {
		t.Fatalf("acquire after holder death (lock should be released): %v", err)
	}
	rel()
}

// TestContendedAcquireFailsFast pins the owner decision: magus never waits on another
// magus process. A project lock held by a different process is refused immediately with
// *lockContendedError, naming the holder, rather than blocked on.
func TestContendedAcquireFailsFast(t *testing.T) {
	cacheDir := t.TempDir()
	lockDir := filepath.Join(cacheDir, "locks", workspaceLockKey(testWorkspaceRoot))

	cmd := helperHold(t, cacheDir, "p", 5_000)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start holder: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	waitForFile(t, filepath.Join(lockDir, "p", "ready"), 3*time.Second)

	// The helper inherits this process's directory, so this is the cwd it recorded.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	locker := newProjectLocker(cacheDir, testWorkspaceRoot)
	start := time.Now()
	_, err = locker.acquire(context.Background(), "p")
	if time.Since(start) > time.Second {
		t.Fatalf("acquire blocked instead of failing fast")
	}
	var c *lockContendedError
	if !errors.As(err, &c) {
		t.Fatalf("want *lockContendedError error, got %v", err)
	}
	if c.Project != "p" {
		t.Fatalf("Contended.Project = %q, want %q", c.Project, "p")
	}

	// A fail-fast that cannot say who won is not actionable, and pid, command, age and
	// the held project (above) are what a caller needs to go look.
	msg := c.Error()
	for _, want := range []string{
		fmt.Sprintf("pid %d", cmd.Process.Pid),
		"-test.run=TestHelperHold",
		wd,
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("contended error does not name the holder's %q:\n%s", want, msg)
		}
	}

	// EX_TEMPFAIL, not 1: a harness branches machine-busy against build-broken on this.
	if got := c.ExitCode(); got != 75 {
		t.Fatalf("ExitCode() = %d, want 75", got)
	}
}

// TestAcquireAllSortedNoDeadlock proves multi-project acquisition in sorted order
// is deadlock-safe: two goroutines each lock the same set given in OPPOSING
// orders and both complete. Sorted acquisition means neither can hold one lock
// while waiting on another the peer holds.
// TestAcquireAllReleasesOnContention proves acquireAll rolls back whatever it already
// holds when a later lock in the sorted set is held by a different process, rather than
// leaving a partial acquisition in place.
//
// This replaces a test that ran two goroutines acquiring {a,b,c} and {c,b,a} concurrently
// to prove sorted acquisition could not deadlock via opposing lock order. Under fail-fast
// that guarantee is now unconditional: acquire never blocks, so two invocations can never
// wait on each other and a mutual-wait deadlock cannot occur by construction. What is left
// to prove is that a partial failure inside acquireAll cleans up after itself.
func TestAcquireAllReleasesOnContention(t *testing.T) {
	cacheDir := t.TempDir()
	locker := newProjectLocker(cacheDir, testWorkspaceRoot)

	// A second locker stands in for a different magus process holding "b".
	holder := newProjectLocker(cacheDir, testWorkspaceRoot)
	relB, err := holder.acquire(context.Background(), "b")
	if err != nil {
		t.Fatalf("holder acquire: %v", err)
	}
	defer relB()

	_, err = locker.acquireAll(context.Background(), []string{"a", "b", "c"})
	var c *lockContendedError
	if !errors.As(err, &c) {
		t.Fatalf("want *lockContendedError for the held project, got %v", err)
	}
	if c.Project != "b" {
		t.Fatalf("Contended.Project = %q, want %q", c.Project, "b")
	}

	// "a" sorts before "b" and so was already taken when "b" failed; acquireAll must
	// have released it rather than leaking a partial hold.
	relA, err := locker.acquire(context.Background(), "a")
	if err != nil {
		t.Fatalf("acquire %q after acquireAll failed: %v; a partial hold was leaked", "a", err)
	}
	relA()
}

// TestLockPathMirrorsProjectTree proves lock files mirror the project tree and
// the root project maps to <dir>/lock.
func TestLockPathMirrorsProjectTree(t *testing.T) {
	l := newProjectLocker("/cache", testWorkspaceRoot)
	base := filepath.Join("/cache", "locks", workspaceLockKey(testWorkspaceRoot))
	cases := map[string]string{
		"":                 filepath.Join(base, "lock"),
		".":                filepath.Join(base, "lock"),
		"docs":             filepath.Join(base, "docs", "lock"),
		"libs/diagnostics": filepath.Join(base, "libs", "diagnostics", "lock"),
		"libs/textsearch":  filepath.Join(base, "libs", "textsearch", "lock"),
	}
	for in, want := range cases {
		if got := l.lockPath(in); got != want {
			t.Errorf("lockPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// testWorkspaceRoot is the workspace these lockers belong to. Any fixed path does:
// the locker only hashes it to keep one workspace's locks out of another's.
const testWorkspaceRoot = "/ws"

// TestLockNamespaceIsPerWorkspace proves a SHARED cache dir does not merge two
// workspaces' locks. An absolute cache.dir (or MAGUS_CACHE_DIR) resolves to the same
// path for every root (that is the point, one cache), but it used to collapse the
// lock tree too, so an unrelated checkout's project "." blocked on this one's and
// presented as a hang rather than an error.
func TestLockNamespaceIsPerWorkspace(t *testing.T) {
	const shared = "/cache"
	a := newProjectLocker(shared, "/ws/one")
	b := newProjectLocker(shared, "/ws/two")

	for _, project := range []string{"", ".", "docs", "libs/diagnostics"} {
		if a.lockPath(project) == b.lockPath(project) {
			t.Errorf("project %q: two workspaces share a lock file %q", project, a.lockPath(project))
		}
	}
	// Same workspace still means the same lock, or the lock stops excluding anything.
	if got, want := newProjectLocker(shared, "/ws/one").lockPath("docs"), a.lockPath("docs"); got != want {
		t.Errorf("same workspace produced different lock paths: %q vs %q", got, want)
	}
}

// --- test helpers -----------------------------------------------------------

// helperHold builds a command that runs TestHelperHold in a subprocess, which
// acquires the given project's exclusive lock, signals readiness, and holds for
// holdMS milliseconds. Running in a separate process exercises the real OS lock.
func helperHold(t *testing.T, cacheDir, project string, holdMS int) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperHold")
	cmd.Env = append(os.Environ(),
		"LOCKTEST_HELPER=1",
		"LOCKTEST_CACHE_DIR="+cacheDir,
		"LOCKTEST_PROJECT="+project,
		"LOCKTEST_HOLD_MS="+strconv.Itoa(holdMS),
	)
	cmd.Stderr = os.Stderr
	return cmd
}

// TestHelperHold is not a real test; it is the subprocess entry point invoked by
// helperHold. It acquires the lock directly via flock semantics (through the
// Locker), writes a ready file, and sleeps.
func TestHelperHold(t *testing.T) {
	if os.Getenv("LOCKTEST_HELPER") != "1" {
		t.Skip("subprocess helper; not run directly")
	}
	cacheDir := os.Getenv("LOCKTEST_CACHE_DIR")
	project := os.Getenv("LOCKTEST_PROJECT")
	holdMS, _ := strconv.Atoi(os.Getenv("LOCKTEST_HOLD_MS"))

	locker := newProjectLocker(cacheDir, testWorkspaceRoot)
	rel, err := locker.acquire(context.Background(), project)
	if err != nil {
		t.Fatalf("helper acquire: %v", err)
	}
	defer rel()

	// Beside the lock file the locker actually took, so the layout lives in ONE place.
	if err := os.WriteFile(filepath.Join(filepath.Dir(locker.lockPath(project)), "ready"), []byte("1"), 0o644); err != nil {
		t.Fatalf("helper write ready: %v", err)
	}
	time.Sleep(time.Duration(holdMS) * time.Millisecond)
}

func waitForFile(t *testing.T, path string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

// TestLockOwnerNamesTheHolder pins the observability half of the lock: flock decides
// exclusion, this decides whether a blocked run can say WHO it is waiting on. A wait
// message that can only say "another magus process" is what turned a six-day-old
// orphan into an investigation.
func TestLockOwnerNamesTheHolder(t *testing.T) {
	l := newProjectLocker(t.TempDir(), testWorkspaceRoot)

	release, err := l.acquire(context.Background(), "web/api")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}

	desc := l.describeOwner("web/api")
	if want := "pid " + strconv.Itoa(os.Getpid()); !strings.Contains(desc, want) {
		t.Errorf("describeOwner = %q, want it to carry %q; the pid is the actionable part", desc, want)
	}
	if !strings.Contains(desc, "in ") {
		t.Errorf("describeOwner = %q, want the holder's directory; that identifies an orphan in a deleted worktree", desc)
	}

	release()
	if got := l.describeOwner("web/api"); got != "" {
		t.Errorf("describeOwner after release = %q, want empty; a released lock must not name a finished process", got)
	}
}

// ancestryCtx builds the context a magus invocation runs under: the ancestry it inherited
// plus its own id last, exactly as BeginInvocation assembles it. The ids belong to this
// process, because that is the only pid a test can make the lock's owner record carry.
func ancestryCtx(t *testing.T, ids ...string) context.Context {
	t.Helper()
	ctx := context.Background()
	for _, id := range ids {
		ctx = types.AppendInvocationAncestor(ctx, os.Getpid(), id)
	}
	// The owner record's Inv comes from the journal, never from the ancestry, so a test
	// that only stamped the ancestry would leave the sidecar anonymous and prove nothing.
	return journal.WithInvocationID(ctx, ids[len(ids)-1])
}

// TestReentrantLockRefusedNotAwaited is the regression test for the nested-run deadlock:
// a target that runs magus against a project its own invocation already locked used to
// wait forever, because the holder cannot release until the waiter exits.
//
// Both acquires happen in ONE process, which is not a shortcut: it is the daemon shape
// exactly. flock is per open file description, so a second handle in the same process
// contends like any other, and under a daemon the holder and the waiter really are one
// process. The ctx timeout is the regression guard: without the refusal this test hangs
// until the deadline instead of failing on the first assertion.
func TestReentrantLockRefusedNotAwaited(t *testing.T) {
	l := newProjectLocker(t.TempDir(), testWorkspaceRoot)

	outer := ancestryCtx(t, "inv-outer")
	release, err := l.acquire(outer, "app")
	if err != nil {
		t.Fatalf("outer acquire: %v", err)
	}
	defer release()

	nested, cancel := context.WithTimeout(ancestryCtx(t, "inv-outer", "inv-nested"), 10*time.Second)
	defer cancel()

	start := time.Now()
	_, err = l.acquire(nested, "app")
	if waited := time.Since(start); waited > 2*time.Second {
		t.Fatalf("nested acquire took %v; a lock its own ancestor holds must be refused, not awaited", waited)
	}
	if !errors.Is(err, types.ProjectLockHeldByAncestor) {
		t.Fatalf("nested acquire error = %v, want MGS3007", err)
	}
	// The holder is what makes the message actionable: it is the run the author has to
	// change, and "another magus process" was never enough to find it.
	if msg := err.Error(); !strings.Contains(msg, "pid "+strconv.Itoa(os.Getpid())) {
		t.Errorf("error = %q, want it to name the holding pid", msg)
	}
	if msg := err.Error(); !strings.Contains(msg, "ctx.needs") {
		t.Errorf("error = %q, want it to name the way out", msg)
	}
}

// TestUnrelatedContentionFailsFast is the other half, and the one that keeps the
// reentrant fix honest: contention with a run this one is NOT nested inside is
// ordinary contention, refused exactly like any other holder (*lockContendedError,
// exit 75), never as MGS3007 (that diagnosis is reserved for a run's own ancestor).
func TestUnrelatedContentionFailsFast(t *testing.T) {
	cacheDir := t.TempDir()
	lockDir := filepath.Join(cacheDir, "locks", workspaceLockKey(testWorkspaceRoot))

	// A separate process, so the holder's sidecar carries an invocation id this one has
	// never heard of: the shape of two developers, or two agents, in one workspace.
	cmd := helperHold(t, cacheDir, "app", 5_000)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start holder: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	waitForFile(t, filepath.Join(lockDir, "app", "ready"), 3*time.Second)

	l := newProjectLocker(cacheDir, testWorkspaceRoot)
	ctx := ancestryCtx(t, "inv-unrelated")
	_, err := l.acquire(ctx, "app")
	var c *lockContendedError
	if !errors.As(err, &c) {
		t.Fatalf("want *lockContendedError for unrelated contention, got %v", err)
	}
	if errors.Is(err, types.ProjectLockHeldByAncestor) {
		t.Error("unrelated contention must not be diagnosed as MGS3007; that refusal is reserved for a run's own ancestor")
	}
}

// TestLockOwnerRecordsInvocation pins the sidecar field the refusal reads. Without it the
// holder is anonymous to its own descendants and every nested run waits forever again.
func TestLockOwnerRecordsInvocation(t *testing.T) {
	l := newProjectLocker(t.TempDir(), testWorkspaceRoot)

	ctx := ancestryCtx(t, "inv-outer", "inv-mine")
	release, err := l.acquire(ctx, "app")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer release()

	// This invocation's own id, not an ancestor's: recording an ancestor would point a
	// descendant's check at a run that holds nothing.
	if got := l.readOwner("app").Inv; got != "inv-mine" {
		t.Errorf("owner Inv = %q, want %q", got, "inv-mine")
	}
}

// TestHeldLocksReportsHolders pins the status-surface half: a held lock is normal, so
// this reports it as state, and the value is naming who holds what.
func TestHeldLocksReportsHolders(t *testing.T) {
	cache := t.TempDir()
	l := newProjectLocker(cache, testWorkspaceRoot)

	if got := heldLocks(cache, testWorkspaceRoot); len(got) != 0 {
		t.Fatalf("HeldLocks on a fresh cache = %v, want none", got)
	}

	relRoot, err := l.acquire(context.Background(), ".")
	if err != nil {
		t.Fatalf("acquire root: %v", err)
	}
	relWeb, err := l.acquire(context.Background(), "web/api")
	if err != nil {
		t.Fatalf("acquire web/api: %v", err)
	}

	held := heldLocks(cache, testWorkspaceRoot)
	if len(held) != 2 {
		t.Fatalf("HeldLocks = %d entries, want 2: %+v", len(held), held)
	}
	// Sorted by project, so the root sorts first.
	if held[0].Project != "." || held[1].Project != "web/api" {
		t.Errorf("projects = %q, %q; want \".\", \"web/api\" in sorted order", held[0].Project, held[1].Project)
	}
	for _, h := range held {
		if h.PID != os.Getpid() {
			t.Errorf("project %s: pid = %d, want %d", h.Project, h.PID, os.Getpid())
		}
		if h.AcquireTime.IsZero() {
			t.Errorf("project %s: Since is zero; age is the signal that separates a peer from an abandoned holder", h.Project)
		}
		if h.Dir == "" {
			t.Errorf("project %s: Dir is empty; it is what identifies a holder in a deleted worktree", h.Project)
		}
	}

	relWeb()
	if held := heldLocks(cache, testWorkspaceRoot); len(held) != 1 || held[0].Project != "." {
		t.Errorf("after releasing web/api, HeldLocks = %+v; want only the root", held)
	}
	relRoot()
	if held := heldLocks(cache, testWorkspaceRoot); len(held) != 0 {
		t.Errorf("after releasing everything, HeldLocks = %+v; want none", held)
	}
}

// TestLockWaitersAreRecorded pins the second half of the picture. A holder answers
// "who is working"; a waiter answers "who is stalled because of it", which is the
// question anyone staring at a queue that will not move is actually asking.
func TestLockWaitersAreRecorded(t *testing.T) {
	cache := t.TempDir()
	l := newProjectLocker(cache, testWorkspaceRoot)

	release, err := l.acquire(context.Background(), "web/api")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer release()

	if held := heldLocks(cache, testWorkspaceRoot); len(held) != 1 || len(held[0].Waiters) != 0 {
		t.Fatalf("HeldLocks with no contention = %+v; want one holder and no waiters", held)
	}

	// A waiter marker is written while blocked and cleared when the wait ends, so
	// record/clear is exercised directly rather than racing a second process.
	stop := l.recordWaiter(context.Background(), "web/api")
	held := heldLocks(cache, testWorkspaceRoot)
	if len(held) != 1 {
		t.Fatalf("HeldLocks = %d entries, want 1", len(held))
	}
	if len(held[0].Waiters) != 1 {
		t.Fatalf("waiters = %+v, want exactly one", held[0].Waiters)
	}
	if held[0].Waiters[0].PID != os.Getpid() {
		t.Errorf("waiter pid = %d, want %d", held[0].Waiters[0].PID, os.Getpid())
	}
	if held[0].Waiters[0].WaitTime.IsZero() {
		t.Error("waiter WaitTime is zero; how long a run has been stalled is the point")
	}

	stop()
	if held := heldLocks(cache, testWorkspaceRoot); len(held) != 1 || len(held[0].Waiters) != 0 {
		t.Errorf("after the wait ended, waiters = %+v; want none", held[0].Waiters)
	}
}

// TestWatchWorkspaceRootReleasesOnVanish pins the orphan case. A process whose
// checkout is deleted keeps running and, because a flock lives exactly as long as its
// holder, keeps every lock it took. Peers then wait forever on a holder that is never
// coming back.
func TestWatchWorkspaceRootReleasesOnVanish(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tree")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	released := make(chan struct{})
	var once sync.Once
	stop := watchWorkspaceRoot(context.Background(), root, 5*time.Millisecond, func() { once.Do(func() { close(released) }) })
	defer stop()

	// While the tree exists, the watchdog must keep its hands off.
	select {
	case <-released:
		t.Fatal("released while the workspace root still existed")
	case <-time.After(30 * time.Millisecond):
	}

	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	select {
	case <-released:
	case <-time.After(2 * time.Second):
		t.Fatal("workspace root vanished but the locks were never released; this is the orphan that blocks every later run")
	}
}

// TestWatchWorkspaceRootStops pins that stopping is what a normal run does, and that
// it does not release afterwards.
func TestWatchWorkspaceRootStops(t *testing.T) {
	root := t.TempDir()
	var released atomic.Bool
	stop := watchWorkspaceRoot(context.Background(), root, 5*time.Millisecond, func() { released.Store(true) })
	stop()
	stop() // idempotent: a release path may stop twice

	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	if released.Load() {
		t.Error("released after stop; a finished run must not have its locks touched")
	}
}

// TestWatchWorkspaceRootIgnoresTransientStatErrors pins that only a genuine absence
// counts. Treating any stat error as "gone" withdraws a live run's exclusivity on a
// permissions blip or a network-mount hiccup, which is the opposite of the lock's job.
func TestWatchWorkspaceRootIgnoresTransientStatErrors(t *testing.T) {
	// A path whose PARENT is not a directory makes Stat fail with ENOTDIR rather
	// than ENOENT, standing in for the transient-error class.
	base := t.TempDir()
	notADir := filepath.Join(base, "file")
	if err := os.WriteFile(notADir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(notADir, "under")

	var released atomic.Bool
	stop := watchWorkspaceRoot(context.Background(), root, 5*time.Millisecond, func() { released.Store(true) })
	defer stop()

	time.Sleep(60 * time.Millisecond)
	if released.Load() {
		t.Error("released on a non-ENOENT stat error; a transient failure must not withdraw a live run's locks")
	}
}

// TestWatchWorkspaceRootStopJoins pins that stop() waits for the goroutine. Closing
// the signal alone leaves a goroutine that can still reach release() afterwards, and
// in the daemon that late release lands on whatever the NEXT run holds.
func TestWatchWorkspaceRootStopJoins(t *testing.T) {
	root := t.TempDir()
	var released atomic.Bool
	stop := watchWorkspaceRoot(context.Background(), root, time.Millisecond, func() { released.Store(true) })

	stop()
	// Once stop() returns the goroutine is done, so deleting the root now can never
	// trigger a release.
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	if released.Load() {
		t.Error("released after stop() returned; stop must join, not merely signal")
	}
}

// captureLockOut is the buffer a test's lockers write their decision lines into, and the
// option that points them at it. These lines are the whole user-visible half of a
// supersede, so a test that cannot read the exact bytes proves nothing about what a
// person sees.
func captureLockOut() (*bytes.Buffer, lockerOption) {
	var b bytes.Buffer
	return &b, writingTo(&b)
}

// quickSupersede shortens the two timings a supersede is paced by, so a test that only
// exists to observe the protocol finishes in milliseconds.
func quickSupersede(t *testing.T, bound time.Duration) {
	t.Helper()
	poll, prevBound := supersedePollInterval, supersedeYieldBound
	supersedePollInterval, supersedeYieldBound = 5*time.Millisecond, bound
	t.Cleanup(func() { supersedePollInterval, supersedeYieldBound = poll, prevBound })
}

// TestLockRecordRoundTripsTheSupersedeFields pins the two fields the qualifier reads. A
// sidecar that loses either one silently disqualifies its holder, which reads as
// supersession quietly not working rather than as a failure.
func TestLockRecordRoundTripsTheSupersedeFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock.owner")
	want := processRecord{
		PID:     41221,
		Command: "magus affected ci --no-default-charms",
		Dir:     "/ws",
		Started: time.Now(),
		Inv:     "inv-0123456789abcdef",
		Root:    "/ws",
		Gate:    true,
	}
	if err := record.Write(path, want); err != nil {
		t.Fatalf("write: %v", err)
	}
	var got processRecord
	if err := record.Read(path, &got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !got.Started.Equal(want.Started.Truncate(time.Second)) {
		t.Errorf("Started = %v, want %v; RFC3339 keeps seconds and the comparison depends on it",
			got.Started, want.Started.Truncate(time.Second))
	}
	// Compared whole, so a field added later without a tag fails here rather than going
	// silently unpersisted. Started is checked above; RFC3339 drops its location.
	got.Started, want.Started = time.Time{}, time.Time{}
	if got != want {
		t.Errorf("record round trip = %+v, want %+v", got, want)
	}
}

// TestLockRecordReadsBothDirectionsAcrossVersions is the compat half. A magus that
// predates supersession and one that does not share a lock directory whenever two
// checkouts share a cache, and neither may choke on the other's sidecar.
func TestLockRecordReadsBothDirectionsAcrossVersions(t *testing.T) {
	dir := t.TempDir()

	// An older magus wrote this: no root line, no gate line. It must decode, and decode as
	// NOT a gate, so the holder keeps today's behavior and is waited on rather than killed.
	old := filepath.Join(dir, "old.owner")
	if err := os.WriteFile(old, []byte("command\tmagus run ci .\ndir\t/ws\npid\t4821\n"), 0o644); err != nil {
		t.Fatalf("write old record: %v", err)
	}
	var got processRecord
	if err := record.Read(old, &got); err != nil {
		t.Fatalf("read old record: %v", err)
	}
	if got.PID != 4821 || got.Root != "" || got.Gate {
		t.Errorf("old record decoded to %+v, want the known fields and zeroed supersede fields", got)
	}

	// And the other direction: an older reader knows only its own field names, so the two
	// lines it has never heard of are lines it ignores rather than lines it rejects.
	type legacyRecord struct {
		PID     int    `record:"pid"`
		Command string `record:"command"`
		Dir     string `record:"dir"`
	}
	fresh := filepath.Join(dir, "new.owner")
	if err := record.Write(fresh, processRecord{PID: 41221, Command: "magus affected ci .", Dir: "/ws", Root: "/ws", Gate: true}); err != nil {
		t.Fatalf("write new record: %v", err)
	}
	var legacy legacyRecord
	if err := record.Read(fresh, &legacy); err != nil {
		t.Fatalf("old reader on a new record: %v", err)
	}
	if legacy.PID != 41221 || legacy.Command != "magus affected ci ." {
		t.Errorf("old reader decoded %+v, want the fields it knows intact", legacy)
	}
}

// TestSupersedeQualifier walks every way a contention is NOT a supersede. The positive
// case is one line; the negatives are the test, because each one is a run that would be
// killed for no reason if the qualifier widened.
func TestSupersedeQualifier(t *testing.T) {
	now := time.Now()
	holder := func(mut func(*processRecord)) processRecord {
		r := processRecord{
			PID:     4821,
			Command: "magus affected ci .",
			Dir:     testWorkspaceRoot,
			Started: now.Add(-time.Minute),
			Root:    testWorkspaceRoot,
			Gate:    true,
		}
		if mut != nil {
			mut(&r)
		}
		return r
	}

	cases := []struct {
		name       string
		waiterGate bool
		owner      processRecord
		want       bool
	}{
		{"a later gate on the same tree", true, holder(nil), true},
		{"a sibling worktree is a different tree", true, holder(func(r *processRecord) { r.Root = "/ws-other" }), false},
		{"the holder is not a gate", true, holder(func(r *processRecord) { r.Gate = false }), false},
		{"the waiter is not a gate", false, holder(nil), false},
		{"the holder started later", true, holder(func(r *processRecord) { r.Started = now.Add(time.Minute) }), false},
		{"the holder has no start time on record", true, holder(func(r *processRecord) { r.Started = time.Time{} }), false},
		{"a sidecar from a magus that predates supersession", true, processRecord{PID: 4821, Command: "magus run ci .", Started: now.Add(-time.Minute)}, false},
		{"no holder on record", true, processRecord{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var opts []lockerOption
			if tc.waiterGate {
				opts = append(opts, asGate())
			}
			l := newProjectLocker(t.TempDir(), testWorkspaceRoot, opts...)
			l.started = now
			if tc.owner.PID != 0 {
				// The acquire path creates the lock directory; this writes the sidecar
				// without acquiring, standing in for the earlier gate that did.
				if err := os.MkdirAll(filepath.Dir(l.ownerPath("app")), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := record.Write(l.ownerPath("app"), tc.owner); err != nil {
					t.Fatalf("write owner: %v", err)
				}
			}
			if _, got := l.supersedes("app"); got != tc.want {
				t.Errorf("supersedes = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestALaterGateTakesTheLockAndTheEarlierOneReportsMGS3014 is the whole protocol end to
// end: the request, the abort, the handover and both messages.
//
// Nothing is running in the earlier gate, which is not a simplification. It is the settle
// tail exactly: a run that has finished its batch still holds every lock, and is
// superseded like any other gate.
//
// Both lockers live in one process, which is the daemon shape rather than a shortcut:
// flock is per open file description, so a second handle contends like any other.
func TestALaterGateTakesTheLockAndTheEarlierOneReportsMGS3014(t *testing.T) {
	quickSupersede(t, 5*time.Second)
	out, toOut := captureLockOut()
	cacheDir := t.TempDir()

	earlier := newProjectLocker(cacheDir, testWorkspaceRoot, asGate(), toOut)
	earlier.started = time.Now().Add(-2 * time.Minute)
	rel, err := earlier.acquire(t.Context(), "app")
	if err != nil {
		t.Fatalf("earlier acquire: %v", err)
	}
	hold := &projectHold{unlock: rel, locker: earlier, paths: []string{"app"}}
	defer hold.release()

	ctx, watch := (&Magus{}).watchForSupersede(t.Context(), hold)
	defer watch.close()
	// The run's own unwind is what frees the locks, on its deferred release; the watch
	// only cancels. Stand in for that unwind here.
	go func() {
		<-ctx.Done()
		hold.release()
	}()

	later := newProjectLocker(cacheDir, testWorkspaceRoot, asGate(), toOut)
	start := time.Now()
	rel2, err := later.acquire(t.Context(), "app")
	if err != nil {
		t.Fatalf("later acquire: %v", err)
	}
	defer rel2()
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("the later gate waited %v; a supersede hands the lock over in seconds or it is just a wait", took)
	}

	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the earlier gate was never cancelled; a yield that does not abort leaves two gates on one tree")
	}

	verdict := watch.verdict(nil)
	if !errors.Is(verdict, types.GateSuperseded) {
		t.Fatalf("verdict = %v, want MGS3014", verdict)
	}
	var stated interface{ ExitCode() int }
	if !errors.As(verdict, &stated) || stated.ExitCode() != 75 {
		t.Errorf("verdict must state exit 75, so a caller can tell a yielded gate from a failed one: %v", verdict)
	}
	msg := verdict.Error()
	for _, want := range []string{
		"superseded by a later gate on the same tree",
		later.started.UTC().Format(time.RFC3339),
		"nothing here was wrong",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("MGS3014 = %q, want it to carry %q", msg, want)
		}
	}

	// The successor says what it did, on the stream nothing bounds: -s is a console mode,
	// and this line never goes through the console.
	if line := out.String(); !strings.Contains(line, "superseded the earlier gate on project app") {
		t.Errorf("lock output = %q, want the one line naming what was superseded", line)
	}
	if n := strings.Count(out.String(), "superseded the earlier gate"); n != 1 {
		t.Errorf("supersede lines = %d, want exactly 1", n)
	}
	if _, err := os.Stat(later.yieldPath("app")); !os.IsNotExist(err) {
		t.Error("the request must be retracted once the lock changes hands")
	}
}

// TestAnAncestorHolderIsRefusedNotSuperseded keeps MGS3007 the more specific answer. A
// nested gate that superseded its own parent would kill the run that is blocked waiting
// for it to exit, which turns a diagnosable deadlock into a dead outer run.
func TestAnAncestorHolderIsRefusedNotSuperseded(t *testing.T) {
	quickSupersede(t, 200*time.Millisecond)
	cacheDir := t.TempDir()

	outer := newProjectLocker(cacheDir, testWorkspaceRoot, asGate())
	outer.started = time.Now().Add(-time.Minute)
	rel, err := outer.acquire(ancestryCtx(t, "inv-outer"), "app")
	if err != nil {
		t.Fatalf("outer acquire: %v", err)
	}
	defer rel()

	nested := newProjectLocker(cacheDir, testWorkspaceRoot, asGate())
	ctx, cancel := context.WithTimeout(ancestryCtx(t, "inv-outer", "inv-nested"), 5*time.Second)
	defer cancel()
	if _, err := nested.acquire(ctx, "app"); !errors.Is(err, types.ProjectLockHeldByAncestor) {
		t.Fatalf("nested gate acquire = %v, want MGS3007", err)
	}
	if _, err := os.Stat(nested.yieldPath("app")); !os.IsNotExist(err) {
		t.Error("a refused acquire must leave no request for the ancestor to answer")
	}
}

// TestAnUnansweredSupersedeFallsBackToRefusing covers the holder that cannot yield: too
// old to know what a request is, stopped, or wedged in a syscall. The bound is what keeps
// a supersede from becoming the hang it exists to remove, and past it magus refuses the
// acquire exactly like any other contention (exit 75) rather than falling back to a wait.
func TestAnUnansweredSupersedeFallsBackToRefusing(t *testing.T) {
	quickSupersede(t, 200*time.Millisecond)
	out, toOut := captureLockOut()
	cacheDir := t.TempDir()

	// No watch is armed over this holder, so nothing ever reads the request. It releases
	// well after the supersede bound, so a test that blocked for it would prove the wait
	// this refusal is supposed to have replaced.
	earlier := newProjectLocker(cacheDir, testWorkspaceRoot, asGate(), toOut)
	earlier.started = time.Now().Add(-time.Minute)
	rel, err := earlier.acquire(t.Context(), "app")
	if err != nil {
		t.Fatalf("earlier acquire: %v", err)
	}
	defer rel()

	later := newProjectLocker(cacheDir, testWorkspaceRoot, asGate(), toOut)
	start := time.Now()
	_, err = later.acquire(t.Context(), "app")
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("acquire took %v after an unanswered supersede; want a fail-fast refusal, not a wait", elapsed)
	}
	var c *lockContendedError
	if !errors.As(err, &c) {
		t.Fatalf("want *lockContendedError once the supersede bound expires, got %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "did not stop within") {
		t.Errorf("lock output = %q, want the fallback to say the supersede went unanswered", got)
	}
	if strings.Contains(got, "superseded the earlier gate") {
		t.Errorf("lock output = %q, must not claim a supersede that never happened", got)
	}
	if _, err := os.Stat(later.yieldPath("app")); !os.IsNotExist(err) {
		t.Error("a supersede that gave up must retract its request")
	}
}
