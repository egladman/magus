package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRunner records starts and stops without touching a real process.
type fakeRunner struct {
	mu       sync.Mutex
	started  int
	stopped  int
	startErr error
}

type fakeHandle struct{ id int }

func (f *fakeRunner) Start(_ context.Context, _ types.Service) (Handle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return nil, f.startErr
	}
	f.started++
	return fakeHandle{id: f.started}, nil
}

func (f *fakeRunner) Stop(Handle) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped++
}

func (f *fakeRunner) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started, f.stopped
}

func svc() types.Service {
	return types.Service{Command: types.Command{Bin: "docker", Args: []string{"run", "postgres:15"}}}
}

func TestAcquireSharesOneInstance(t *testing.T) {
	f := &fakeRunner{}
	r := New(f, time.Hour)
	ctx := context.Background()

	// Two dependents on the same key: started once, shared.
	h1, err := r.Acquire(ctx, "pg", svc())
	require.NoError(t, err)
	h2, err := r.Acquire(ctx, "pg", svc())
	require.NoError(t, err)
	assert.Equal(t, h1, h2)

	started, stopped := f.counts()
	assert.Equal(t, 1, started, "shared instance starts once")
	assert.Equal(t, 0, stopped)
	assert.Equal(t, 1, r.Held())
}

func TestRefCountKeepsRunningUntilLastRelease(t *testing.T) {
	f := &fakeRunner{}
	r := New(f, 0) // no keep-warm: reap at ref-count zero
	ctx := context.Background()

	_, _ = r.Acquire(ctx, "pg", svc())
	_, _ = r.Acquire(ctx, "pg", svc())

	r.Release("pg")
	_, stopped := f.counts()
	assert.Equal(t, 0, stopped, "still one dependent, stays up")

	r.Release("pg")
	_, stopped = f.counts()
	assert.Equal(t, 1, stopped, "last dependent released, reaped")
	assert.Equal(t, 0, r.Held())
}

func TestKeepWarmThenIdleReap(t *testing.T) {
	f := &fakeRunner{}
	r := New(f, 30*time.Millisecond)
	ctx := context.Background()

	_, _ = r.Acquire(ctx, "pg", svc())
	r.Release("pg")

	// Warm immediately after release.
	_, stopped := f.counts()
	assert.Equal(t, 0, stopped)
	assert.Equal(t, 1, r.Held())

	// Reaped after the idle window.
	assert.Eventually(t, func() bool {
		_, s := f.counts()
		return s == 1
	}, time.Second, 5*time.Millisecond, "idle service should be reaped")
	assert.Equal(t, 0, r.Held())
}

func TestReAcquireCancelsIdleReap(t *testing.T) {
	f := &fakeRunner{}
	r := New(f, 40*time.Millisecond)
	ctx := context.Background()

	_, _ = r.Acquire(ctx, "pg", svc())
	r.Release("pg")                       // starts idle timer
	_, err := r.Acquire(ctx, "pg", svc()) // re-acquire before it fires
	require.NoError(t, err)

	// Wait past the original idle window; it must NOT have been reaped.
	time.Sleep(80 * time.Millisecond)
	started, stopped := f.counts()
	assert.Equal(t, 1, started, "warm instance reused, not restarted")
	assert.Equal(t, 0, stopped, "re-acquire cancelled the idle reap")
	assert.Equal(t, 1, r.Held())
}

func TestPerServiceIdleOverride(t *testing.T) {
	f := &fakeRunner{}
	r := New(f, time.Hour) // long default...
	ctx := context.Background()

	s := svc()
	s.Idle = "20ms" // ...overridden short by the service
	_, _ = r.Acquire(ctx, "pg", s)
	r.Release("pg")

	assert.Eventually(t, func() bool {
		_, stopped := f.counts()
		return stopped == 1
	}, time.Second, 5*time.Millisecond, "per-service idle override should reap despite long default")
}

func TestDistinctKeysAreSeparateInstances(t *testing.T) {
	f := &fakeRunner{}
	r := New(f, time.Hour)
	ctx := context.Background()

	_, _ = r.Acquire(ctx, "pg15", svc())
	_, _ = r.Acquire(ctx, "pg16", svc())
	started, _ := f.counts()
	assert.Equal(t, 2, started)
	assert.Equal(t, 2, r.Held())
}

func TestFailedStartNotCached(t *testing.T) {
	f := &fakeRunner{startErr: errors.New("boom")}
	r := New(f, time.Hour)
	ctx := context.Background()

	_, err := r.Acquire(ctx, "pg", svc())
	require.Error(t, err)
	assert.Equal(t, 0, r.Held(), "failed start leaves no entry")

	// A retry can start fresh once the runner recovers.
	f.mu.Lock()
	f.startErr = nil
	f.mu.Unlock()
	_, err = r.Acquire(ctx, "pg", svc())
	require.NoError(t, err)
	assert.Equal(t, 1, r.Held())
}

func TestShutdownStopsAll(t *testing.T) {
	f := &fakeRunner{}
	r := New(f, time.Hour)
	ctx := context.Background()

	_, _ = r.Acquire(ctx, "a", svc())
	_, _ = r.Acquire(ctx, "b", svc())
	r.Shutdown()

	_, stopped := f.counts()
	assert.Equal(t, 2, stopped)
	assert.Equal(t, 0, r.Held())
}

// slowRunner blocks in Start until released, so a test can interleave teardown with
// an in-flight start. It closes entered on entry (the registry entry is in the map by
// then) and blocks on proceed until the test lets Start finish.
type slowRunner struct {
	mu      sync.Mutex
	started int
	stopped int
	entered chan struct{}
	proceed chan struct{}
}

func (s *slowRunner) Start(context.Context, types.Service) (Handle, error) {
	close(s.entered) // announce we are mid-start (only one start in these tests)
	<-s.proceed      // block until the test lets it finish
	s.mu.Lock()
	defer s.mu.Unlock()
	s.started++
	return fakeHandle{id: s.started}, nil
}

func (s *slowRunner) Stop(Handle) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped++
}

func (s *slowRunner) counts() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.started, s.stopped
}

// TestShutdownWaitsForInFlightStart is the regression test for the race the audit
// found: Shutdown must not skip an entry whose Start is still running, or the
// just-started process is orphaned. Shutdown has to wait for the start and then stop
// it.
func TestShutdownWaitsForInFlightStart(t *testing.T) {
	sr := &slowRunner{entered: make(chan struct{}), proceed: make(chan struct{})}
	r := New(sr, time.Hour)

	acquired := make(chan struct{})
	go func() {
		_, _ = r.Acquire(context.Background(), "pg", svc())
		close(acquired)
	}()

	<-sr.entered // the entry is now in the map and Start is blocked mid-flight

	// Shutdown while Start is blocked, then let Start complete.
	shutdownReturned := make(chan struct{})
	go func() {
		r.Shutdown() // must block on the in-flight start, not skip it
		close(shutdownReturned)
	}()
	time.Sleep(20 * time.Millisecond) // let Shutdown reach its wait
	close(sr.proceed)

	<-acquired
	select {
	case <-shutdownReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown did not return")
	}

	started, stopped := sr.counts()
	assert.Equal(t, 1, started)
	assert.Equal(t, 1, stopped, "the process started mid-shutdown must still be stopped, not orphaned")
	assert.Equal(t, 0, r.Held())
}

func TestSuperviseGating(t *testing.T) {
	f := &fakeRunner{}
	sess := NewSession(New(f, time.Hour), nil, nil) // in-process only (no daemon)
	s := svc()

	// No session in context: not handled, so the caller forks it in the foreground.
	handled, err := TrySupervise(context.Background(), "k", s)
	require.NoError(t, err)
	assert.False(t, handled)

	// Session present but supervision not active (a directly-run service): not handled.
	ctx := WithSession(context.Background(), sess)
	handled, err = TrySupervise(ctx, "k", s)
	require.NoError(t, err)
	assert.False(t, handled)
	started, _ := f.counts()
	assert.Equal(t, 0, started, "not supervised, so not started here")

	// Session + supervision active (a dependency): started and handled.
	ctx = WithSupervision(ctx)
	handled, err = TrySupervise(ctx, "k", s)
	require.NoError(t, err)
	assert.True(t, handled)
	started, _ = f.counts()
	assert.Equal(t, 1, started)
}

func TestSessionRoutesToDaemonWhenPresent(t *testing.T) {
	f := &fakeRunner{}
	var acquired, released []string
	sess := NewSession(New(f, time.Hour),
		func(_ context.Context, key string, _ types.Service) error {
			acquired = append(acquired, key)
			return nil
		},
		func(key string) { released = append(released, key) },
	)
	ctx := WithSupervision(WithSession(context.Background(), sess))

	handled, err := TrySupervise(ctx, "pg", svc())
	require.NoError(t, err)
	assert.True(t, handled)

	// Routed to the daemon closure, NOT started in-process.
	started, _ := f.counts()
	assert.Equal(t, 0, started, "daemon-hosted, not started in-process")
	assert.Equal(t, []string{"pg"}, acquired)

	// ReleaseAll releases the daemon-held key (kept warm) and shuts the local registry.
	sess.ReleaseAll()
	assert.Equal(t, []string{"pg"}, released)
}

func TestSessionFallsBackToInProcessOnDaemonFailure(t *testing.T) {
	f := &fakeRunner{}
	var released []string
	sess := NewSession(New(f, time.Hour),
		func(context.Context, string, types.Service) error { return errors.New("daemon gone") },
		func(key string) { released = append(released, key) },
	)
	ctx := WithSupervision(WithSession(context.Background(), sess))

	// Daemon acquire fails: degrade to in-process rather than aborting the run.
	handled, err := TrySupervise(ctx, "pg", svc())
	require.NoError(t, err)
	assert.True(t, handled)
	started, _ := f.counts()
	assert.Equal(t, 1, started, "daemon failed, so the service is hosted in-process")

	// The fallback is NOT recorded as a daemon key, so ReleaseAll does not release it
	// remotely; the in-process registry stops it on Shutdown instead.
	sess.ReleaseAll()
	assert.Empty(t, released, "in-process fallback is not released to the daemon")
}

func TestConcurrentAcquireStartsOnce(t *testing.T) {
	f := &fakeRunner{}
	r := New(f, time.Hour)
	ctx := context.Background()

	var wg sync.WaitGroup
	var errs atomic.Int32
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.Acquire(ctx, "pg", svc()); err != nil {
				errs.Add(1)
			}
		}()
	}
	wg.Wait()

	started, _ := f.counts()
	assert.Equal(t, 1, started, "concurrent acquires of one key start exactly one instance")
	assert.Equal(t, int32(0), errs.Load())
	assert.Equal(t, 1, r.Held())
}

// portSvc is a container service with a published port, for exercising the derived
// label/command/port fields of a Snapshot entry.
func portSvc() types.Service {
	return types.Service{Command: types.Command{Bin: "docker", Args: []string{"run", "-p", "5432:5432", "postgres:15"}}}
}

func TestSnapshotRunningEntry(t *testing.T) {
	f := &fakeRunner{}
	r := New(f, time.Hour)

	_, err := r.Acquire(context.Background(), "fingerprintabcdef", portSvc())
	require.NoError(t, err)

	snap := r.Snapshot()
	require.Len(t, snap, 1)
	got := snap[0]
	assert.False(t, got.StartedAt.IsZero(), "a started entry carries a start time")

	got.StartedAt = time.Time{} // zeroed for a whole-struct compare of the derived fields
	assert.Equal(t, ServiceStatus{
		ID:         "fingerprinta", // first 12 chars of the key
		Label:      "postgres:15",
		Command:    "docker run -p 5432:5432 postgres:15",
		Ports:      []string{"5432"},
		State:      "running",
		Dependents: 1,
	}, got)
}

func TestSnapshotStateTransitions(t *testing.T) {
	f := &fakeRunner{}
	r := New(f, time.Hour) // long keep-warm so a released entry stays "idle"
	ctx := context.Background()

	_, _ = r.Acquire(ctx, "pg", portSvc())
	_, _ = r.Acquire(ctx, "pg", portSvc()) // two dependents
	require.Equal(t, 2, r.Snapshot()[0].Dependents)
	assert.Equal(t, "running", r.Snapshot()[0].State)

	r.Release("pg")
	assert.Equal(t, "running", r.Snapshot()[0].State, "still one dependent")

	r.Release("pg") // last dependent: kept warm at zero refs
	s := r.Snapshot()
	require.Len(t, s, 1)
	assert.Equal(t, 0, s[0].Dependents)
	assert.Equal(t, "idle", s[0].State)
}

func TestSnapshotStartingEntry(t *testing.T) {
	sr := &slowRunner{entered: make(chan struct{}), proceed: make(chan struct{})}
	r := New(sr, time.Hour)

	go func() { _, _ = r.Acquire(context.Background(), "pg", portSvc()) }()
	<-sr.entered // the entry is in the map but Start has not completed

	s := r.Snapshot()
	require.Len(t, s, 1)
	assert.Equal(t, "starting", s[0].State, "before ready is closed the entry is starting")

	close(sr.proceed) // let Start finish so the goroutine does not leak
}

func TestSnapshotNonContainerLabel(t *testing.T) {
	f := &fakeRunner{}
	r := New(f, time.Hour)

	local := types.Service{Command: types.Command{Bin: "/usr/local/bin/myserver", Args: []string{"--port", "8080"}}}
	_, err := r.Acquire(context.Background(), "local", local)
	require.NoError(t, err)

	s := r.Snapshot()
	require.Len(t, s, 1)
	assert.Equal(t, "myserver", s[0].Label, "non-container label falls back to the binary basename")
	assert.Equal(t, "/usr/local/bin/myserver --port 8080", s[0].Command)
	assert.Empty(t, s[0].Ports, "a non-container command has no derived ports")
}

func TestSnapshotSortedByID(t *testing.T) {
	f := &fakeRunner{}
	r := New(f, time.Hour)
	ctx := context.Background()

	_, _ = r.Acquire(ctx, "ccc", portSvc())
	_, _ = r.Acquire(ctx, "aaa", portSvc())
	_, _ = r.Acquire(ctx, "bbb", portSvc())

	s := r.Snapshot()
	require.Len(t, s, 3)
	assert.Equal(t, []string{"aaa", "bbb", "ccc"}, []string{s[0].ID, s[1].ID, s[2].ID})
}
