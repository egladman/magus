package proc

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egladman/magus/internal/cache"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/proc/endpoint"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newJobService(handler func(ctx context.Context, args []string) error) *service {
	return &service{
		handler:   handler,
		parentCtx: context.Background(),
		lim:       cache.NewLimiter(4),
	}
}

func TestSubmitJobRunsAsync(t *testing.T) {
	ran := make(chan []string, 1)
	s := newJobService(func(_ context.Context, args []string) error {
		ran <- args
		return nil
	})

	var reply JobReply
	require.NoError(t, s.submitJob(JobRequest{Magic: JobMagic, Args: []string{"graph", "build"}}, &reply))
	assert.NotEmpty(t, reply.Inv, "an accepted job returns an invocation id")

	select {
	case got := <-ran:
		assert.Equal(t, []string{"graph", "build"}, got, "the handler runs with the submitted args")
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not run")
	}
}

func TestSubmitJobInvokesOnJobDone(t *testing.T) {
	type call struct {
		args []string
		err  error
	}
	done := make(chan call, 1)
	s := newJobService(func(context.Context, []string) error { return errors.New("boom") })
	s.onJobDone = func(_ context.Context, args []string, dur time.Duration, err error) {
		assert.GreaterOrEqual(t, dur, time.Duration(0))
		done <- call{args, err}
	}

	var reply JobReply
	require.NoError(t, s.submitJob(JobRequest{Magic: JobMagic, Args: []string{"reindex"}}, &reply))

	select {
	case got := <-done:
		assert.Equal(t, []string{"reindex"}, got.args, "onJobDone sees the job args")
		require.Error(t, got.err, "onJobDone sees the handler's error so the trail records it as error")
	case <-time.After(2 * time.Second):
		t.Fatal("onJobDone was not called for a background job")
	}
}

func TestSubmitJobIgnoresBadMagic(t *testing.T) {
	var ran bool
	s := newJobService(func(context.Context, []string) error { ran = true; return nil })
	var reply JobReply
	require.NoError(t, s.submitJob(JobRequest{Magic: "wrong", Args: []string{"x"}}, &reply))
	assert.Empty(t, reply.Inv, "an unauthenticated submission is ignored")
	time.Sleep(50 * time.Millisecond)
	assert.False(t, ran, "the handler never runs for a bad-magic request")
}

func TestSubmitJobCoalescesDuplicates(t *testing.T) {
	var mu sync.Mutex
	starts := 0
	release := make(chan struct{})
	s := newJobService(func(context.Context, []string) error {
		mu.Lock()
		starts++
		mu.Unlock()
		<-release // hold the first job in flight
		return nil
	})

	req := JobRequest{Magic: JobMagic, Args: []string{"graph", "build"}}
	var r1, r2 JobReply
	require.NoError(t, s.submitJob(req, &r1))
	// Give the first job's goroutine a moment to register as in-flight.
	time.Sleep(50 * time.Millisecond)
	require.NoError(t, s.submitJob(req, &r2))

	assert.NotEmpty(t, r1.Inv, "the first identical job is accepted")
	assert.Empty(t, r2.Inv, "a duplicate in-flight job is coalesced, not re-run")

	close(release)
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, starts, "the handler ran once for two identical submissions")
}

type fakeServiceHost struct {
	mu         sync.Mutex
	acquired   []string
	released   []string
	stoppedAll bool
	acquireErr error
}

func (h *fakeServiceHost) Acquire(_ context.Context, key string, _ spells.Service) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.acquireErr != nil {
		return h.acquireErr
	}
	h.acquired = append(h.acquired, key)
	return nil
}

func (h *fakeServiceHost) Release(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.released = append(h.released, key)
}

func (h *fakeServiceHost) StopAll() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := len(h.acquired) - len(h.released)
	h.stoppedAll = true
	return n
}

func TestServiceAcquireReleaseRoundTrip(t *testing.T) {
	host := &fakeServiceHost{}
	srv, err := New(Options{
		Handler:     func(context.Context, []string) error { return nil },
		ServiceHost: host,
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	svc := spells.Service{Command: spells.Command{Bin: "docker", Args: []string{"run", "postgres:15"}}}
	require.NoError(t, AcquireService(context.Background(), srv.Addr(), "pg", svc))
	require.NoError(t, ReleaseService(context.Background(), srv.Addr(), "pg"))

	host.mu.Lock()
	defer host.mu.Unlock()
	assert.Equal(t, []string{"pg"}, host.acquired)
	assert.Equal(t, []string{"pg"}, host.released)
}

func TestStopAllServicesRoundTrip(t *testing.T) {
	host := &fakeServiceHost{}
	srv, err := New(Options{
		Handler:     func(context.Context, []string) error { return nil },
		ServiceHost: host,
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	svc := spells.Service{Command: spells.Command{Bin: "docker", Args: []string{"run", "postgres:15"}}}
	require.NoError(t, AcquireService(context.Background(), srv.Addr(), "a", svc))
	require.NoError(t, AcquireService(context.Background(), srv.Addr(), "b", svc))

	n, err := StopAllServices(context.Background(), srv.Addr())
	require.NoError(t, err)
	assert.Equal(t, 2, n, "both hosted services reported stopped")

	host.mu.Lock()
	defer host.mu.Unlock()
	assert.True(t, host.stoppedAll)
}

func TestServiceAcquireError(t *testing.T) {
	host := &fakeServiceHost{acquireErr: errors.New("readiness failed")}
	srv, err := New(Options{
		Handler:     func(context.Context, []string) error { return nil },
		ServiceHost: host,
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	err = AcquireService(context.Background(), srv.Addr(), "pg", spells.Service{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "readiness failed")
}

func TestServiceAcquireNoHost(t *testing.T) {
	// A per-process proc server (no ServiceHost) reports hosting unavailable, so the
	// client falls back to running the service in-process for this run.
	srv, err := New(Options{Handler: func(context.Context, []string) error { return nil }})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	err = AcquireService(context.Background(), srv.Addr(), "pg", spells.Service{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not host shared services")
}

// TestRunAdoptsClientAncestry pins the plumbing the daemon's re-entrancy check rests on.
// The daemon, not the client, takes the project locks for an adopted run, so without the
// client's ancestry on ctx it cannot tell a lock it holds for THIS client's parent (a
// deadlock it must refuse) from one it holds for an unrelated client (a wait that will
// end). The ancestry only reaches it over the wire.
func TestRunAdoptsClientAncestry(t *testing.T) {
	got := make(chan []string, 1)
	s := newJobService(func(ctx context.Context, _ []string) error {
		got <- types.InvocationAncestorsFromContext(ctx)
		return nil
	})

	var reply RunReply
	want := []string{"inv-outer", "inv-nested"}
	require.NoError(t, s.run(RunRequest{Args: []string{"run", "build"}, Ancestors: want}, &reply))
	assert.Equal(t, 0, reply.ExitCode)
	assert.Equal(t, want, <-got, "the handler must see the ancestry the client sent")
}

// TestRunWithholdsReportedError pins that a failure the handler already explained does not
// come back as its sentinel's text. The client PRINTS RunReply.Err, so a dispatch that
// printed a diagnostic and returned a bare "silent exit" would have that phrase reported
// to the user as the reason their run failed.
func TestRunWithholdsReportedError(t *testing.T) {
	var reply RunReply
	s := newJobService(func(context.Context, []string) error { return reportedErr{} })
	require.NoError(t, s.run(RunRequest{Args: []string{"run", "build"}}, &reply))
	assert.Equal(t, 1, reply.ExitCode, "the run still failed")
	assert.Empty(t, reply.Err, "an already-reported failure must not ship its placeholder text")

	// An ordinary error is the other half: its message IS the only account of the failure
	// an adopted client gets, so it must cross.
	reply = RunReply{}
	s = newJobService(func(context.Context, []string) error { return errors.New("no such target") })
	require.NoError(t, s.run(RunRequest{Args: []string{"run", "nope"}}, &reply))
	assert.Equal(t, 1, reply.ExitCode)
	assert.Equal(t, "no such target", reply.Err)
}

// reportedErr stands in for cmd/magus's errSilent, which proc cannot import.
type reportedErr struct{}

func (reportedErr) Error() string         { return "silent exit" }
func (reportedErr) AlreadyReported() bool { return true }

// TestShutdownClosesServer pins the fix for the silent `server stop` no-op: a shutdown RPC
// must actually tear the server down, not just acknowledge. It asserts the observable
// signals a blocking daemon loop and `server stop`'s verification rely on - Done() closing
// and the socket going dead - because the shutdown handler cancels only the listener's own
// context, and a caller cannot see that from the reply alone.
func TestShutdownClosesServer(t *testing.T) {
	srv, err := New(Options{Handler: func(context.Context, []string) error { return nil }})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	addr := srv.Addr()
	require.True(t, SocketLive(context.Background(), addr), "server should be live before shutdown")

	// Done must not have fired yet; the daemon loop would still be blocked here.
	select {
	case <-srv.Done():
		t.Fatal("Done fired before shutdown was requested")
	default:
	}

	require.NoError(t, Shutdown(context.Background(), addr))

	// The blocking daemon loop wakes on Done(); it must close after the RPC shutdown.
	select {
	case <-srv.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Done did not close after shutdown; the daemon process would keep running")
	}

	// And the socket must actually stop answering, which is what `server stop` polls to
	// verify the daemon is gone instead of trusting the shutdown reply.
	require.Eventually(t, func() bool {
		return !SocketLive(context.Background(), addr)
	}, 2*time.Second, 20*time.Millisecond, "socket kept answering after shutdown")
}

// TestSocketLiveFalseForBogusAddr guards the negative path the stop/start probes depend on:
// an address with no daemon (or a malformed one) reports not-live rather than erroring, so a
// stop against nothing exits cleanly non-zero and a start does not think a daemon exists.
func TestSocketLiveFalseForBogusAddr(t *testing.T) {
	assert.False(t, SocketLive(context.Background(), "unix:///nonexistent/magus-daemon.sock"))
	assert.False(t, SocketLive(context.Background(), "not-a-valid-endpoint::::"))
}

// TestForwardVersionGate drives the adoption gate end-to-end through Forward: the server is
// built with one display version, the client forwards under another, and the outcome is
// asserted from both the call (adopted -> exit 0, no error; refused -> ErrVersionMismatch,
// classified NotAdopted) and whether the handler ran. Both ends run adoptionIdentity over
// the SAME test binary, so the two "unknown" cases fingerprint identically and adopt; the
// release cases exercise the exact-match and mismatch halves.
func TestForwardVersionGate(t *testing.T) {
	cases := []struct {
		name          string
		serverVersion string
		clientVersion string
		wantAdopt     bool
	}{
		{"same release adopts", "v1.0.0", "v1.0.0", true},
		{"different release refuses", "v1.0.0", "v2.0.0", false},
		{"release daemon refuses dev client", "v1.0.0", devVersionSentinel, false},
		{"empty server version disables the gate", "", devVersionSentinel, true},
		{"same dev build adopts (identical fingerprint)", devVersionSentinel, devVersionSentinel, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var called atomic.Bool
			srv, err := New(Options{
				Version: tc.serverVersion,
				Handler: func(context.Context, []string) error { called.Store(true); return nil },
			})
			require.NoError(t, err)
			defer srv.Close()
			require.NoError(t, srv.Start())
			t.Setenv("MAGUS_DAEMON_SOCKET", srv.Addr())

			code, err := Forward(context.Background(), []string{"run", "build", "x"}, tc.clientVersion, "")
			if tc.wantAdopt {
				require.NoError(t, err)
				assert.Equal(t, 0, code)
				assert.True(t, called.Load(), "an adopted call runs the handler")
				return
			}
			require.Error(t, err, "a version mismatch surfaces as an error")
			assert.True(t, errors.Is(err, ErrVersionMismatch), "got %v", err)
			assert.True(t, NotAdopted(err), "a version mismatch is classified not-adopted so the client falls back silently")
			assert.False(t, called.Load(), "a refused call must not run the handler")
		})
	}
}

// TestForwardDevDifferentFingerprintRefused proves the core fix at the wire level: a dev
// daemon (identity fingerprinted from its build) refuses a forwarded run whose version is a
// DIFFERENT dev fingerprint. Two distinct dev builds can't coexist in one test process, so
// the mismatching client frame is hand-crafted with a fabricated "dev-*" identity that
// cannot equal this binary's own. This is the stale-daemon incident in miniature.
func TestForwardDevDifferentFingerprintRefused(t *testing.T) {
	var called atomic.Bool
	srv, err := New(Options{
		Version: devVersionSentinel, // gateVersion = this binary's real dev fingerprint
		Handler: func(context.Context, []string) error { called.Store(true); return nil },
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	ep, err := endpoint.Parse(srv.Addr())
	require.NoError(t, err)
	conn, err := ep.Dial(context.Background())
	require.NoError(t, err)
	defer conn.Close()

	// A dev identity that provably differs from any real fingerprint of this binary.
	frame := `{"type":"run","args":["run","build","x"],"version":"dev-0000000000000000deadbeef","cwd":"/tmp","protocol":"v2"}` + "\n"
	_, err = conn.Write([]byte(frame))
	require.NoError(t, err)

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	require.NoError(t, err)

	var envelope struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(bytes.TrimRight(buf[:n], "\n"), &envelope))
	assert.Equal(t, "error", envelope.Type)
	assert.Equal(t, ErrVersionMismatch.Error(), envelope.Message, "a mismatched dev fingerprint is refused as a version mismatch")
	assert.False(t, called.Load(), "the refused run must not execute")
}

// TestSubmitJobVersionGate confirms the job gate uses the same identity as run: a background
// job carrying a mismatched version is refused, while a matching (or empty) one is accepted.
// The client SubmitJob sends adoptionIdentity(version), so an empty version disables the gate
// and a differing release version trips it.
func TestSubmitJobVersionGate(t *testing.T) {
	t.Run("mismatched version refused", func(t *testing.T) {
		var reply JobReply
		s := &service{gateVersion: "v1.0.0", parentCtx: context.Background()}
		err := s.submitJob(JobRequest{Magic: JobMagic, Version: "v2.0.0", Args: []string{"graph", "build"}}, &reply)
		assert.ErrorIs(t, err, ErrVersionMismatch)
		assert.Empty(t, reply.Inv, "a refused job returns no invocation id")
	})
}
