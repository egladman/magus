package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/types/daemon"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	internalmcp "github.com/egladman/magus/internal/handler/mcp"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/types"
)

// With mcp.http false the endpoint names no URL and does not read as serving, even with the
// listener up: the mcp probe fails with a note naming stdio and the socket, and the console,
// which still rides the listener, still reads as served.
func TestMCPEndpointStatusWithHTTPOff(t *testing.T) {
	cfg := mcpServing(t, http.StatusOK)
	cfg.HTTP = new(false)
	got := BuildMCPEndpointStatus(context.Background(), cfg)
	require.NotNil(t, got)
	assert.Equal(t, mcpHTTPOff, got.State)
	assert.Empty(t, got.URL, "no /mcp URL to connect to")
	assert.False(t, got.Enabled)
	assert.True(t, got.Reachable, "the listener is up for the console")
	assert.Contains(t, got.Note, "mcp.http=false")
	assert.Contains(t, got.Note, "magus mcp")
	assert.Contains(t, got.Note, "server socket")

	ok, reason := evaluateMCPHealth(got)
	assert.False(t, ok, "the mcp probe fails: nothing serves MCP over HTTP")
	assert.Equal(t, got.Note, reason)

	console := BuildConsoleStatus(config.Console{}, got)
	assert.Equal(t, "serving", console.State)
	assert.Equal(t, cfg.Address, console.Address)
}

// makeReply builds a minimal StatusReply with the given pid and workspaces.
func makeReply(pid int, roots ...string) *proc.StatusReply {
	r := &proc.StatusReply{ParentPID: pid}
	for _, root := range roots {
		r.Workspaces = append(r.Workspaces, proc.Workspace{
			Root:       root,
			LoadedAt:   time.Now(),
			LastAccess: time.Now(),
		})
	}
	return r
}

func TestEvaluateHealth(t *testing.T) {
	// assertHealth evaluates one health scenario and checks the ok flag plus a
	// substring of the reason.
	assertHealth := func(name string, reply *proc.StatusReply, err error, kind ProbeKind, root string, wantOK bool, wantSub string) {
		t.Run(name, func(t *testing.T) {
			ok, reason := evaluateHealth(reply.StatusOutput(), err, kind, root)
			assert.Equal(t, wantOK, ok, "reason: %q", reason)
			if wantSub != "" {
				assert.Contains(t, reason, wantSub)
			}
		})
	}

	assertHealth("liveness/unreachable-err", nil, errors.New("dial failed"), probeLiveness, "", false, "unreachable")
	assertHealth("liveness/nil-reply-no-err", nil, nil, probeLiveness, "", false, "unreachable")
	assertHealth("readiness/unreachable", nil, errors.New("no socket"), probeReadiness, "", false, "unreachable")
	assertHealth("liveness/server-up-no-workspaces", makeReply(42), nil, probeLiveness, "", true, "42")
	assertHealth("liveness/server-up-with-workspace", makeReply(99, "/ws"), nil, probeLiveness, "", true, "99")
	assertHealth("readiness/no-workspaces-no-root", makeReply(1), nil, probeReadiness, "", false, "no workspaces")
	assertHealth("readiness/one-workspace-no-root", makeReply(1, "/ws"), nil, probeReadiness, "", true, "1 workspace")
	assertHealth("readiness/two-workspaces-no-root", makeReply(1, "/ws", "/ws2"), nil, probeReadiness, "", true, "2 workspace")
	assertHealth("readiness/root-present", makeReply(1, "/repo"), nil, probeReadiness, "/repo", true, "/repo")
	assertHealth("readiness/root-missing", makeReply(1, "/other"), nil, probeReadiness, "/repo", false, "/repo")
	assertHealth("readiness/root-given-no-workspaces", makeReply(1), nil, probeReadiness, "/repo", false, "/repo")
	assertHealth("readiness/root-trailing-slash-normalised", makeReply(1, "/repo"), nil, probeReadiness, "/repo/", true, "/repo")
	failed := &proc.StatusReply{ParentPID: 1, Workspaces: []proc.Workspace{
		{Root: "/repo", State: daemon.WorkspaceFailed}, {Root: "/loading", State: daemon.WorkspaceLoading},
	}}
	assertHealth("readiness/failed-root-names-the-code", failed, nil, probeReadiness, "/repo", false, "MGS3016")
	assertHealth("readiness/loading-root-not-ready", failed, nil, probeReadiness, "/loading", false, "is loading")
	assertHealth("readiness/only-unloaded-workspaces", failed, nil, probeReadiness, "", false, "no workspaces loaded")
	assertHealth("readiness/per-process-pool-rejected", nil, errNotServer, probeReadiness, "", false, "not the server")
}

// TestServerSnapshotRefusesAPerProcessPool pins what replaced the "proc" mode string: only
// a reply that carries the server's own report is the server, so a per-process pool
// answering the probed socket reads as no server rather than as one with nothing loaded.
func TestServerSnapshotRefusesAPerProcessPool(t *testing.T) {
	srv, err := proc.New(t.Context(), proc.Options{Limiter: cache.NewLimiter(4), Handler: func(context.Context, []string) error { return nil }})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start(t.Context()))

	_, err = serverSnapshot(srv.Addr())(t.Context())
	require.ErrorIs(t, err, errNotServer)

	server, err := proc.New(t.Context(), proc.Options{
		Limiter: cache.NewLimiter(4),
		Handler: func(context.Context, []string) error { return nil },
		Server:  func() *daemon.Server { return &daemon.Server{PID: 1} },
	})
	require.NoError(t, err)
	defer server.Close()
	require.NoError(t, server.Start(t.Context()))
	out, err := serverSnapshot(server.Addr())(t.Context())
	require.NoError(t, err)
	assert.NotNil(t, out)
}

// TestServerSnapshotAsksTheServerNotTheRunsPool is the probe asking the right process.
// Inside a run MAGUS_PROC_SOCKET names that run's own per-process pool, and pools no longer
// bind server.address, so a probe that read the variable (or scanned for any live socket)
// reached the pool and reported the server down while it was up. The probe asks
// server.address, whatever the environment says.
func TestServerSnapshotAsksTheServerNotTheRunsPool(t *testing.T) {
	pool, err := proc.New(t.Context(), proc.Options{Limiter: cache.NewLimiter(4), Handler: func(context.Context, []string) error { return nil }})
	require.NoError(t, err)
	defer pool.Close()
	require.NoError(t, pool.Start(t.Context()))

	server, err := proc.New(t.Context(), proc.Options{
		Limiter: cache.NewLimiter(4),
		Handler: func(context.Context, []string) error { return nil },
		Server:  func() *daemon.Server { return &daemon.Server{PID: 7} },
	})
	require.NoError(t, err)
	defer server.Close()
	require.NoError(t, server.Start(t.Context()))
	// Set only now: proc.New refuses to host a pool while the variable names a parent.
	t.Setenv(proc.SocketEnv, pool.Addr())

	out, err := serverSnapshot(server.Addr())(t.Context())
	require.NoError(t, err, "the probe must reach the server, not the run's pool")
	ok, reason := evaluateHealth(out, err, probeLiveness, "")
	assert.True(t, ok, reason)
}

func TestHealthHTTPHandler(t *testing.T) {
	// assertHandler stands up a handler with a fake querier and checks the HTTP
	// status code for a given probe kind and ?workspace= filter.
	assertHandler := func(name string, kind ProbeKind, workspaces []string, queryWS string, wantStatus int) {
		t.Run(name, func(t *testing.T) {
			reply := makeReply(1, workspaces...)
			// Build a handler with a fake querier instead of dialing a real socket.
			h := healthHTTPHandler(kind, func(context.Context) (*daemon.Output, error) {
				return reply.StatusOutput(), nil
			})
			url := "/"
			if queryWS != "" {
				url = fmt.Sprintf("/?workspace=%s", queryWS)
			}
			req := httptest.NewRequest(http.MethodGet, url, nil)
			rec := httptest.NewRecorder()
			h(rec, req)
			assert.Equal(t, wantStatus, rec.Code, "body: %q", rec.Body.String())
			// The body is a fixed generic token, never evaluateHealth's reason (which
			// embeds the server PID). A kubelet reads only the status code, so this
			// redaction cannot break probe use.
			wantBody := "ok\n"
			if wantStatus != http.StatusOK {
				wantBody = "unavailable\n"
			}
			assert.Equal(t, wantBody, rec.Body.String())
		})
	}

	assertHandler("liveness/ok", probeLiveness, nil, "", http.StatusOK)
	assertHandler("liveness/ok-with-workspaces", probeLiveness, []string{"/ws"}, "", http.StatusOK)
	assertHandler("readiness/no-ws", probeReadiness, nil, "", http.StatusServiceUnavailable)
	assertHandler("readiness/ws-loaded-no-filter", probeReadiness, []string{"/ws"}, "", http.StatusOK)
	assertHandler("readiness/ws-match", probeReadiness, []string{"/ws"}, "/ws", http.StatusOK)
	assertHandler("readiness/ws-no-match", probeReadiness, []string{"/ws"}, "/other", http.StatusServiceUnavailable)
}

// TestHealthHTTPHandlerUnreachable drives the error path with a querier that
// cannot be reached.
func TestHealthHTTPHandlerUnreachable(t *testing.T) {
	// A proc-dial error carrying the server socket path is exactly the kind of text
	// evaluateHealth would fold into its reason ("server unreachable: ...dial <socket>").
	h := healthHTTPHandler(probeLiveness, func(context.Context) (*daemon.Output, error) {
		return nil, errors.New("proc: query: dial /var/run/magus/server.sock: connection refused")
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h(rec, req)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	// The redacted body must not surface the error text (and its socket path).
	assert.Equal(t, "unavailable\n", rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "server.sock")
}

// TestHealthEndpointBodiesRedactSensitiveDetail is the regression guard for the security
// fix: the unguarded /livez, /healthz, and /readyz bodies must never carry the server PID,
// a workspace root, or any filesystem path, even when the underlying snapshot is rich with
// them. It drives the real handlers with a snapshot whose workspace roots and PID are
// distinctive sentinels, then asserts none of those sentinels reach any body.
func TestHealthEndpointBodiesRedactSensitiveDetail(t *testing.T) {
	const (
		sentinelPID  = 424242
		sentinelRoot = "/Users/secret/private-repo"
	)
	snapshot := makeReply(sentinelPID, sentinelRoot, "/srv/another/workspace").StatusOutput()
	statusFn := func(context.Context) (*daemon.Output, error) { return snapshot, nil }

	// leaks lists the sentinels no unguarded health body may echo.
	leaks := []string{
		fmt.Sprintf("%d", sentinelPID), // the server PID
		sentinelRoot,                   // a workspace root
		"/srv/another/workspace",       // a second workspace root
		"/",                            // any absolute-path fragment at all
	}
	assertNoLeak := func(t *testing.T, body string) {
		for _, s := range leaks {
			assert.NotContains(t, body, s, "unguarded health body leaked %q", s)
		}
	}

	t.Run("livez", func(t *testing.T) {
		rec := httptest.NewRecorder()
		healthHTTPHandler(probeLiveness, statusFn)(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		assert.Equal(t, http.StatusOK, rec.Code)
		assertNoLeak(t, rec.Body.String())
	})

	t.Run("healthz", func(t *testing.T) {
		// /healthz is wired to the same handler as /livez (probeLiveness).
		rec := httptest.NewRecorder()
		healthHTTPHandler(probeLiveness, statusFn)(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		assert.Equal(t, http.StatusOK, rec.Code)
		assertNoLeak(t, rec.Body.String())
	})

	t.Run("readyz", func(t *testing.T) {
		extra := readinessExtras{
			symbolIndexes: func(context.Context) []types.SymbolIndexStatus {
				return []types.SymbolIndexStatus{{Freshness: types.SymbolIndexFresh}}
			},
			services:       func() []daemon.Service { return []daemon.Service{{State: "running"}} },
			knowledgeGraph: func() (bool, bool) { return true, true },
		}
		rec := httptest.NewRecorder()
		readinessHTTPHandler(statusFn, extra)(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		assert.Equal(t, http.StatusOK, rec.Code)
		// Counts and generic state phrases are wanted in the component details; only
		// identifying content (the sentinels above, any path fragment) must be absent.
		assertNoLeak(t, rec.Body.String())
	})
}

func TestWorkspacesComponent(t *testing.T) {
	cases := []struct {
		name     string
		snapshot *daemon.Output
		want     daemon.ReadinessComponent
	}{
		{"nil-snapshot", nil, daemon.ReadinessComponent{Name: "workspaces", Status: "down", Detail: "server unreachable"}},
		{"no-workspaces", &daemon.Output{}, daemon.ReadinessComponent{Name: "workspaces", Status: "down", Detail: "no workspaces loaded"}},
		// Two workspaces with recognizable roots: the count is wanted in Detail, but the
		// roots themselves must NOT leak into it on this unguarded endpoint.
		{"two-workspaces", &daemon.Output{Workspaces: []daemon.Workspace{{Root: "/a"}, {Root: "/b"}}}, daemon.ReadinessComponent{Name: "workspaces", Status: "ok", Detail: "2 loaded"}},
		{"one-failed", &daemon.Output{Workspaces: []daemon.Workspace{{Root: "/a", State: daemon.WorkspaceActive}, {Root: "/b", State: daemon.WorkspaceFailed}}}, daemon.ReadinessComponent{Name: "workspaces", Status: "degraded", Detail: "1 loaded, 1 failed to load"}},
		{"all-failed", &daemon.Output{Workspaces: []daemon.Workspace{{Root: "/b", State: daemon.WorkspaceFailed}}}, daemon.ReadinessComponent{Name: "workspaces", Status: "down", Detail: "1 failed to load"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, workspacesComponent(c.snapshot))
		})
	}
}

func TestSymbolIndexComponent(t *testing.T) {
	cases := []struct {
		name    string
		indexes []types.SymbolIndexStatus
		want    daemon.ReadinessComponent
	}{
		{"none", nil, daemon.ReadinessComponent{Name: "symbol_index", Status: "disabled", Detail: "no symbol-capable project"}},
		{"all-fresh", []types.SymbolIndexStatus{{Freshness: types.SymbolIndexFresh}, {Freshness: types.SymbolIndexFresh}}, daemon.ReadinessComponent{Name: "symbol_index", Status: "ok", Detail: "2 of 2 up to date"}},
		{"one-stale-one-not-built", []types.SymbolIndexStatus{{Freshness: types.SymbolIndexFresh}, {Freshness: types.SymbolIndexStale}, {Freshness: types.SymbolIndexNotBuilt}}, daemon.ReadinessComponent{Name: "symbol_index", Status: "degraded", Detail: "1 of 3 up to date"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, symbolIndexComponent(c.indexes))
		})
	}
}

func TestServicesComponent(t *testing.T) {
	cases := []struct {
		name     string
		services []daemon.Service
		want     daemon.ReadinessComponent
	}{
		{"none", nil, daemon.ReadinessComponent{Name: "services", Status: "disabled", Detail: "no hosted services"}},
		{"all-running", []daemon.Service{{State: "running"}, {State: "idle"}}, daemon.ReadinessComponent{Name: "services", Status: "ok", Detail: "2 running, 0 failed"}},
		{"some-failed", []daemon.Service{{State: "running"}, {State: "failed"}}, daemon.ReadinessComponent{Name: "services", Status: "degraded", Detail: "1 running, 1 failed"}},
		{"all-failed", []daemon.Service{{State: "failed"}, {State: "failed"}}, daemon.ReadinessComponent{Name: "services", Status: "down", Detail: "0 running, 2 failed"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, servicesComponent(c.services))
		})
	}
}

func TestKnowledgeGraphComponent(t *testing.T) {
	cases := []struct {
		name     string
		watching bool
		valid    bool
		want     daemon.ReadinessComponent
	}{
		{"watching-and-valid", true, true, daemon.ReadinessComponent{Name: "knowledge_graph", Status: "ok", Detail: "watcher active, graph fresh"}},
		{"watching-not-valid", true, false, daemon.ReadinessComponent{Name: "knowledge_graph", Status: "degraded", Detail: "watcher active, graph rebuilding"}},
		{"not-watching", false, false, daemon.ReadinessComponent{Name: "knowledge_graph", Status: "down", Detail: "no watcher; falling back to cache-first rebuild per query"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, knowledgeGraphComponent(c.watching, c.valid))
		})
	}
}

// TestReadinessHTTPHandler verifies /readyz's JSON body: the whole daemon.ReadinessReport
// (Ready plus every component) for a healthy snapshot with all extras wired, for an
// unhealthy one, and for the nil-extras case (no source wired degrades every extra
// component to disabled/down rather than erroring).
func TestReadinessHTTPHandler(t *testing.T) {
	statusOf := func(workspaces ...string) statusFunc {
		reply := makeReply(1, workspaces...)
		return func(context.Context) (*daemon.Output, error) {
			return reply.StatusOutput(), nil
		}
	}
	do := func(h http.HandlerFunc) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		return rec
	}

	t.Run("ready-with-all-extras-healthy", func(t *testing.T) {
		extra := readinessExtras{
			symbolIndexes: func(context.Context) []types.SymbolIndexStatus {
				return []types.SymbolIndexStatus{{Freshness: types.SymbolIndexFresh}}
			},
			services: func() []daemon.Service {
				return []daemon.Service{{State: "running"}}
			},
			knowledgeGraph: func() (bool, bool) { return true, true },
		}
		rec := do(readinessHTTPHandler(statusOf("/ws"), extra))

		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, "application/json", rec.Header().Get("Content-Type"))

		var report daemon.ReadinessReport
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &report))
		require.Equal(t, daemon.ReadinessReport{
			Ready: true,
			Components: []daemon.ReadinessComponent{
				{Name: "workspaces", Status: "ok", Detail: "1 loaded"},
				{Name: "symbol_index", Status: "ok", Detail: "1 of 1 up to date"},
				{Name: "services", Status: "ok", Detail: "1 running, 0 failed"},
				{Name: "knowledge_graph", Status: "ok", Detail: "watcher active, graph fresh"},
			},
		}, report)
	})

	t.Run("not-ready-despite-healthy-extras", func(t *testing.T) {
		// No workspaces loaded fails the gate even though every extra component
		// reports healthy: the gate is workspace-loaded alone, unchanged by this body.
		extra := readinessExtras{knowledgeGraph: func() (bool, bool) { return true, true }}
		rec := do(readinessHTTPHandler(statusOf(), extra))
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)

		var report daemon.ReadinessReport
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &report))
		require.False(t, report.Ready)
	})

	t.Run("nil-extras-degrade-not-error", func(t *testing.T) {
		rec := do(readinessHTTPHandler(statusOf("/ws"), readinessExtras{}))
		require.Equal(t, http.StatusOK, rec.Code)

		var report daemon.ReadinessReport
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &report))
		require.Equal(t, daemon.ReadinessReport{
			Ready: true,
			Components: []daemon.ReadinessComponent{
				{Name: "workspaces", Status: "ok", Detail: "1 loaded"},
				{Name: "symbol_index", Status: "disabled", Detail: "no symbol-capable project"},
				{Name: "services", Status: "disabled", Detail: "no hosted services"},
				{Name: "knowledge_graph", Status: "down", Detail: "no watcher; falling back to cache-first rebuild per query"},
			},
		}, report)
	})
}

// TestReadinessHTTPHandlerMatchesHealthHTTPHandlerGate is the hard-constraint check: the
// JSON body must ride ALONGSIDE the existing pass/fail gate, never change it. It runs the
// old plain-text handler and the new JSON handler against identical snapshots and asserts
// their status codes never diverge, across both the ready and not-ready cases.
func TestReadinessHTTPHandlerMatchesHealthHTTPHandlerGate(t *testing.T) {
	scenarios := []struct {
		name       string
		workspaces []string
	}{
		{"no-workspaces", nil},
		{"one-workspace", []string{"/ws"}},
		{"two-workspaces", []string{"/ws", "/ws2"}},
	}
	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			statusFn := func(context.Context) (*daemon.Output, error) {
				return makeReply(1, s.workspaces...).StatusOutput(), nil
			}

			oldRec := httptest.NewRecorder()
			healthHTTPHandler(probeReadiness, statusFn)(oldRec, httptest.NewRequest(http.MethodGet, "/", nil))

			newRec := httptest.NewRecorder()
			readinessHTTPHandler(statusFn, readinessExtras{})(newRec, httptest.NewRequest(http.MethodGet, "/", nil))

			require.Equal(t, oldRec.Code, newRec.Code, "the /readyz JSON body must not change the pass/fail gate")
		})
	}
}

func TestProbeMCPReadiness(t *testing.T) {
	t.Run("ready-200", func(t *testing.T) {
		assert.Equal(t, http.StatusOK, probeMCPReadiness(context.Background(), readyzServer(t, http.StatusOK)))
	})
	t.Run("not-ready-503", func(t *testing.T) {
		assert.Equal(t, http.StatusServiceUnavailable, probeMCPReadiness(context.Background(), readyzServer(t, http.StatusServiceUnavailable)))
	})
	t.Run("answered-other-status-reads-as-ok", func(t *testing.T) {
		// An older server without /readyz still proves a listener is up: any answered
		// status collapses to OK so the endpoint reads as reachable.
		assert.Equal(t, http.StatusOK, probeMCPReadiness(context.Background(), readyzServer(t, http.StatusTeapot)))
	})
	t.Run("nothing-listening-returns-zero", func(t *testing.T) {
		// Reserved-for-docs TEST-NET-1 address that refuses fast; 0 == unreachable.
		assert.Equal(t, 0, probeMCPReadiness(context.Background(), "127.0.0.1:1"))
	})
}
func TestMCPAddress(t *testing.T) {
	assert.Equal(t, "127.0.0.1:9000", MCPAddress(config.MCP{Address: "127.0.0.1:9000"}))
	assert.Equal(t, internalmcp.DefaultAddress, MCPAddress(config.MCP{}), "empty address falls back to the default")
}
func TestBuildMCPEndpointStatus(t *testing.T) {
	ctx := context.Background()
	t.Run("disabled", func(t *testing.T) {
		got := BuildMCPEndpointStatus(ctx, mcpDisabled())
		require.NotNil(t, got)
		assert.False(t, got.Enabled)
		assert.Equal(t, "disabled", got.State)
		assert.False(t, got.Reachable)
	})
	t.Run("serving", func(t *testing.T) {
		got := BuildMCPEndpointStatus(ctx, mcpServing(t, http.StatusOK))
		require.NotNil(t, got)
		assert.True(t, got.Enabled)
		assert.True(t, got.Reachable)
		assert.Equal(t, "serving", got.State)
		assert.Contains(t, got.URL, "/mcp")
	})
	t.Run("not-ready", func(t *testing.T) {
		got := BuildMCPEndpointStatus(ctx, mcpServing(t, http.StatusServiceUnavailable))
		require.NotNil(t, got)
		assert.True(t, got.Reachable)
		assert.Equal(t, "not-ready", got.State)
	})
	t.Run("unreachable-points-at-server-start", func(t *testing.T) {
		got := BuildMCPEndpointStatus(ctx, mcpUnreachable())
		require.NotNil(t, got)
		assert.False(t, got.Reachable)
		assert.Equal(t, "unreachable", got.State)
		assert.Contains(t, got.Note, "magus server start")
	})
}
func TestParseProbeKind(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want ProbeKind
	}{
		{"liveness", probeLiveness},
		{"readiness", probeReadiness},
		{"mcp", probeMCP},
	} {
		kind, err := parseProbeKind(tc.in)
		require.NoError(t, err, tc.in)
		assert.Equal(t, tc.want, kind, tc.in)
	}
	_, err := parseProbeKind("bogus")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mcp")
}
func TestProbeName(t *testing.T) {
	assert.Equal(t, "liveness", probeName(probeLiveness))
	assert.Equal(t, "readiness", probeName(probeReadiness))
	assert.Equal(t, "mcp", probeName(probeMCP))
	assert.Equal(t, "unknown", probeName(ProbeKind(99)))
}
func TestParseProbeKinds(t *testing.T) {
	t.Run("single", func(t *testing.T) {
		kinds, err := ParseProbeKinds("liveness")
		require.NoError(t, err)
		assert.Equal(t, []ProbeKind{probeLiveness}, kinds)
	})
	t.Run("comma-combined-in-order", func(t *testing.T) {
		kinds, err := ParseProbeKinds("liveness,mcp")
		require.NoError(t, err)
		assert.Equal(t, []ProbeKind{probeLiveness, probeMCP}, kinds)
	})
	t.Run("trims-and-tolerates-empty-segments", func(t *testing.T) {
		kinds, err := ParseProbeKinds(" mcp , ,liveness,")
		require.NoError(t, err)
		assert.Equal(t, []ProbeKind{probeMCP, probeLiveness}, kinds)
	})
	t.Run("dedupes", func(t *testing.T) {
		kinds, err := ParseProbeKinds("mcp,mcp")
		require.NoError(t, err)
		assert.Equal(t, []ProbeKind{probeMCP}, kinds)
	})
	t.Run("unknown-errors", func(t *testing.T) {
		_, err := ParseProbeKinds("liveness,bogus")
		require.Error(t, err)
	})
	t.Run("all-empty-errors", func(t *testing.T) {
		_, err := ParseProbeKinds(" , ,")
		require.Error(t, err)
	})
}
func TestEvaluateMCPHealth(t *testing.T) {
	t.Run("serving-passes", func(t *testing.T) {
		ok, reason := evaluateMCPHealth(&daemon.MCPEndpoint{Reachable: true, State: "serving", URL: "http://127.0.0.1:7391/mcp"})
		assert.True(t, ok)
		assert.Contains(t, reason, "serving")
	})
	t.Run("not-ready-passes", func(t *testing.T) {
		// The endpoint is up; a liveness/ensure check should not restart the server.
		ok, _ := evaluateMCPHealth(&daemon.MCPEndpoint{Reachable: true, State: "not-ready", URL: "http://127.0.0.1:7391/mcp"})
		assert.True(t, ok)
	})
	t.Run("unreachable-fails-with-note", func(t *testing.T) {
		ok, reason := evaluateMCPHealth(&daemon.MCPEndpoint{State: "unreachable", Note: "start the server: magus server start"})
		assert.False(t, ok)
		assert.Contains(t, reason, "magus server start")
	})
	t.Run("disabled-fails", func(t *testing.T) {
		ok, reason := evaluateMCPHealth(&daemon.MCPEndpoint{State: "disabled", Note: "MCP is disabled (mcp.enabled=false); no agent tools are served."})
		assert.False(t, ok)
		assert.Contains(t, reason, "disabled")
	})
	t.Run("nil-fails", func(t *testing.T) {
		ok, _ := evaluateMCPHealth(nil)
		assert.False(t, ok)
	})
	t.Run("unreachable-without-note-falls-back-to-state", func(t *testing.T) {
		ok, reason := evaluateMCPHealth(&daemon.MCPEndpoint{State: "unreachable"})
		assert.False(t, ok)
		assert.Equal(t, "mcp endpoint unreachable", reason)
	})
}
func TestEvaluateProbes(t *testing.T) {
	ctx := context.Background()
	aliveServer := &daemon.Output{ParentPID: 42, Workspaces: []daemon.Workspace{{Root: "/ws"}}}

	t.Run("liveness-alone-passes-and-dials-once", func(t *testing.T) {
		calls := 0
		res := evaluateProbes(ctx, recordingStatus(&calls, aliveServer, nil), config.MCP{}, []ProbeKind{probeLiveness}, "")
		require.Len(t, res, 1)
		assert.True(t, res[0].ok)
		assert.Equal(t, 1, calls)
	})
	t.Run("two-socket-probes-dial-once", func(t *testing.T) {
		calls := 0
		res := evaluateProbes(ctx, recordingStatus(&calls, aliveServer, nil), config.MCP{}, []ProbeKind{probeLiveness, probeReadiness}, "")
		require.Len(t, res, 2)
		assert.True(t, res[0].ok)
		assert.True(t, res[1].ok)
		assert.Equal(t, 1, calls, "the server snapshot is fetched once and reused")
	})
	t.Run("mcp-only-never-dials-the-server", func(t *testing.T) {
		calls := 0
		res := evaluateProbes(ctx, recordingStatus(&calls, nil, errors.New("must not be called")), mcpServing(t, http.StatusOK), []ProbeKind{probeMCP}, "")
		require.Len(t, res, 1)
		assert.True(t, res[0].ok)
		assert.Equal(t, 0, calls, "an mcp-only probe makes no proc RPC")
	})
	t.Run("combined-liveness-and-mcp-both-evaluated", func(t *testing.T) {
		calls := 0
		res := evaluateProbes(ctx, recordingStatus(&calls, aliveServer, nil), mcpUnreachable(), []ProbeKind{probeLiveness, probeMCP}, "")
		require.Len(t, res, 2)
		assert.True(t, res[0].ok, "server is alive")
		assert.False(t, res[1].ok, "mcp endpoint is down")
		assert.Equal(t, probeMCP, res[1].kind)
	})
}
func TestRunProbesMCPOnly(t *testing.T) {
	ctx := context.Background()
	t.Run("serving-returns-nil", func(t *testing.T) {
		// socket "" is never dialed for an mcp-only probe, so this makes no proc RPC.
		require.NoError(t, RunProbes(ctx, "", mcpServing(t, http.StatusOK), []ProbeKind{probeMCP}, ""))
	})
	t.Run("unreachable-exits-1", func(t *testing.T) {
		err := RunProbes(ctx, "", mcpUnreachable(), []ProbeKind{probeMCP}, "")
		require.Error(t, err)
		var silent interface{ ExitCode() int }
		require.ErrorAs(t, err, &silent)
		assert.Equal(t, 1, silent.ExitCode())
	})
}
func TestRenderProbeResults(t *testing.T) {
	render := func(results []probeResult) (string, string, bool) {
		var out strings.Builder
		var ok bool
		errb := notices(t, func() { ok = renderProbeResults(t.Context(), &out, results) })
		return out.String(), errb, ok
	}

	t.Run("single-pass-no-label", func(t *testing.T) {
		out, errb, ok := render([]probeResult{{kind: probeLiveness, ok: true, reason: "server pid 42 is alive"}})
		assert.True(t, ok)
		assert.Equal(t, "ok: server pid 42 is alive\n", out)
		assert.Empty(t, errb)
	})
	t.Run("single-fail-exit-signal", func(t *testing.T) {
		out, errb, ok := render([]probeResult{{kind: probeMCP, ok: false, reason: "unreachable"}})
		assert.False(t, ok)
		assert.Empty(t, out)
		assert.Contains(t, errb, "unreachable")
	})
	t.Run("multi-labels-each-and-fails-if-any-fails", func(t *testing.T) {
		out, errb, ok := render([]probeResult{
			{kind: probeLiveness, ok: true, reason: "alive"},
			{kind: probeMCP, ok: false, reason: "down"},
		})
		assert.False(t, ok)
		assert.Contains(t, out, "ok: liveness: alive")
		assert.Contains(t, errb, "mcp: down")
	})
	t.Run("multi-all-pass", func(t *testing.T) {
		_, _, ok := render([]probeResult{
			{kind: probeLiveness, ok: true, reason: "alive"},
			{kind: probeMCP, ok: true, reason: "serving"},
		})
		assert.True(t, ok)
	})
}

// mcpUnreachable returns an enabled MCP config whose address nothing listens on.
func mcpUnreachable() config.MCP {
	return config.MCP{Address: "127.0.0.1:1", Enabled: new(true)}
}

// mcpDisabled returns an MCP config with the server turned off.
func mcpDisabled() config.MCP {
	return config.MCP{Enabled: new(false)}
}

// recordingStatus is a statusFunc that counts calls and returns a fixed snapshot/err, so
// tests can assert how many times (if at all) the server socket was dialed.
func recordingStatus(calls *int, out *daemon.Output, err error) statusFunc {
	return func(context.Context) (*daemon.Output, error) {
		*calls++
		return out, err
	}
}

// notices returns what the pretty display prints for the records fn logs through the
// default logger, the way a notice reaches a person now that it is a record.
func notices(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(cache.NewLogHandler(&buf, "pretty", &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)
	fn()
	return buf.String()
}

// readyzServer stands up an httptest server whose /readyz returns code, and returns
// its host:port (the form mcp.address takes). Other paths 404, proving the probe keys
// on /readyz specifically.
func readyzServer(t *testing.T, code int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/readyz" {
			w.WriteHeader(code)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

// mcpServing returns an MCP config enabled and pointing at a live endpoint whose /readyz
// returns code (200 serving, 503 not-ready). mcpUnreachable and mcpDisabled are the other
// two states. They build an explicit config to pass in, so no test mutates package state.
func mcpServing(t *testing.T, code int) config.MCP {
	return config.MCP{Address: readyzServer(t, code), Enabled: new(true)}
}
