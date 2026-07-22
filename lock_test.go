package magus

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

	relA, err := locker.acquire(context.Background(), "libs/diag")
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
		"":                filepath.Join("/cache", "locks", "lock"),
		".":               filepath.Join("/cache", "locks", "lock"),
		"docs":            filepath.Join("/cache", "locks", "docs", "lock"),
		"libs/diag":       filepath.Join("/cache", "locks", "libs", "diag", "lock"),
		"libs/textsearch": filepath.Join("/cache", "locks", "libs", "textsearch", "lock"),
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
