package proc

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeServiceHost struct {
	mu         sync.Mutex
	acquired   []string
	released   []string
	stoppedAll bool
	acquireErr error
}

func (h *fakeServiceHost) Acquire(_ context.Context, key string, _ types.Service) error {
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

	svc := types.Service{Command: types.Command{Bin: "docker", Args: []string{"run", "postgres:15"}}}
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

	svc := types.Service{Command: types.Command{Bin: "docker", Args: []string{"run", "postgres:15"}}}
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

	err = AcquireService(context.Background(), srv.Addr(), "pg", types.Service{})
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

	err = AcquireService(context.Background(), srv.Addr(), "pg", types.Service{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not host shared services")
}
