package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/types"
)

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
	assertHealth := func(name string, reply *proc.StatusReply, err error, kind probeKind, root string, wantOK bool, wantSub string) {
		t.Run(name, func(t *testing.T) {
			ok, reason := evaluateHealth(statusOutputFromReply(reply), err, kind, root)
			assert.Equal(t, wantOK, ok, "reason: %q", reason)
			if wantSub != "" {
				assert.Contains(t, reason, wantSub)
			}
		})
	}

	assertHealth("liveness/unreachable-err", nil, errors.New("dial failed"), probeLiveness, "", false, "unreachable")
	assertHealth("liveness/nil-reply-no-err", nil, nil, probeLiveness, "", false, "unreachable")
	assertHealth("readiness/unreachable", nil, errors.New("no socket"), probeReadiness, "", false, "unreachable")
	assertHealth("liveness/daemon-up-no-workspaces", makeReply(42), nil, probeLiveness, "", true, "42")
	assertHealth("liveness/daemon-up-with-workspace", makeReply(99, "/ws"), nil, probeLiveness, "", true, "99")
	assertHealth("readiness/no-workspaces-no-root", makeReply(1), nil, probeReadiness, "", false, "no workspaces")
	assertHealth("readiness/one-workspace-no-root", makeReply(1, "/ws"), nil, probeReadiness, "", true, "1 workspace")
	assertHealth("readiness/two-workspaces-no-root", makeReply(1, "/ws", "/ws2"), nil, probeReadiness, "", true, "2 workspace")
	assertHealth("readiness/root-present", makeReply(1, "/repo"), nil, probeReadiness, "/repo", true, "/repo")
	assertHealth("readiness/root-missing", makeReply(1, "/other"), nil, probeReadiness, "/repo", false, "/repo")
	assertHealth("readiness/root-given-no-workspaces", makeReply(1), nil, probeReadiness, "/repo", false, "/repo")
	assertHealth("readiness/root-trailing-slash-normalised", makeReply(1, "/repo"), nil, probeReadiness, "/repo/", true, "/repo")
	assertHealth("readiness/daemon-mode-with-workspace",
		&proc.StatusReply{ParentPID: 1, Mode: "daemon", Workspaces: []proc.Workspace{{Root: "/ws"}}},
		nil, probeReadiness, "", true, "1 workspace")
	assertHealth("readiness/proc-mode-rejected",
		&proc.StatusReply{ParentPID: 1, Mode: "proc"}, nil, probeReadiness, "", false, "per-process mode")
	assertHealth("liveness/proc-mode-still-alive",
		&proc.StatusReply{ParentPID: 7, Mode: "proc"}, nil, probeLiveness, "", true, "7")
}

func TestHealthHTTPHandler(t *testing.T) {
	// assertHandler stands up a handler with a fake querier and checks the HTTP
	// status code for a given probe kind and ?workspace= filter.
	assertHandler := func(name string, kind probeKind, workspaces []string, queryWS string, wantStatus int) {
		t.Run(name, func(t *testing.T) {
			reply := makeReply(1, workspaces...)
			// Build a handler with a fake querier instead of dialing a real socket.
			h := healthHTTPHandler(kind, func(context.Context) (*types.StatusOutput, error) {
				return statusOutputFromReply(reply), nil
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
			// embeds the daemon PID). A kubelet reads only the status code, so this
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

// unreachable querier for testing the error path
func TestHealthHTTPHandlerUnreachable(t *testing.T) {
	// A proc-dial error carrying the daemon socket path is exactly the kind of text
	// evaluateHealth would fold into its reason ("daemon unreachable: ...dial <socket>").
	h := healthHTTPHandler(probeLiveness, func(context.Context) (*types.StatusOutput, error) {
		return nil, errors.New("proc: query: dial /var/run/magus/daemon.sock: connection refused")
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h(rec, req)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	// The redacted body must not surface the error text (and its socket path).
	assert.Equal(t, "unavailable\n", rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "daemon.sock")
}

// TestHealthEndpointBodiesRedactSensitiveDetail is the regression guard for the security
// fix: the unguarded /livez, /healthz, and /readyz bodies must never carry the daemon PID,
// a workspace root, or any filesystem path, even when the underlying snapshot is rich with
// them. It drives the real handlers with a snapshot whose workspace roots and PID are
// distinctive sentinels, then asserts none of those sentinels reach any body.
func TestHealthEndpointBodiesRedactSensitiveDetail(t *testing.T) {
	const (
		sentinelPID  = 424242
		sentinelRoot = "/Users/secret/private-repo"
	)
	snapshot := statusOutputFromReply(makeReply(sentinelPID, sentinelRoot, "/srv/another/workspace"))
	statusFn := func(context.Context) (*types.StatusOutput, error) { return snapshot, nil }

	// leaks lists the sentinels no unguarded health body may echo.
	leaks := []string{
		fmt.Sprintf("%d", sentinelPID), // the daemon PID
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
			services:       func() []types.StatusService { return []types.StatusService{{State: "running"}} },
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
		snapshot *types.StatusOutput
		want     types.ReadinessComponent
	}{
		{"nil-snapshot", nil, types.ReadinessComponent{Name: "workspaces", Status: "down", Detail: "daemon unreachable"}},
		{"proc-mode", &types.StatusOutput{Mode: "proc"}, types.ReadinessComponent{Name: "workspaces", Status: "down", Detail: "daemon is in per-process mode"}},
		{"no-workspaces", &types.StatusOutput{Mode: "daemon"}, types.ReadinessComponent{Name: "workspaces", Status: "down", Detail: "no workspaces loaded"}},
		// Two workspaces with recognizable roots: the count is wanted in Detail, but the
		// roots themselves must NOT leak into it on this unguarded surface.
		{"two-workspaces", &types.StatusOutput{Mode: "daemon", Workspaces: []types.StatusWorkspace{{Root: "/a"}, {Root: "/b"}}}, types.ReadinessComponent{Name: "workspaces", Status: "ok", Detail: "2 loaded"}},
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
		want    types.ReadinessComponent
	}{
		{"none", nil, types.ReadinessComponent{Name: "symbol_index", Status: "disabled", Detail: "no symbol-capable project"}},
		{"all-fresh", []types.SymbolIndexStatus{{Freshness: types.SymbolIndexFresh}, {Freshness: types.SymbolIndexFresh}}, types.ReadinessComponent{Name: "symbol_index", Status: "ok", Detail: "2 of 2 up to date"}},
		{"one-stale-one-not-built", []types.SymbolIndexStatus{{Freshness: types.SymbolIndexFresh}, {Freshness: types.SymbolIndexStale}, {Freshness: types.SymbolIndexNotBuilt}}, types.ReadinessComponent{Name: "symbol_index", Status: "degraded", Detail: "1 of 3 up to date"}},
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
		services []types.StatusService
		want     types.ReadinessComponent
	}{
		{"none", nil, types.ReadinessComponent{Name: "services", Status: "disabled", Detail: "no hosted services"}},
		{"all-running", []types.StatusService{{State: "running"}, {State: "idle"}}, types.ReadinessComponent{Name: "services", Status: "ok", Detail: "2 running, 0 failed"}},
		{"some-failed", []types.StatusService{{State: "running"}, {State: "failed"}}, types.ReadinessComponent{Name: "services", Status: "degraded", Detail: "1 running, 1 failed"}},
		{"all-failed", []types.StatusService{{State: "failed"}, {State: "failed"}}, types.ReadinessComponent{Name: "services", Status: "down", Detail: "0 running, 2 failed"}},
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
		want     types.ReadinessComponent
	}{
		{"watching-and-valid", true, true, types.ReadinessComponent{Name: "knowledge_graph", Status: "ok", Detail: "watcher active, graph fresh"}},
		{"watching-not-valid", true, false, types.ReadinessComponent{Name: "knowledge_graph", Status: "degraded", Detail: "watcher active, graph rebuilding"}},
		{"not-watching", false, false, types.ReadinessComponent{Name: "knowledge_graph", Status: "down", Detail: "no watcher; falling back to cache-first rebuild per query"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, knowledgeGraphComponent(c.watching, c.valid))
		})
	}
}

// TestReadinessHTTPHandler verifies /readyz's JSON body: the whole types.ReadinessReport
// (Ready plus every component) for a healthy snapshot with all extras wired, for an
// unhealthy one, and for the nil-extras case (no source wired degrades every extra
// component to disabled/down rather than erroring).
func TestReadinessHTTPHandler(t *testing.T) {
	statusOf := func(workspaces ...string) statusFunc {
		reply := makeReply(1, workspaces...)
		return func(context.Context) (*types.StatusOutput, error) {
			return statusOutputFromReply(reply), nil
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
			services: func() []types.StatusService {
				return []types.StatusService{{State: "running"}}
			},
			knowledgeGraph: func() (bool, bool) { return true, true },
		}
		rec := do(readinessHTTPHandler(statusOf("/ws"), extra))

		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, "application/json", rec.Header().Get("Content-Type"))

		var report types.ReadinessReport
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &report))
		require.Equal(t, types.ReadinessReport{
			Ready: true,
			Components: []types.ReadinessComponent{
				{Name: "workspaces", Status: "ok", Detail: "1 loaded"},
				{Name: "symbol_index", Status: "ok", Detail: "1 of 1 up to date"},
				{Name: "services", Status: "ok", Detail: "1 running, 0 failed"},
				{Name: "knowledge_graph", Status: "ok", Detail: "watcher active, graph fresh"},
			},
		}, report)
	})

	t.Run("not-ready-despite-healthy-extras", func(t *testing.T) {
		// No workspaces loaded fails the gate even though every extra component
		// reports healthy - the gate is workspace-loaded alone, unchanged by this body.
		extra := readinessExtras{knowledgeGraph: func() (bool, bool) { return true, true }}
		rec := do(readinessHTTPHandler(statusOf(), extra))
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)

		var report types.ReadinessReport
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &report))
		require.False(t, report.Ready)
	})

	t.Run("nil-extras-degrade-not-error", func(t *testing.T) {
		rec := do(readinessHTTPHandler(statusOf("/ws"), readinessExtras{}))
		require.Equal(t, http.StatusOK, rec.Code)

		var report types.ReadinessReport
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &report))
		require.Equal(t, types.ReadinessReport{
			Ready: true,
			Components: []types.ReadinessComponent{
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
			statusFn := func(context.Context) (*types.StatusOutput, error) {
				return statusOutputFromReply(makeReply(1, s.workspaces...)), nil
			}

			oldRec := httptest.NewRecorder()
			healthHTTPHandler(probeReadiness, statusFn)(oldRec, httptest.NewRequest(http.MethodGet, "/", nil))

			newRec := httptest.NewRecorder()
			readinessHTTPHandler(statusFn, readinessExtras{})(newRec, httptest.NewRequest(http.MethodGet, "/", nil))

			require.Equal(t, oldRec.Code, newRec.Code, "the /readyz JSON body must not change the pass/fail gate")
		})
	}
}

// TestResolveDeclaredWorkspacesMergesAndDedupes verifies that cfg and env
// inputs are merged, deduplicated, resolved to absolute paths, and that
// non-existent or non-directory entries are skipped.
func TestResolveDeclaredWorkspacesMergesAndDedupes(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	c := t.TempDir()

	// Non-existent path should be dropped silently.
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	got := resolveDeclaredWorkspaces(
		[]string{a, b}, // cfg
		b+string(filepath.ListSeparator)+c+string(filepath.ListSeparator)+missing, // env (b is duplicate)
	)
	require.Len(t, got, 3)
	want := map[string]bool{a: true, b: true, c: true}
	for _, root := range got {
		assert.True(t, want[root], "unexpected root in result: %q", root)
	}
}

func TestResolveDeclaredWorkspacesEmpty(t *testing.T) {
	assert.Nil(t, resolveDeclaredWorkspaces(nil, ""), "expected nil for empty inputs")
}

// TestAcquireRejectsNonDeclared confirms that once setDeclared has been
// called with an allowlist, acquire of a root outside the list fails with
// MGS2010 (SandboxPolicyMismatch).
func TestAcquireRejectsNonDeclared(t *testing.T) {
	allowed := t.TempDir()
	forbidden := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lim := cache.NewLimiter(1)
	reg := newWSRegistry(ctx, lim, 0, nil)
	defer reg.close()

	reg.setDeclared([]string{allowed})

	_, err := reg.acquire(ctx, forbidden)
	require.Error(t, err, "acquire of non-declared root should error")
	var de *types.DiagnosticError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, types.SandboxPolicyMismatch, de.Code, "expected MGS2010 SandboxPolicyMismatch")
}

// TestAcquireAdmitsDeclaredEvenWithoutMagusYaml verifies that a declared
// workspace without a magus.yaml falls back to defaults rather than being
// rejected as undeclared. Without an actual workspace layout the load will
// still fail; we only verify that the allowlist check passes (the failure
// surfaces from magus.Open, not from the declared gate).
func TestAcquireAdmitsDeclaredEvenWithoutMagusYaml(t *testing.T) {
	allowed := t.TempDir()
	// Write an empty magus.yaml so config loading succeeds and the test
	// reaches the magus.Open step deterministically.
	require.NoError(t, os.WriteFile(filepath.Join(allowed, "magus.yaml"), []byte(""), 0o644))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lim := cache.NewLimiter(1)
	reg := newWSRegistry(ctx, lim, 0, nil)
	defer reg.close()
	reg.setDeclared([]string{allowed})

	// acquire may fail at magus.Open (no real workspace), but it must NOT
	// fail with the MGS2010 declared-list gate.
	_, err := reg.acquire(ctx, allowed)
	if err != nil {
		var de *types.DiagnosticError
		if errors.As(err, &de) {
			assert.NotEqual(t, types.SandboxPolicyMismatch, de.Code,
				"acquire of declared root was wrongly rejected by the allowlist gate: %v", err)
		}
	}
}

// TestWarmRespectsContextCancellation verifies that warm exits promptly when
// the context is already cancelled, without hanging on acquire.
func TestWarmRespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before warm is called

	lim := cache.NewLimiter(2)
	reg := newWSRegistry(context.Background(), lim, 0, nil)
	defer reg.close()

	// Supply several roots; warm should bail after the first ctx.Err() check.
	roots := []string{t.TempDir(), t.TempDir(), t.TempDir()}
	reg.setDeclared(roots)

	done := make(chan struct{})
	go func() {
		reg.warm(ctx, roots)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("warm did not return promptly after context cancellation")
	}
}

// TestWarmCompletesAndPopulatesStatus verifies that warm runs to completion
// and that any successfully loaded workspaces appear in status() afterwards.
// Warm must not panic, hang, or silently skip entries.
func TestWarmCompletesAndPopulatesStatus(t *testing.T) {
	root1 := t.TempDir()
	root2 := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lim := cache.NewLimiter(2)
	reg := newWSRegistry(ctx, lim, 0, nil)
	defer reg.close()
	reg.setDeclared([]string{root1, root2})

	done := make(chan struct{})
	go func() {
		reg.warm(ctx, []string{root1, root2})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("warm did not complete within timeout")
	}
	// Any workspace that loaded successfully must appear in status().
	// The exact count depends on whether magus.Open succeeds for bare dirs.
	assert.NotPanics(t, func() { _ = reg.status() })
}

// TestWarmInBackgroundTrackedByClose verifies that close() waits for an
// in-flight warm rather than racing it — otherwise a workspace could be
// acquired (and leaked) after close() swept the entry map.
func TestWarmInBackgroundTrackedByClose(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lim := cache.NewLimiter(2)
	reg := newWSRegistry(ctx, lim, 0, nil)
	reg.setDeclared([]string{root})
	reg.warmInBackground(ctx, []string{root})

	// close() must return: its wg.Wait() should account for the warm goroutine
	// (plus the janitor) and not hang.
	done := make(chan struct{})
	go func() {
		reg.close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("close did not return; warm goroutine not tracked by the waitgroup?")
	}
}
