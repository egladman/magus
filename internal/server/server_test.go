package server

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

	"connectrpc.com/connect"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/auth"
	mcp "github.com/egladman/magus/internal/handler/mcp"
	"github.com/egladman/magus/internal/httpx"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/rpcerr"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/proto/gen/go/magus/job/v1alpha1/jobv1alpha1connect"
	statusv1alpha1 "github.com/egladman/magus/proto/gen/go/magus/status/v1alpha1"
	"github.com/egladman/magus/proto/gen/go/magus/status/v1alpha1/statusv1alpha1connect"
	"github.com/egladman/magus/types"
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
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module servertest\n"), 0o644))
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
	t.Fatal("server did not become ready")
}

// TestServeBearerGuardTwoTier boots a real server against a fixture workspace and
// proves the two-tier bearer guard is wired onto both /mcp and the /api bridge:
// unauthenticated requests get 401, while both the retrievable operator token and a
// non-expired named connector token pass the guard.
func TestServeBearerGuardTwoTier(t *testing.T) {
	testkit.Isolate(t)
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

	// EnsureOperator minted the operator token during Serve; read it back.
	cli, err := auth.LoadOperator()
	require.NoError(t, err)

	// A non-expired connector token reaches /mcp.
	dir, err := auth.StoreDir()
	require.NoError(t, err)
	store, err := auth.LoadStore(dir)
	require.NoError(t, err)
	connectorTok, _, err := store.Mint(types.GrantOperator, auth.MintRequest{Name: "test", Grant: types.GrantConnector, TTL: time.Hour})
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
		t.Fatal("server did not shut down")
	}
}

// TestServeHealthRoutesCORS proves the health routes (a browser console PWA needs to read
// /readyz cross-origin) carry the same CORSAllow allow-list the /api bridge uses, while
// staying otherwise unguarded: no bearer token is required, and no rebind check blocks a
// same-origin request. An allow-listed loopback Origin gets its Access-Control-Allow-Origin
// reflected back; an origin that is not on the list gets no CORS header at all (an allow-list
// reflect, never "*").
func TestServeHealthRoutesCORS(t *testing.T) {
	testkit.Isolate(t)
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
		t.Fatal("server did not shut down")
	}
}

// TestServeConsoleHostedOriginCORS proves the hosted PWA Origin
// (https://eli.gladman.cc) can clear rebind and get a CORS preflight answer on
// /api/, while /mcp stays loopback-only on Origin and never reflects that site.
func TestServeConsoleHostedOriginCORS(t *testing.T) {
	testkit.Isolate(t)
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

	cli, err := auth.LoadOperator()
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
		t.Fatal("server did not shut down")
	}
}

// TestEveryRouteRefusesAnAnonymousCaller walks every pattern the server mounted (read off the
// server, not a list kept here) and proves a caller with no bearer token reaches only the
// health probes and the console's app shell. A route mounted without a guard fails this test
// without anyone having to remember to add it.
func TestEveryRouteRefusesAnAnonymousCaller(t *testing.T) {
	testkit.Isolate(t)
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
	d.onMounted = func(p []string, _ map[string]types.Need) { mounted <- p }

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
		case pattern == "/api/v1/token/exchange":
			// No bearer here: the code in the body is the credential, so an anonymous caller
			// without one is refused for the missing code, and one with a wrong code as a
			// wrong bearer would be.
			assert.Equal(t, http.StatusMethodNotAllowed, status(http.MethodGet, pattern))
			assert.Equal(t, http.StatusBadRequest, status(http.MethodPost, pattern))
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
		t.Fatal("server did not shut down")
	}
}

// A server whose workspace failed to load still listens: status and the console shell are
// served, and every workspace call answers the failure in its route's own protocol behind
// the same guard, so an anonymous caller learns nothing about the tree.
func TestServeUnloadedAnswersWorkspaceCallsWithTheFailure(t *testing.T) {
	testkit.Isolate(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	port := freePort(t)
	addr := netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), port)
	failure := &types.WorkspaceFailure{
		Message: "magusfile: exec magusfile.buzz: [BZZ1005] ...",
		Diagnostics: []types.SourceDiagnostic{{
			Code: "BZZ1005", File: "magusfile.buzz", Line: 3, Column: 3, Message: "cannot assign str to int",
		}},
	}
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	d := NewUnloaded(mcp.Options{Version: "test", HTTPAddr: addr, HealthRoutes: map[string]http.Handler{"/readyz": ok}},
		Unloaded{Root: "/repo", Err: func() rpcerr.Error { return rpcerr.WorkspaceFailed("/repo", failure) }})
	serveErr := make(chan error, 1)
	go func() { serveErr <- d.Serve(ctx) }()

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitReady(t, base+"/readyz")
	cli, err := auth.LoadOperator()
	require.NoError(t, err)

	call := func(method, path, token, contentType, body string) (int, []byte) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, base+path, strings.NewReader(body))
		require.NoError(t, err)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		got, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, got
	}

	graph := "/magus.graph.v1alpha1.GraphService/QueryNodes"
	code, _ := call(http.MethodPost, graph, "", "application/json", "{}")
	assert.Equal(t, http.StatusUnauthorized, code, "the guard still comes first")

	code, body := call(http.MethodPost, graph, cli, "application/json", "{}")
	assert.Equal(t, http.StatusBadRequest, code)
	var connectErr struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details []struct {
			Type string `json:"type"`
		} `json:"details"`
	}
	require.NoError(t, json.Unmarshal(body, &connectErr), "%s", body)
	assert.Equal(t, "failed_precondition", connectErr.Code)
	assert.Contains(t, connectErr.Message, "[MGS3016] workspace /repo failed to load: magusfile.buzz:3:3 [BZZ1005]")
	var kinds []string
	for _, d := range connectErr.Details {
		kinds = append(kinds, d.Type)
	}
	assert.Equal(t, []string{"google.rpc.ErrorInfo", "google.rpc.PreconditionFailure", "google.rpc.ResourceInfo", "google.rpc.Help"}, kinds)

	code, body = call(http.MethodGet, "/api/v1/graph", cli, "", "")
	assert.Equal(t, http.StatusBadRequest, code)
	var aip struct {
		Error struct {
			Status  string `json:"status"`
			Details []struct {
				Type       string `json:"@type"`
				Reason     string `json:"reason"`
				Violations []struct {
					Type    string `json:"type"`
					Subject string `json:"subject"`
				} `json:"violations"`
			} `json:"details"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(body, &aip), "%s", body)
	assert.Equal(t, "FAILED_PRECONDITION", aip.Error.Status)
	require.Len(t, aip.Error.Details, 4)
	assert.Equal(t, "MGS3016", aip.Error.Details[0].Reason)
	require.Len(t, aip.Error.Details[1].Violations, 1)
	assert.Equal(t, "BZZ1005", aip.Error.Details[1].Violations[0].Type)
	assert.Equal(t, "magusfile.buzz:3:3", aip.Error.Details[1].Violations[0].Subject)

	// StatusService needs no workspace, so it answers even now.
	code, body = call(http.MethodPost, "/magus.status.v1alpha1.StatusService/GetStatus", cli, "application/json", "{}")
	assert.Equal(t, http.StatusOK, code, "%s", body)

	cancel()
	select {
	case err := <-serveErr:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("server did not shut down")
	}
}

// TestServeUnloadedRefusesEveryLoadedConnectService derives, from a REAL loaded server's own
// mounted patterns, every Connect service prefix that reads the workspace (StatusService
// excepted, since it needs none), then asserts an unloaded server refuses each one with the
// load error rather than a bare 404, so the two servers cannot drift apart in what they mount.
func TestServeUnloadedRefusesEveryLoadedConnectService(t *testing.T) {
	testkit.Isolate(t)
	root := fixtureWorkspace(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m, err := magus.Open(ctx, root)
	require.NoError(t, err)

	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	loadedPort := freePort(t)
	loadedAddr := netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), loadedPort)
	loaded := New(mcp.Options{Magus: m, Version: "test", HTTPAddr: loadedAddr, HealthRoutes: map[string]http.Handler{"/readyz": ok}})
	loadedMounted := make(chan []string, 1)
	loaded.onMounted = func(p []string, _ map[string]types.Need) { loadedMounted <- p }
	loadedErr := make(chan error, 1)
	go func() { loadedErr <- loaded.Serve(ctx) }()
	loadedBase := fmt.Sprintf("http://127.0.0.1:%d", loadedPort)
	waitReady(t, loadedBase+"/readyz")
	loadedPatterns := <-loadedMounted

	// A Connect service is mounted as a path-prefix pattern whose first segment is the
	// fully-qualified service name (it contains a "."); every other mount here is a plain
	// JSON route or the console shell.
	var services []string
	for _, p := range loadedPatterns {
		name, _, cut := strings.Cut(strings.TrimPrefix(p, "/"), "/")
		if !cut || !strings.Contains(name, ".") || name == statusv1alpha1connect.StatusServiceName {
			continue
		}
		services = append(services, name)
	}
	require.NotEmpty(t, services, "the loaded server must mount at least one workspace Connect service")

	failure := &types.WorkspaceFailure{Message: "magusfile: exec magusfile.buzz: [BZZ1005] ..."}
	unloadedPort := freePort(t)
	unloadedAddr := netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), unloadedPort)
	unloaded := NewUnloaded(mcp.Options{Version: "test", HTTPAddr: unloadedAddr, HealthRoutes: map[string]http.Handler{"/readyz": ok}},
		Unloaded{Root: "/repo", Err: func() rpcerr.Error { return rpcerr.WorkspaceFailed("/repo", failure) }})
	unloadedErr := make(chan error, 1)
	go func() { unloadedErr <- unloaded.Serve(ctx) }()
	unloadedBase := fmt.Sprintf("http://127.0.0.1:%d", unloadedPort)
	waitReady(t, unloadedBase+"/readyz")

	cli, err := auth.LoadOperator()
	require.NoError(t, err)

	for _, name := range services {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, unloadedBase+"/"+name+"/Probe", strings.NewReader("{}"))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+cli)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		_ = resp.Body.Close()
		if !assert.NotEqual(t, http.StatusNotFound, resp.StatusCode, "%s: the unloaded server is missing this mount", name) {
			continue
		}
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "%s: %s", name, body)
		var got struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		require.NoError(t, json.Unmarshal(body, &got), "%s: %s", name, body)
		assert.Equal(t, "failed_precondition", got.Code, name)
		assert.Contains(t, got.Message, "[MGS3016]", name)
	}

	cancel()
	for _, errCh := range []chan error{loadedErr, unloadedErr} {
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(10 * time.Second):
			t.Fatal("server did not shut down")
		}
	}
}

// serverSocket starts a real proc server on a socket in a fresh short directory (t.TempDir's
// can exceed the unix socket length limit on macOS), skipping where a peer's uid cannot be
// read, and returns it with a client that dials it.
func serverSocket(t *testing.T) (*proc.Server, string, *http.Client) {
	t.Helper()
	if !httpx.PeerCredentialsSupported() {
		t.Skip("no peer credentials on this platform")
	}
	t.Setenv(proc.SocketEnv, "")
	dir, err := os.MkdirTemp("", "srv")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "server.sock")
	srv, err := proc.New(proc.Options{
		Handler: func(context.Context, []string) error { return nil },
		Address: "unix://" + path,
	})
	require.NoError(t, err)
	require.NoError(t, srv.Start())
	t.Cleanup(srv.Close)
	return srv, srv.Addr(), &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", path)
		},
	}}
}

// TestServerSocketCarriesMCPAndTheAPIs mounts a loaded server on a real server socket and
// drives each surface there without a token: an MCP session whose tool call the trail
// attributes to the socket peer, a Connect call, and a proc route beside them. The socket's
// route table is the loopback one minus the browser-only /api routes, Need for Need. The
// loopback /mcp still refuses the same tokenless request.
func TestServerSocketCarriesMCPAndTheAPIs(t *testing.T) {
	srv, addr, client := serverSocket(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	builtConsole(t)
	m, err := magus.Open(t.Context(), fixtureWorkspace(t))
	require.NoError(t, err)

	socketNeeds := make(chan map[string]types.Need, 1)
	d, tcp := testServer(t, func(opts mcp.Options) *Server {
		opts.Magus = m
		return New(opts, WithSocket(srv))
	})
	d.onSocketMounted = func(n map[string]types.Need) { socketNeeds <- n }
	base, _, tcpNeeds := serveMounted(t, d, tcp)

	onSocket := <-socketNeeds
	want := map[string]types.Need{}
	for path, need := range tcpNeeds {
		if !strings.HasPrefix(path, "/api/") {
			want[path] = need
		}
	}
	assert.Equal(t, want, onSocket, "the socket serves every loopback route but the browser's /api, held to the same Need")
	assert.Contains(t, onSocket, "/mcp")

	call := func(c *http.Client, url, session, body string) (*http.Response, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if session != "" {
			req.Header.Set("Mcp-Session-Id", session)
		}
		resp, err := c.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		out, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp, string(out)
	}

	const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"socket-test","version":"1"}}}`
	resp, out := call(client, "http://magus/mcp", "", initialize)
	require.Equal(t, http.StatusOK, resp.StatusCode, out)
	session := resp.Header.Get("Mcp-Session-Id")
	require.NotEmpty(t, session)

	resp, out = call(client, "http://magus/mcp", session, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"magus_config_get","arguments":{}}}`)
	require.Equal(t, http.StatusOK, resp.StatusCode, out)
	assert.Contains(t, out, `"result"`)
	assert.NotContains(t, out, `"isError":true`, "the socket peer's grant reaches every tool")

	events, err := os.ReadFile(filepath.Join(m.CacheDir(), "activity", "events.jsonl"))
	require.NoError(t, err)
	assert.Contains(t, string(events), `"action":"magus_config_get"`)
	assert.Contains(t, string(events), `"class":"socket-peer"`, "the call is attributed to the socket peer")

	status := statusv1alpha1connect.NewStatusServiceClient(client, "http://magus")
	_, err = status.GetStatus(t.Context(), connect.NewRequest(&statusv1alpha1.GetStatusRequest{}))
	require.NoError(t, err, "a Connect service answers the socket peer")

	pool, err := proc.QueryStatus(t.Context(), addr)
	require.NoError(t, err, "the proc routes share the socket")
	assert.Equal(t, os.Getpid(), pool.ParentPID)

	resp, out = call(http.DefaultClient, base+"/mcp", "", initialize)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "loopback HTTP still needs a bearer: %s", out)
}

// A server whose workspace failed mounts on the socket too, and its services answer there with
// the load failure, as they do on loopback.
func TestServerSocketCarriesTheUnloadedFailure(t *testing.T) {
	srv, _, client := serverSocket(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	builtConsole(t)
	root := t.TempDir()
	failure := &types.WorkspaceFailure{Message: "magusfile: exec magusfile.buzz: [BZZ1005] ..."}
	d, tcp := testServer(t, func(opts mcp.Options) *Server {
		return NewUnloaded(opts, Unloaded{Root: root, Err: func() rpcerr.Error { return rpcerr.WorkspaceFailed(root, failure) }}, WithSocket(srv))
	})
	serveMounted(t, d, tcp)

	req, err := http.NewRequest(http.MethodPost, "http://magus/"+jobv1alpha1connect.JobServiceName+"/ListJobs", strings.NewReader(`{}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "FAILED_PRECONDITION: %s", body)
	assert.Contains(t, string(body), string(types.WorkspaceLoadFailed))
}
