package daemon

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/auth"
	mcp "github.com/egladman/magus/internal/handler/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// freePort returns a currently-free loopback port. There is a small window
// between close and re-bind, which is standard and acceptable for a test.
func freePort(t *testing.T) uint16 {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := uint16(ln.Addr().(*net.TCPAddr).Port)
	require.NoError(t, ln.Close())
	return port
}

// fixtureWorkspace writes a minimal single-project workspace (a go.mod plus one
// JS project marker) that magus.Open can discover.
func fixtureWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module daemontest\n"), 0o644))
	pkg := filepath.Join(root, "pkg")
	require.NoError(t, os.MkdirAll(pkg, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pkg, "package.json"), []byte(`{"name":"pkg"}`), 0o644))
	return root
}

func waitReady(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url) //nolint:noctx // short-lived readiness poll in a test
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("daemon did not become ready")
}

// TestServeBearerGuardTwoTier boots a real daemon against a fixture workspace and
// proves the two-tier bearer guard is wired onto both /mcp and the /api bridge:
// unauthenticated requests get 401, while both the retrievable cli token and a
// non-expired named connector token pass the guard.
func TestServeBearerGuardTwoTier(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := fixtureWorkspace(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m, err := magus.Open(ctx, root)
	require.NoError(t, err)

	port := freePort(t)
	addr := netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), port)

	d := New(mcp.Options{
		Magus:    m,
		Version:  "test",
		HTTPAddr: addr,
		HealthRoutes: map[string]http.Handler{
			"/readyz": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }),
		},
	})

	serveErr := make(chan error, 1)
	go func() { serveErr <- d.Serve(ctx) }()

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitReady(t, base+"/readyz")

	// Resolve minted the cli token during Serve; read it back.
	cli, err := auth.Load()
	require.NoError(t, err)

	// A non-expired named connector token is the second accepted tier.
	store, err := auth.LoadConnectorStore()
	require.NoError(t, err)
	connectorTok, _, err := store.Create("test", time.Now().Add(time.Hour))
	require.NoError(t, err)

	// status issues a GET and returns only the status code. It must NOT read the
	// body: a GET to /mcp that passes auth opens a long-lived SSE stream that
	// never closes, so reading it would block. Closing the body tears the stream
	// down; the per-request timeout bounds a server that never sends headers.
	client := &http.Client{Timeout: 5 * time.Second}
	status := func(path, token string) int {
		reqCtx, reqCancel := context.WithTimeout(ctx, 5*time.Second)
		defer reqCancel()
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, base+path, nil)
		require.NoError(t, err)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	// /mcp: unauthenticated rejected; both tiers pass the guard (a request that
	// gets past auth is never 401, whatever the MCP handler then makes of a GET).
	assert.Equal(t, http.StatusUnauthorized, status("/mcp", ""), "no token")
	assert.Equal(t, http.StatusUnauthorized, status("/mcp", "not-the-token"), "wrong token")
	assert.NotEqual(t, http.StatusUnauthorized, status("/mcp", cli), "cli token should pass the guard")
	assert.NotEqual(t, http.StatusUnauthorized, status("/mcp", connectorTok), "connector token should pass the guard")

	// /api bridge uses the same header-only guard as /mcp: the explorer sends the
	// token in an Authorization header (its SSE reader is fetch()-based, not an
	// EventSource), so no query-param carrier is offered.
	assert.Equal(t, http.StatusUnauthorized, status("/api/v1/graph", ""), "no token on bridge")
	assert.NotEqual(t, http.StatusUnauthorized, status("/api/v1/graph", cli), "cli token on bridge")

	// queryStatus presents the token ONLY as a `?token=` query param, never a header.
	queryStatus := func(path, token string) int {
		reqCtx, reqCancel := context.WithTimeout(ctx, 5*time.Second)
		defer reqCancel()
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, base+path+"?token="+token, nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	// The hardening invariant: both /mcp and the /api bridge are header-only, so a
	// valid token in the URL is rejected on each - the token never rides in a URL.
	assert.Equal(t, http.StatusUnauthorized, queryStatus("/mcp", cli), "/mcp must reject a query-param token")
	assert.Equal(t, http.StatusUnauthorized, queryStatus("/api/v1/graph", cli), "/api must reject a query-param token")

	cancel()
	select {
	case err := <-serveErr:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not shut down")
	}
}
