package daemon

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/auth"
	mcp "github.com/egladman/magus/internal/handler/mcp"
	"github.com/egladman/magus/internal/json"
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
	connectorTok, _, err := store.Create("test", time.Now().Add(time.Hour), auth.ScopeMCP)
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
	// valid token in the URL is rejected on each: the token never rides in a URL.
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

// TestServeHealthRoutesCORS proves the health routes (a browser console PWA needs to read
// /readyz cross-origin) carry the same CORSAllow allow-list the /api bridge uses, while
// staying otherwise unguarded: no bearer token is required, and no rebind check blocks a
// same-origin request. An allow-listed loopback Origin gets its Access-Control-Allow-Origin
// reflected back; an origin that is not on the list gets no CORS header at all (an allow-list
// reflect, never "*").
func TestServeHealthRoutesCORS(t *testing.T) {
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
			"/livez":  http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }),
			"/readyz": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }),
		},
	})

	serveErr := make(chan error, 1)
	go func() { serveErr <- d.Serve(ctx) }()

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitReady(t, base+"/readyz")

	loopbackOrigin := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 5 * time.Second}

	// An allow-listed loopback origin, no bearer token: health routes stay tokenless even
	// with CORS added, and the origin is reflected back.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/readyz", nil)
	require.NoError(t, err)
	req.Header.Set("Origin", loopbackOrigin)
	resp, err := client.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "health routes must stay reachable without a bearer token")
	assert.Equal(t, loopbackOrigin, resp.Header.Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "Origin", resp.Header.Get("Vary"))

	// An origin NOT on the allow-list gets no CORS header at all: CORSAllow only ever
	// reflects a matched Origin, never advertises "*".
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, base+"/livez", nil)
	require.NoError(t, err)
	req.Header.Set("Origin", "http://evil.example")
	resp, err = client.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"), "an unlisted origin must not be reflected")

	// The OPTIONS preflight a browser sends ahead of the cross-origin GET is answered here,
	// not proxied to the underlying handler.
	req, err = http.NewRequestWithContext(ctx, http.MethodOptions, base+"/readyz", nil)
	require.NoError(t, err)
	req.Header.Set("Origin", loopbackOrigin)
	resp, err = client.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	assert.Equal(t, loopbackOrigin, resp.Header.Get("Access-Control-Allow-Origin"))

	cancel()
	select {
	case err := <-serveErr:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not shut down")
	}
}

// TestServeConsoleHostedOriginCORS proves the hosted PWA Origin
// (https://eli.gladman.cc) can clear rebind and get a CORS preflight answer on
// /api/, while /mcp stays loopback-only on Origin and never reflects that site.
func TestServeConsoleHostedOriginCORS(t *testing.T) {
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

	const siteOrigin = "https://eli.gladman.cc"
	client := &http.Client{Timeout: 5 * time.Second}

	// OPTIONS /api/v1/graph from the hosted origin: rebind admits the Origin,
	// CORS outside bearer answers 204 with ACAO + PNA when requested.
	req, err := http.NewRequestWithContext(ctx, http.MethodOptions, base+"/api/v1/graph", nil)
	require.NoError(t, err)
	req.Header.Set("Origin", siteOrigin)
	req.Header.Set("Access-Control-Request-Method", "GET")
	req.Header.Set("Access-Control-Request-Private-Network", "true")
	resp, err := client.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	assert.Equal(t, siteOrigin, resp.Header.Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", resp.Header.Get("Access-Control-Allow-Private-Network"))

	// Viewer remount of /api/v1/insight must share the same posture (longer
	// pattern wins over /api/).
	req, err = http.NewRequestWithContext(ctx, http.MethodOptions, base+"/api/v1/insight", nil)
	require.NoError(t, err)
	req.Header.Set("Origin", siteOrigin)
	req.Header.Set("Access-Control-Request-Method", "GET")
	resp, err = client.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	assert.Equal(t, siteOrigin, resp.Header.Get("Access-Control-Allow-Origin"))

	cli, err := auth.Load()
	require.NoError(t, err)

	// Authenticated GET with the hosted Origin must not be 403 from rebind.
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/graph", nil)
	require.NoError(t, err)
	req.Header.Set("Origin", siteOrigin)
	req.Header.Set("Authorization", "Bearer "+cli)
	resp, err = client.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.NotEqual(t, http.StatusForbidden, resp.StatusCode, "hosted Origin must clear rebind on /api/")
	assert.Equal(t, siteOrigin, resp.Header.Get("Access-Control-Allow-Origin"))

	// An origin not on the allow-list is still refused by rebind on /api/.
	req, err = http.NewRequestWithContext(ctx, http.MethodOptions, base+"/api/v1/graph", nil)
	require.NoError(t, err)
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Access-Control-Request-Method", "GET")
	resp, err = client.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"))

	// /mcp keeps loopback-only Origin posture: the site Origin is forbidden.
	req, err = http.NewRequestWithContext(ctx, http.MethodOptions, base+"/mcp", nil)
	require.NoError(t, err)
	req.Header.Set("Origin", siteOrigin)
	req.Header.Set("Access-Control-Request-Method", "POST")
	resp, err = client.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode, "/mcp must not admit the hosted Origin")

	cancel()
	select {
	case err := <-serveErr:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not shut down")
	}
}

// TestEveryRouteRefusesAnAnonymousCaller walks every pattern the daemon mounted (read off the
// server, not a list kept here) and proves a caller with no bearer token reaches only the
// health probes and the console's app shell. A route mounted without a guard fails this test
// without anyone having to remember to add it.
func TestEveryRouteRefusesAnAnonymousCaller(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := fixtureWorkspace(t)

	// A built console holding the hosted demo's data beside the shell, as the real build does.
	consoleDir := t.TempDir()
	for name, body := range map[string]string{
		"index.html":                 "<html><head>\n</head><body>shell</body></html>",
		"console.js":                 "js",
		"sw.js":                      "self",
		"manifest.webmanifest":       "{}",
		"graph/explorer.js":          "js",
		"graph/knowledge-graph.json": `{"nodes":[{"kind":"note"}]}`,
		"graph/target-graph.json":    `{"projects":[]}`,
	} {
		p := filepath.Join(consoleDir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	}
	t.Setenv("MAGUS_CONSOLE_DIR", consoleDir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m, err := magus.Open(ctx, root)
	require.NoError(t, err)

	port := freePort(t)
	addr := netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), port)

	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	// The same three probes cmd/magus mounts; /healthz is its liveness alias.
	health := map[string]bool{"/livez": true, "/readyz": true, "/healthz": true}
	routes := map[string]http.Handler{}
	for p := range health {
		routes[p] = ok
	}
	d := New(mcp.Options{Magus: m, Version: "test", HTTPAddr: addr, HealthRoutes: routes})
	mounted := make(chan []string, 1)
	d.mounted = func(p []string) { mounted <- p }

	serveErr := make(chan error, 1)
	go func() { serveErr <- d.Serve(ctx) }()

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitReady(t, base+"/readyz")
	patterns := <-mounted
	for _, want := range []string{"/mcp", "/api/", "/console/", "/api/v1/share"} {
		require.Contains(t, patterns, want, "the walk must see the whole mux")
	}

	client := &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	anonymous := func(method, path string) *http.Response {
		reqCtx, reqCancel := context.WithTimeout(ctx, 5*time.Second)
		defer reqCancel()
		req, err := http.NewRequestWithContext(reqCtx, method, base+path, nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(body))
		return resp
	}
	status := func(method, path string) int { return anonymous(method, path).StatusCode }
	// assertRefused requires the refusal a client can act on: structured JSON in the route's
	// own protocol, carrying an MGS code, and a bearer challenge on every 401.
	assertRefused := func(method, path string) {
		t.Helper()
		resp := anonymous(method, path)
		what := method + " " + path
		if !assert.Contains(t, []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound},
			resp.StatusCode, "%s answered without a token", what) {
			return
		}
		if resp.StatusCode == http.StatusUnauthorized {
			// An anonymous caller presented no token, so the challenge carries no error=.
			assert.Equal(t, `Bearer realm="magus"`, resp.Header.Get("WWW-Authenticate"), what)
		}
		assert.Equal(t, "application/json", resp.Header.Get("Content-Type"), what)
		body, _ := io.ReadAll(resp.Body)
		// A Connect procedure (/<package>.<Service>/...) answers in Connect's error envelope,
		// whose details are base64 Any values; every other route in AIP-193's JSON, whose
		// details are readable.
		if svc, _, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/"); strings.Contains(svc, ".") {
			var got struct {
				Code    string `json:"code"`
				Message string `json:"message"`
				Details []struct {
					Type string `json:"type"`
				} `json:"details"`
			}
			if !assert.NoError(t, json.Unmarshal(body, &got), "%s: body is not JSON: %s", what, body) {
				return
			}
			assert.Contains(t, []string{"unauthenticated", "permission_denied", "not_found"}, got.Code, what)
			assert.Contains(t, got.Message, "[MGS9", what)
			if assert.Len(t, got.Details, 2, what) {
				assert.Equal(t, "google.rpc.ErrorInfo", got.Details[0].Type, what)
				assert.Equal(t, "google.rpc.Help", got.Details[1].Type, what)
			}
			return
		}
		var got struct {
			Error struct {
				Code    int    `json:"code"`
				Status  string `json:"status"`
				Details []struct {
					Reason string `json:"reason"`
				} `json:"details"`
			} `json:"error"`
		}
		if !assert.NoError(t, json.Unmarshal(body, &got), "%s: body is not JSON: %s", what, body) {
			return
		}
		assert.Equal(t, resp.StatusCode, got.Error.Code, what)
		assert.NotEmpty(t, got.Error.Status, what)
		if assert.NotEmpty(t, got.Error.Details, what) {
			assert.True(t, strings.HasPrefix(got.Error.Details[0].Reason, "MGS9"), "%s: reason %q", what, got.Error.Details[0].Reason)
		}
	}

	for _, pattern := range patterns {
		switch {
		case health[pattern]:
			assert.Equal(t, http.StatusOK, status(http.MethodGet, pattern), pattern)
		case pattern == "/console/":
			for _, p := range []string{"/console/", "/console/console.js", "/console/sw.js",
				"/console/manifest.webmanifest", "/console/graph/", "/console/graph/explorer.js"} {
				assert.Equal(t, http.StatusOK, status(http.MethodGet, p), "shell file %s", p)
			}
			for _, p := range []string{"/console/graph/knowledge-graph.json",
				"/console/graph/target-graph.json", "/console/graph/explorer.js.map"} {
				assertRefused(http.MethodGet, p)
			}
		default:
			// A trailing-slash pattern is a subtree: probe it and a path beneath it.
			paths := []string{pattern}
			if strings.HasSuffix(pattern, "/") {
				paths = append(paths, pattern+"Probe")
			}
			for _, p := range paths {
				for _, method := range []string{http.MethodGet, http.MethodPost} {
					assertRefused(method, p)
				}
			}
		}
	}

	cancel()
	select {
	case err := <-serveErr:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not shut down")
	}
}
