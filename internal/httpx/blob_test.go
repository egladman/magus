package httpx

import (
	"context"
	"io"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStartBlobServesOnceCORSLocked confirms the one-shot server serves the blob with the
// content type and CORS origin lock, and WaitServed reports ServeCompleted after the fetch.
func TestStartBlobServesOnceCORSLocked(t *testing.T) {
	raw := []byte(`{"nodes":1}`)
	bs, err := StartBlob("https://example.test", "/graph.json", "application/json", raw)
	require.NoError(t, err)

	resp, err := http.Get(bs.SourceURL())
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	assert.Equal(t, "https://example.test", resp.Header.Get("Access-Control-Allow-Origin"))
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, raw, body)

	// The fetch marks the run served; cancel shortly after (past the handler's close),
	// so WaitServed skips most of the grace window yet still reports Completed.
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(150 * time.Millisecond); cancel() }()
	assert.Equal(t, ServeCompleted, bs.WaitServed(ctx))
}

// TestStartBlobPreflight confirms an OPTIONS preflight is answered 204 with the allowed
// methods, without consuming the one-shot fetch.
func TestStartBlobPreflight(t *testing.T) {
	bs, err := StartBlob("https://example.test", "/graph.json", "application/json", []byte("{}"))
	require.NoError(t, err)
	defer bs.WaitServed(canceledCtx())

	req, _ := http.NewRequest(http.MethodOptions, bs.SourceURL(), nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Access-Control-Allow-Methods"), "GET")
	assert.Equal(t, "https://example.test", resp.Header.Get("Access-Control-Allow-Origin"))
}

// TestStartBlobRequiresToken confirms the blob route is gated by the per-run bearer token:
// SourceURL carries it and fetches; a tokenless fetch is rejected with a 401 challenge.
func TestStartBlobRequiresToken(t *testing.T) {
	bs, err := StartBlob("https://example.test", "/graph.json", "application/json", []byte("{}"))
	require.NoError(t, err)
	defer bs.WaitServed(canceledCtx())

	assert.Contains(t, bs.SourceURL(), "token="+bs.Token())

	resp, err := http.Get("http://" + bs.Addr() + "/graph.json")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.NotEmpty(t, resp.Header.Get("WWW-Authenticate"))
}

// TestStartBlobEphemeralPort confirms StartBlob binds a real ephemeral port on loopback.
func TestStartBlobEphemeralPort(t *testing.T) {
	bs, err := StartBlob("https://example.test", "/b", "text/plain", []byte("x"))
	require.NoError(t, err)
	defer bs.WaitServed(canceledCtx())

	host, portStr, err := net.SplitHostPort(bs.Addr())
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1", host)
	port, _ := strconv.Atoi(portStr)
	assert.Greater(t, port, 0, "StartBlob binds a real ephemeral loopback port")
}

func canceledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
