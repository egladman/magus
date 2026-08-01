package magus

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestSameProjectExclusiveSerializes proves two exclusive holders of the SAME
// project's lock cannot overlap: the second blocks until the first releases.
// Uses two OS processes (a subprocess holds the lock) so it exercises the real
// kernel flock, not an in-process handle.
func TestSameProjectExclusiveSerializes(t *testing.T) {
	cacheDir := t.TempDir()
	lockDir := filepath.Join(cacheDir, "locks")

	// Subprocess acquires the "app" lock and holds it for 600ms.
	holdMS := 600
	cmd := helperHold(t, lockDir, "app", holdMS)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start holder: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	// Wait until the subprocess has actually taken the lock (it signals by
	// creating a ready file), so we know our acquire genuinely contends.
	waitForFile(t, filepath.Join(lockDir, "app", "ready"), 3*time.Second)

	locker := newProjectLocker(cacheDir, false)
	start := time.Now()
	release, err := locker.acquire(context.Background(), "app")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer release()
	waited := time.Since(start)

	// We should have blocked roughly until the holder released (it had ~600ms
	// left minus the ready-signal slack). Require a clear, non-trivial wait.
	if waited < 200*time.Millisecond {
		t.Fatalf("expected to block on the held lock, but acquired after %v", waited)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("holder subprocess failed: %v", err)
	}
}

// TestDifferentProjectsNoContention proves two DIFFERENT projects' exclusive
// locks are held concurrently, with no false contention.
func TestDifferentProjectsNoContention(t *testing.T) {
	locker := newProjectLocker(t.TempDir(), false)

	relA, err := locker.acquire(context.Background(), "libs/diagnostics")
	if err != nil {
		t.Fatalf("acquire A: %v", err)
	}
	defer relA()

	// A different project must not block even with a short-deadline context: if it
	// contended, TryLock would fail and the deadline would elapse.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	relB, err := locker.acquire(ctx, "libs/textsearch")
	if err != nil {
		t.Fatalf("acquire B (different project) should not contend: %v", err)
	}
	defer relB()
}

// TestReleaseFrees proves a released lock can be re-taken immediately.
func TestReleaseFrees(t *testing.T) {
	locker := newProjectLocker(t.TempDir(), false)

	rel, err := locker.acquire(context.Background(), "docs")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	rel()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	rel2, err := locker.acquire(ctx, "docs")
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
	lockDir := filepath.Join(cacheDir, "locks")

	// Hold effectively forever; we kill it.
	cmd := helperHold(t, lockDir, "svc", 60_000)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start holder: %v", err)
	}
	waitForFile(t, filepath.Join(lockDir, "svc", "ready"), 3*time.Second)

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill holder: %v", err)
	}
	_, _ = cmd.Process.Wait()

	// The kernel must have dropped the lock on process death, so we acquire it
	// under a bounded context rather than blocking forever.
	locker := newProjectLocker(cacheDir, false)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rel, err := locker.acquire(ctx, "svc")
	if err != nil {
		t.Fatalf("acquire after holder death (lock should be released): %v", err)
	}
	rel()
}

// TestNoWaitFailsFast proves the no-wait path returns *lockContendedError immediately
// instead of blocking when another handle holds the lock.
func TestNoWaitFailsFast(t *testing.T) {
	cacheDir := t.TempDir()
	lockDir := filepath.Join(cacheDir, "locks")

	cmd := helperHold(t, lockDir, "p", 5_000)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start holder: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	waitForFile(t, filepath.Join(lockDir, "p", "ready"), 3*time.Second)

	locker := newProjectLocker(cacheDir, true) // noWait
	start := time.Now()
	_, err := locker.acquire(context.Background(), "p")
	if time.Since(start) > time.Second {
		t.Fatalf("no-wait acquire blocked instead of failing fast")
	}
	var c *lockContendedError
	if !errors.As(err, &c) {
		t.Fatalf("want *lockContendedError error, got %v", err)
	}
	if c.Project != "p" {
		t.Fatalf("Contended.Project = %q, want %q", c.Project, "p")
	}
}

// TestWaitingMessageEmittedOnce proves the waiting hook fires exactly once when
// an acquire has to block on a held lock.
func TestWaitingMessageEmittedOnce(t *testing.T) {
	cacheDir := t.TempDir()
	lockDir := filepath.Join(cacheDir, "locks")

	cmd := helperHold(t, lockDir, "w", 400)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start holder: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	waitForFile(t, filepath.Join(lockDir, "w", "ready"), 3*time.Second)

	var notes int32
	locker := newProjectLocker(cacheDir, false, withLockNotify(func(string) { atomic.AddInt32(&notes, 1) }))
	rel, err := locker.acquire(context.Background(), "w")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer rel()
	if got := atomic.LoadInt32(&notes); got != 1 {
		t.Fatalf("waiting message emitted %d times, want 1", got)
	}
	_ = cmd.Wait()
}

// TestWaitHeartbeatDoesNotBreakContendedAcquire covers the heartbeat added so a blocked
// run stops looking hung. The interval is shortened well below the hold time so the
// heartbeat goroutine definitely ticks during the wait, then the acquire must still
// succeed and its stop func must return rather than deadlock. Run under -race this also
// proves the goroutine neither races the acquire nor outlives it.
//
// It deliberately installs NO notify hook, because the hook short-circuits the heartbeat;
// this is the real stderr path a user gets.
func TestWaitHeartbeatDoesNotBreakContendedAcquire(t *testing.T) {
	prev := lockWaitHeartbeat
	lockWaitHeartbeat = 10 * time.Millisecond
	t.Cleanup(func() { lockWaitHeartbeat = prev })

	cacheDir := t.TempDir()
	lockDir := filepath.Join(cacheDir, "locks")

	cmd := helperHold(t, lockDir, "hb", 300)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start holder: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	waitForFile(t, filepath.Join(lockDir, "hb", "ready"), 3*time.Second)

	locker := newProjectLocker(cacheDir, false)
	rel, err := locker.acquire(context.Background(), "hb")
	if err != nil {
		t.Fatalf("acquire after heartbeat wait: %v", err)
	}
	rel()
	_ = cmd.Wait()
}

// TestAcquireAllSortedNoDeadlock proves multi-project acquisition in sorted order
// is deadlock-safe: two goroutines each lock the same set given in OPPOSING
// orders and both complete. Sorted acquisition means neither can hold one lock
// while waiting on another the peer holds.
func TestAcquireAllSortedNoDeadlock(t *testing.T) {
	locker := newProjectLocker(t.TempDir(), false)
	set1 := []string{"a", "b", "c"}
	set2 := []string{"c", "b", "a"}

	done := make(chan error, 2)
	run := func(paths []string) {
		for i := 0; i < 50; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			rel, err := locker.acquireAll(ctx, paths)
			if err != nil {
				cancel()
				done <- err
				return
			}
			// Tiny critical section to interleave the two goroutines.
			time.Sleep(time.Millisecond)
			rel()
			cancel()
		}
		done <- nil
	}
	go run(set1)
	go run(set2)

	deadline := time.After(30 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("AcquireAll goroutine failed: %v", err)
			}
		case <-deadline:
			t.Fatal("deadlock: AcquireAll goroutines did not finish (sorted order should prevent this)")
		}
	}
}

// TestLockPathMirrorsProjectTree proves lock files mirror the project tree and
// the root project maps to <dir>/lock.
func TestLockPathMirrorsProjectTree(t *testing.T) {
	l := newProjectLocker("/cache", false)
	cases := map[string]string{
		"":                 filepath.Join("/cache", "locks", "lock"),
		".":                filepath.Join("/cache", "locks", "lock"),
		"docs":             filepath.Join("/cache", "locks", "docs", "lock"),
		"libs/diagnostics": filepath.Join("/cache", "locks", "libs", "diagnostics", "lock"),
		"libs/textsearch":  filepath.Join("/cache", "locks", "libs", "textsearch", "lock"),
	}
	for in, want := range cases {
		if got := l.lockPath(in); got != want {
			t.Errorf("lockPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- test helpers -----------------------------------------------------------

// helperHold builds a command that runs TestHelperHold in a subprocess, which
// acquires the given project's exclusive lock, signals readiness, and holds for
// holdMS milliseconds. Running in a separate process exercises the real OS lock.
func helperHold(t *testing.T, lockDir, project string, holdMS int) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperHold")
	cmd.Env = append(os.Environ(),
		"LOCKTEST_HELPER=1",
		"LOCKTEST_DIR="+lockDir,
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
	dir := os.Getenv("LOCKTEST_DIR")
	project := os.Getenv("LOCKTEST_PROJECT")
	holdMS, _ := strconv.Atoi(os.Getenv("LOCKTEST_HOLD_MS"))

	// The Locker's dir is <cacheDir>/locks; LOCKTEST_DIR is already that lock dir, so
	// point the cacheDir one level up.
	locker := newProjectLocker(filepath.Dir(dir), false)
	rel, err := locker.acquire(context.Background(), project)
	if err != nil {
		t.Fatalf("helper acquire: %v", err)
	}
	defer rel()

	if err := os.WriteFile(filepath.Join(dir, project, "ready"), []byte("1"), 0o644); err != nil {
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
	l := newProjectLocker(t.TempDir(), false)

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

// TestOrphanHintEscalates pins that the message stops reassuring once a wait is long
// enough that "busy peer" is no longer the likely explanation.
func TestOrphanHintEscalates(t *testing.T) {
	if got := orphanHint(5*time.Second, "pid 1"); got != "" {
		t.Errorf("orphanHint(5s) = %q, want empty; a short contention must not cry wolf", got)
	}
	if got := orphanHint(LockStaleAfter-time.Second, "pid 1"); got != "" {
		t.Errorf("orphanHint(just under threshold) = %q, want empty", got)
	}

	hint := orphanHint(LockStaleAfter+time.Second, "pid 71557 (magus run serve)")
	for _, want := range []string{"may be abandoned", "pid 71557", "worktree"} {
		if !strings.Contains(hint, want) {
			t.Errorf("orphanHint past threshold = %q, want it to contain %q", hint, want)
		}
	}

	if got := orphanHint(LockStaleAfter+time.Second, ""); !strings.Contains(got, "the holder") {
		t.Errorf("orphanHint with unreadable sidecar = %q, want it to escalate without a name", got)
	}
}

// TestHeldLocksReportsHolders pins the status-surface half: a held lock is normal, so
// this reports it as state, and the value is naming who holds what.
func TestHeldLocksReportsHolders(t *testing.T) {
	cache := t.TempDir()
	l := newProjectLocker(cache, false)

	if got := heldLocks(cache); len(got) != 0 {
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

	held := heldLocks(cache)
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
	if held := heldLocks(cache); len(held) != 1 || held[0].Project != "." {
		t.Errorf("after releasing web/api, HeldLocks = %+v; want only the root", held)
	}
	relRoot()
	if held := heldLocks(cache); len(held) != 0 {
		t.Errorf("after releasing everything, HeldLocks = %+v; want none", held)
	}
}

// TestLockWaitersAreRecorded pins the second half of the picture. A holder answers
// "who is working"; a waiter answers "who is stalled because of it", which is the
// question anyone staring at a queue that will not move is actually asking.
func TestLockWaitersAreRecorded(t *testing.T) {
	cache := t.TempDir()
	l := newProjectLocker(cache, false)

	release, err := l.acquire(context.Background(), "web/api")
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer release()

	if held := heldLocks(cache); len(held) != 1 || len(held[0].Waiters) != 0 {
		t.Fatalf("HeldLocks with no contention = %+v; want one holder and no waiters", held)
	}

	// A waiter marker is written while blocked and cleared when the wait ends, so
	// record/clear is exercised directly rather than racing a second process.
	stop := l.recordWaiter("web/api")
	held := heldLocks(cache)
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
	if held := heldLocks(cache); len(held) != 1 || len(held[0].Waiters) != 0 {
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
