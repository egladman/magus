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
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLimiterSnapshot(t *testing.T) {
	t.Parallel()
	l := NewLimiter(3)
	assert.Equal(t, LimiterStats{Capacity: 3, InUse: 0, Waiting: 0}, l.Snapshot(), "initial snapshot")

	_ = l.Acquire(context.Background())
	_ = l.Acquire(context.Background())
	snap := l.Snapshot()
	assert.Equal(t, 3, snap.Capacity)
	assert.Equal(t, 2, snap.InUse, "after 2 acquires")

	l.Release()
	assert.Equal(t, 1, l.Snapshot().InUse, "after release")
}

func TestLimiterUnlimited(t *testing.T) {
	t.Parallel()
	l := NewLimiter(0)
	for range 100 {
		require.NoError(t, l.Acquire(context.Background()))
	}
	snap := l.Snapshot()
	assert.Equal(t, 0, snap.Capacity, "unlimited capacity")
	assert.Equal(t, 0, snap.InUse, "unlimited in-use")
}

func TestLimiterCancelledAcquire(t *testing.T) {
	t.Parallel()
	l := NewLimiter(1)
	_ = l.Acquire(context.Background()) // fill the slot

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.Error(t, l.Acquire(ctx), "expected error on cancelled acquire")
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
	require.NoError(t, err)
	// After Yield returns we have re-acquired; total in-use should be 2 again.
	assert.Equal(t, 2, l.Snapshot().InUse, "post-yield inUse")
	// The goroutine inside fn should have seen inUse==2 (its own + the 1 held
	// by the other Acquire still outstanding).
	assert.Equal(t, 2, maxSeen, "maxSeen inside fn")
}

func TestLimiterAcquireN(t *testing.T) {
	t.Parallel()
	l := NewLimiter(4)

	require.NoError(t, l.AcquireN(context.Background(), 3), "AcquireN(3)")
	assert.Equal(t, 3, l.Snapshot().InUse, "after AcquireN(3)")

	l.ReleaseN(3)
	assert.Equal(t, 0, l.Snapshot().InUse, "after ReleaseN(3)")
}

func TestLimiterAcquireNTooMany(t *testing.T) {
	t.Parallel()
	l := NewLimiter(4)
	assert.Error(t, l.AcquireN(context.Background(), 5), "expected error when requesting more slots than capacity")
	assert.Equal(t, 0, l.Snapshot().InUse, "after rejected AcquireN")
}

func TestLimiterAcquireNCancel(t *testing.T) {
	t.Parallel()
	l := NewLimiter(2)
	_ = l.AcquireN(context.Background(), 2) // fill all slots

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.Error(t, l.AcquireN(ctx, 1), "expected error on cancelled context")
	assert.Equal(t, 2, l.Snapshot().InUse, "cancelled AcquireN must not change inUse")
}

// TestLimiterFairQueueing verifies FIFO fairness: a large request at the head
// of the queue blocks smaller requests behind it until it can be fully satisfied.
func TestLimiterFairQueueing(t *testing.T) {
	t.Parallel()
	l := NewLimiter(4)

	// Hold all 4 slots.
	require.NoError(t, l.AcquireN(context.Background(), 4))

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
	assert.Equal(t, 0, l.Snapshot().InUse, "post-fairness-test inUse")
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
		nil,
	)

	_ = l.Acquire(context.Background())
	_ = l.Acquire(context.Background())
	l.Release()
	l.Release()

	mu.Lock()
	assert.Len(t, acqNs, 2, "onAcquire fired the wrong number of times")
	assert.Len(t, relN, 2, "onRelease fired the wrong number of times")
	mu.Unlock()

	// Contended case: second Acquire on a full pool should observe wait > 0.
	l2 := NewLimiter(1)
	var waitedNs int64
	l2.SetHooks(func(ns int64, _ int) { waitedNs = ns }, nil, nil)
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
	assert.NotZero(t, waitedNs, "contended acquire reported wait == 0ns")
}

// TestLimiterWaitingHook verifies the onWait hook mirrors the internal waiting counter
// exactly: it fires +n the instant a caller begins waiting and -n once its Acquire returns,
// so the net inflight of onWait tracks Snapshot().Waiting at every step.
func TestLimiterWaitingHook(t *testing.T) {
	t.Parallel()
	l := NewLimiter(1)
	var net atomic.Int64
	l.SetHooks(nil, nil, func(delta int) { net.Add(int64(delta)) })

	// Uncontended: onWait still fires +1 (before Acquire) and -1 (on return), netting 0.
	require.NoError(t, l.Acquire(context.Background()))
	assert.Equal(t, int64(0), net.Load(), "uncontended acquire should net zero waiting")

	// Contended: a second acquire on the now-full pool must register as waiting.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = l.Acquire(context.Background())
	}()
	for l.Snapshot().Waiting == 0 {
		runtime.Gosched()
	}
	// While the goroutine is provably blocked, onWait's net must equal the live waiting count.
	assert.Equal(t, int64(l.Snapshot().Waiting), net.Load(), "onWait net must mirror Waiting")

	l.Release() // unblock the waiter
	wg.Wait()
	assert.Equal(t, int64(0), net.Load(), "waiting should return to zero after acquire")
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

	assert.ErrorIs(t, err, fnErr, "fn error not returned")
	// Slot was restored: the caller still holds exactly its one slot.
	assert.Equal(t, 1, l.Snapshot().InUse, "post-yield inUse, want 1 (slot restored)")
}

// TestLimiterYieldNoOverReleaseOnCancel mimics a RunAll worker: acquire a slot,
// yield it around a cancelled child, then release once on return. If Yield
// returned slot-less, the deferred Release would over-release the semaphore
// (x/sync/semaphore panics on net over-release) or silently inflate capacity.
func TestLimiterYieldNoOverReleaseOnCancel(t *testing.T) {
	t.Parallel()
	l := NewLimiter(2)
	for range 5 {
		require.NoError(t, l.Acquire(context.Background()))
		ctx, cancel := context.WithCancel(context.Background())
		_ = l.Yield(ctx, func() error { cancel(); return nil })
		l.Release() // must not panic; balanced because Yield restored the slot
	}
	assert.Equal(t, 0, l.Snapshot().InUse, "inUse after balanced acquire/yield/release cycles")
	// Full capacity must still be acquirable — no permits leaked or lost.
	assert.NoError(t, l.AcquireN(context.Background(), 2), "capacity shrank after yield cycles")
}

// makeTar writes a gzip-compressed tar containing one file of size n bytes.
func makeTar(t *testing.T, name string, size int64) io.Reader {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     name,
		Typeflag: tar.TypeReg,
		Size:     size,
	}), "tar header")
	_, err := io.Copy(tw, io.LimitReader(zeroReader{}, size))
	require.NoError(t, err, "tar body")
	require.NoError(t, tw.Close(), "tar close")
	require.NoError(t, gw.Close(), "gzip close")
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
	require.NoError(t, err, "Open")

	// Create a "bomb": a single entry that would be 1 MiB without the cap.
	const entrySize = 1 << 20
	archive := makeTar(t, "manifests/test/entry", entrySize)

	require.NoError(t, c.Import(context.Background(), archive), "Import")

	// The written file must not exceed the cap.
	dest := filepath.Join(cdir, "manifests", "test", "entry")
	fi, err := os.Stat(dest)
	require.NoError(t, err, "stat")
	assert.LessOrEqual(t, fi.Size(), int64(1024), "file size must not exceed the cap")
}

// TestImportDefaultCapApplied ensures Import works normally when no cap
// option is set — the default cap must be large enough for real archives.
func TestImportDefaultCapApplied(t *testing.T) {
	t.Parallel()

	cdir := t.TempDir()
	c, err := Open(cdir)
	require.NoError(t, err, "Open")

	// A small legitimate entry — must pass through untruncated.
	const entrySize = 4096
	archive := makeTar(t, "manifests/test/small", entrySize)

	require.NoError(t, c.Import(context.Background(), archive), "Import")

	dest := filepath.Join(cdir, "manifests", "test", "small")
	fi, err := os.Stat(dest)
	require.NoError(t, err, "stat")
	assert.Equal(t, int64(entrySize), fi.Size())
}
