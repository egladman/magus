package web

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServeBlobServesOnceCORSLocked confirms the one-shot server serves the blob with the
// content type and CORS origin lock, and WaitServed reports ServeCompleted after the fetch.
func TestServeBlobServesOnceCORSLocked(t *testing.T) {
	raw := []byte(`{"nodes":1}`)
	bs, err := ServeBlob(Config{Origin: "https://example.test"}, "/graph.json", "application/json", raw)
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

// TestServeBlobPreflight confirms an OPTIONS preflight is answered 204 with the allowed
// methods, without consuming the one-shot fetch.
func TestServeBlobPreflight(t *testing.T) {
	bs, err := ServeBlob(Config{Origin: "https://example.test"}, "/graph.json", "application/json", []byte("{}"))
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

func canceledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
