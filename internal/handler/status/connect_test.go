package status

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	statusv1 "github.com/egladman/magus/proto/gen/go/magus/status/v1"
	"github.com/egladman/magus/proto/gen/go/magus/status/v1/statusv1connect"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectGetStatusReportsLiveSnapshot(t *testing.T) {
	src := fakeSource{report: types.StatusReport{Pool: &types.StatusOutput{Mode: "daemon", Capacity: 4, Running: 1}}}
	svc := NewConnectService(src, types.BuildInfo{Version: "v1.2.3", Commit: "abc1234"}, nil)

	resp, err := svc.GetStatus(context.Background(), connect.NewRequest(&statusv1.GetStatusRequest{}))
	require.NoError(t, err)

	got := resp.Msg.GetStatus()
	// A running pool with no error rolls up to HEALTHY, and the build identity is stamped on.
	assert.Equal(t, statusv1.Health_HEALTH_HEALTHY, got.GetHealth())
	assert.Equal(t, int32(4), got.GetPool().GetCapacity())
	assert.Equal(t, int32(1), got.GetPool().GetRunning())
	assert.Equal(t, "v1.2.3", got.GetBuild().GetVersion())
	assert.Equal(t, "abc1234", got.GetBuild().GetCommit())
}

// TestConnectGetStatusCarriesEnvelopeExtras pins the two static per-session fields that moved off the
// deprecated JSON route onto the GetStatus response envelope: observing_since and the resolved config.
func TestConnectGetStatusCarriesEnvelopeExtras(t *testing.T) {
	since := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
	src := fakeSource{report: types.StatusReport{
		ObservingSince: since,
		Config:         types.StatusConfig{DefaultCharms: []string{"rw"}, Concurrency: 8, Sandbox: true},
	}}
	svc := NewConnectService(src, types.BuildInfo{}, nil)

	resp, err := svc.GetStatus(context.Background(), connect.NewRequest(&statusv1.GetStatusRequest{}))
	require.NoError(t, err)

	assert.Equal(t, since.Unix(), resp.Msg.GetObservingSince().GetSeconds())
	cfg := resp.Msg.GetConfig()
	require.NotNil(t, cfg)
	assert.Equal(t, []string{"rw"}, cfg.GetDefaultCharms())
	assert.Equal(t, int32(8), cfg.GetConcurrency())
	assert.True(t, cfg.GetSandbox())
}

// A non-daemon report leaves observing_since zero, so GetStatus must omit it (not stamp epoch).
func TestConnectGetStatusOmitsZeroObservingSince(t *testing.T) {
	svc := NewConnectService(fakeSource{}, types.BuildInfo{}, nil)
	resp, err := svc.GetStatus(context.Background(), connect.NewRequest(&statusv1.GetStatusRequest{}))
	require.NoError(t, err)
	assert.Nil(t, resp.Msg.GetObservingSince(), "zero observing-since must be omitted, not epoch")
}

// mutableSource lets a test flip the reported snapshot mid-stream so StreamStatus's push-on-change
// path is exercised. The mutex keeps the handler goroutine's read race-clean against the test's write.
type mutableSource struct {
	mu     sync.Mutex
	report types.StatusReport
}

func (m *mutableSource) StatusReport(context.Context) types.StatusReport {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.report
}

func (m *mutableSource) set(r types.StatusReport) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.report = r
}

// TestConnectStreamStatusPushesInitialThenOnChange drives the real Connect StreamStatus RPC over
// an httptest server (connect.ServerStream has no injectable test sink), verifying it emits the
// connect-time snapshot and then a fresh frame after the underlying report changes.
func TestConnectStreamStatusPushesInitialThenOnChange(t *testing.T) {
	src := &mutableSource{}
	src.set(types.StatusReport{Pool: &types.StatusOutput{Mode: "daemon", Running: 0}})
	svc := NewConnectService(src, types.BuildInfo{Version: "v1"}, nil)
	svc.interval = 10 * time.Millisecond // tighten the poll so the test does not wait on the 2s default

	path, handler := statusv1connect.NewStatusServiceHandler(svc)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := statusv1connect.NewStatusServiceClient(srv.Client(), srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stream, err := client.StreamStatus(ctx, connect.NewRequest(&statusv1.StreamStatusRequest{}))
	require.NoError(t, err)

	// First frame is the connect-time snapshot (Running 0).
	require.True(t, stream.Receive())
	assert.Equal(t, int32(0), stream.Msg().GetStatus().GetPool().GetRunning())

	// Flip the snapshot so the next tick observes a change and pushes a second frame.
	src.set(types.StatusReport{Pool: &types.StatusOutput{Mode: "daemon", Running: 2}})
	require.True(t, stream.Receive())
	assert.Equal(t, int32(2), stream.Msg().GetStatus().GetPool().GetRunning())
	require.NoError(t, stream.Close())
}
