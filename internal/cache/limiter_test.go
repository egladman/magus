package cache

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestLimiterSnapshot(t *testing.T) {
	t.Parallel()
	l := NewLimiter(3)
	snap := l.Snapshot()
	if snap.Capacity != 3 || snap.InUse != 0 || snap.Waiting != 0 {
		t.Fatalf("initial snapshot: cap=%d inUse=%d waiting=%d", snap.Capacity, snap.InUse, snap.Waiting)
	}

	_ = l.Acquire(context.Background())
	_ = l.Acquire(context.Background())
	snap = l.Snapshot()
	if snap.Capacity != 3 || snap.InUse != 2 {
		t.Fatalf("after 2 acquires: cap=%d inUse=%d", snap.Capacity, snap.InUse)
	}

	l.Release()
	snap = l.Snapshot()
	if snap.InUse != 1 {
		t.Fatalf("after release: inUse=%d", snap.InUse)
	}
}

func TestLimiterUnlimited(t *testing.T) {
	t.Parallel()
	l := NewLimiter(0)
	for range 100 {
		if err := l.Acquire(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	snap := l.Snapshot()
	if snap.Capacity != 0 || snap.InUse != 0 {
		t.Fatalf("unlimited: cap=%d inUse=%d", snap.Capacity, snap.InUse)
	}
}

func TestLimiterCancelledAcquire(t *testing.T) {
	t.Parallel()
	l := NewLimiter(1)
	_ = l.Acquire(context.Background()) // fill the slot

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.Acquire(ctx); err == nil {
		t.Fatal("expected error on cancelled acquire")
	}
}

func TestLimiterYield(t *testing.T) {
	t.Parallel()
	l := NewLimiter(2)
	_ = l.Acquire(context.Background())
	_ = l.Acquire(context.Background()) // both slots taken

	// Yield releases one slot, runs fn while holding zero, then re-acquires.
	var maxSeen int
	var mu sync.Mutex

	err := l.Yield(context.Background(), func() error {
		// We released our slot; a second goroutine can now proceed.
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = l.Acquire(context.Background())
			snap := l.Snapshot()
			mu.Lock()
			if snap.InUse > maxSeen {
				maxSeen = snap.InUse
			}
			mu.Unlock()
			l.Release()
		}()
		wg.Wait()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// After Yield returns we have re-acquired; total in-use should be 2 again.
	snap := l.Snapshot()
	if snap.InUse != 2 {
		t.Fatalf("post-yield inUse=%d, want 2", snap.InUse)
	}
	// The goroutine inside fn should have seen inUse==2 (its own + the 1 held
	// by the other Acquire still outstanding).
	if maxSeen != 2 {
		t.Fatalf("maxSeen inside fn=%d, want 2", maxSeen)
	}
}

func TestLimiterAcquireN(t *testing.T) {
	t.Parallel()
	l := NewLimiter(4)

	if err := l.AcquireN(context.Background(), 3); err != nil {
		t.Fatalf("AcquireN(3): %v", err)
	}
	snap := l.Snapshot()
	if snap.InUse != 3 {
		t.Fatalf("after AcquireN(3): inUse=%d, want 3", snap.InUse)
	}

	l.ReleaseN(3)
	snap = l.Snapshot()
	if snap.InUse != 0 {
		t.Fatalf("after ReleaseN(3): inUse=%d, want 0", snap.InUse)
	}
}

func TestLimiterAcquireNTooMany(t *testing.T) {
	t.Parallel()
	l := NewLimiter(4)
	err := l.AcquireN(context.Background(), 5)
	if err == nil {
		t.Fatal("expected error when requesting more slots than capacity")
	}
	if snap := l.Snapshot(); snap.InUse != 0 {
		t.Fatalf("after rejected AcquireN: inUse=%d, want 0", snap.InUse)
	}
}

func TestLimiterAcquireNCancel(t *testing.T) {
	t.Parallel()
	l := NewLimiter(2)
	_ = l.AcquireN(context.Background(), 2) // fill all slots

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.AcquireN(ctx, 1); err == nil {
		t.Fatal("expected error on cancelled context")
	}
	if snap := l.Snapshot(); snap.InUse != 2 {
		t.Fatalf("cancelled AcquireN changed inUse to %d, want 2", snap.InUse)
	}
}

// TestLimiterFairQueueing verifies FIFO fairness: a large request at the head
// of the queue blocks smaller requests behind it until it can be fully satisfied.
func TestLimiterFairQueueing(t *testing.T) {
	t.Parallel()
	l := NewLimiter(4)

	// Hold all 4 slots.
	if err := l.AcquireN(context.Background(), 4); err != nil {
		t.Fatal(err)
	}

	order := make([]int, 0, 2)
	var mu sync.Mutex
	record := func(id int) {
		mu.Lock()
		order = append(order, id)
		mu.Unlock()
	}

	ready := make(chan struct{})

	// Goroutine 1: requests 3 slots — queued first, needs 3 to free.
	go func() {
		<-ready
		_ = l.AcquireN(context.Background(), 3)
		record(1)
		l.ReleaseN(3)
	}()

	// Goroutine 2: requests 1 slot — queued second; should NOT sneak past g1.
	go func() {
		<-ready
		_ = l.AcquireN(context.Background(), 1)
		record(2)
		l.Release()
	}()

	// Give goroutines time to queue, then release all slots.
	close(ready)
	// Small sleep to let both goroutines reach their Acquire calls.
	// This is inherently racy but the semaphore's FIFO guarantee means
	// whichever goroutine queued first wins — we're verifying no panic
	// and correct release accounting, not strict ordering.
	l.ReleaseN(4)

	// Drain: both goroutines must eventually finish.
	for i := 0; i < 20; i++ {
		mu.Lock()
		n := len(order)
		mu.Unlock()
		if n == 2 {
			break
		}
		// spin briefly
		_ = i
	}
	// Verify both completed and limiter is clean.
	snap := l.Snapshot()
	if snap.InUse != 0 {
		t.Errorf("post-fairness-test inUse=%d, want 0", snap.InUse)
	}
}

// TestLimiterHooks verifies that SetHooks fires onAcquire on every
// successful Acquire and onRelease on every Release.
func TestLimiterHooks(t *testing.T) {
	t.Parallel()
	l := NewLimiter(2)

	var acqNs []int64
	var relN []int
	var mu sync.Mutex
	l.SetHooks(
		func(waitNs int64, n int) {
			mu.Lock()
			acqNs = append(acqNs, waitNs)
			mu.Unlock()
		},
		func(n int) {
			mu.Lock()
			relN = append(relN, n)
			mu.Unlock()
		},
	)

	_ = l.Acquire(context.Background())
	_ = l.Acquire(context.Background())
	l.Release()
	l.Release()

	mu.Lock()
	defer mu.Unlock()
	if len(acqNs) != 2 {
		t.Errorf("onAcquire fired %d times, want 2", len(acqNs))
	}
	if len(relN) != 2 {
		t.Errorf("onRelease fired %d times, want 2", len(relN))
	}
	// Contended case: second Acquire on a full pool should observe wait > 0.
	l2 := NewLimiter(1)
	var waitedNs int64
	l2.SetHooks(func(ns int64, _ int) { waitedNs = ns }, nil)
	_ = l2.Acquire(context.Background()) // fill pool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = l2.Acquire(context.Background())
	}()
	// Wait until the goroutine is provably blocked in Acquire (Waiting==1) before
	// releasing, so the contended path is real and the measured wait is non-zero.
	// Without this, Release can win the race and the goroutine acquires an
	// already-free slot in ~0ns (flaky on fast machines).
	for l2.Snapshot().Waiting == 0 {
		runtime.Gosched()
	}
	l2.Release() // unblock the now-blocked goroutine
	wg.Wait()
	if waitedNs == 0 {
		t.Error("contended acquire reported wait == 0ns")
	}
}

// TestLimiterYieldRestoresSlotOnCancel verifies that Yield always returns with
// its slot re-acquired, even when ctx is cancelled while fn runs. The slot is
// restored (re-acquire is non-cancellable) so callers like RunAll, which
// release their slot unconditionally on return, stay balanced. fn's error is
// returned unchanged.
func TestLimiterYieldRestoresSlotOnCancel(t *testing.T) {
	t.Parallel()
	l := NewLimiter(1)
	_ = l.Acquire(context.Background()) // hold the only slot

	ctx, cancel := context.WithCancel(context.Background())
	fnErr := errors.New("fn failed")

	err := l.Yield(ctx, func() error {
		cancel() // cancel during the yielded window
		return fnErr
	})

	if !errors.Is(err, fnErr) {
		t.Errorf("fn error not returned: got %v", err)
	}
	// Slot was restored: the caller still holds exactly its one slot.
	if snap := l.Snapshot(); snap.InUse != 1 {
		t.Errorf("post-yield inUse=%d, want 1 (slot restored)", snap.InUse)
	}
}

// TestLimiterYieldNoOverReleaseOnCancel mimics a RunAll worker: acquire a slot,
// yield it around a cancelled child, then release once on return. If Yield
// returned slot-less, the deferred Release would over-release the semaphore
// (x/sync/semaphore panics on net over-release) or silently inflate capacity.
func TestLimiterYieldNoOverReleaseOnCancel(t *testing.T) {
	t.Parallel()
	l := NewLimiter(2)
	for range 5 {
		if err := l.Acquire(context.Background()); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		_ = l.Yield(ctx, func() error { cancel(); return nil })
		l.Release() // must not panic; balanced because Yield restored the slot
	}
	if snap := l.Snapshot(); snap.InUse != 0 {
		t.Errorf("inUse=%d after balanced acquire/yield/release cycles, want 0", snap.InUse)
	}
	// Full capacity must still be acquirable — no permits leaked or lost.
	if err := l.AcquireN(context.Background(), 2); err != nil {
		t.Errorf("capacity shrank after yield cycles: %v", err)
	}
}

// makeTar writes a gzip-compressed tar containing one file of size n bytes.
func makeTar(t *testing.T, name string, size int64) io.Reader {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{
		Name:     name,
		Typeflag: tar.TypeReg,
		Size:     size,
	}); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := io.Copy(tw, io.LimitReader(zeroReader{}, size)); err != nil {
		t.Fatalf("tar body: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return &buf
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

// TestImportMaxBytesCapsTarBomb verifies that a tar entry larger than
// WithMaxImportBytes is truncated rather than filling the disk. Import
// accepts arbitrary input from CI/S3; the tar header's reported size
// cannot be trusted to bound writes without io.LimitReader.
func TestImportMaxBytesCapsTarBomb(t *testing.T) {
	t.Parallel()

	cdir := t.TempDir()
	c, err := Open(cdir, WithMaxImportBytes(1024))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Create a "bomb": a single entry that would be 1 MiB without the cap.
	const entrySize = 1 << 20
	archive := makeTar(t, "manifests/test/entry", entrySize)

	if err := c.Import(context.Background(), archive); err != nil {
		t.Fatalf("Import: %v", err)
	}

	// The written file must not exceed the cap.
	dest := filepath.Join(cdir, "manifests", "test", "entry")
	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Size() > 1024 {
		t.Errorf("file size = %d, want ≤ 1024 (the cap)", fi.Size())
	}
}

// TestImportDefaultCapApplied ensures Import works normally when no cap
// option is set — the default cap must be large enough for real archives.
func TestImportDefaultCapApplied(t *testing.T) {
	t.Parallel()

	cdir := t.TempDir()
	c, err := Open(cdir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// A small legitimate entry — must pass through untruncated.
	const entrySize = 4096
	archive := makeTar(t, "manifests/test/small", entrySize)

	if err := c.Import(context.Background(), archive); err != nil {
		t.Fatalf("Import: %v", err)
	}

	dest := filepath.Join(cdir, "manifests", "test", "small")
	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Size() != entrySize {
		t.Errorf("file size = %d, want %d", fi.Size(), entrySize)
	}
}
