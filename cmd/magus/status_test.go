package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
	internalmcp "github.com/egladman/magus/internal/handler/mcp"
	"github.com/egladman/magus/types"
)

func TestPrintStatusCompact(t *testing.T) {
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	at := func(ago time.Duration) time.Time { return now.Add(-ago) }

	assertCompact := func(name string, report statusReport, want string) {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			printStatusCompact(&buf, report, now)
			assert.Equal(t, want, buf.String())
			assert.Equal(t, 1, strings.Count(buf.String(), "\n"), "compact must emit exactly one line")
		})
	}

	assertCompact("no parent",
		statusReport{PoolError: "no running magus proc server found"},
		"daemon: off\n")

	assertCompact("daemon idle",
		statusReport{Pool: &types.StatusOutput{
			Mode: "daemon", Capacity: 8, Running: 0,
		}},
		"daemon · 0/8 idle\n")

	assertCompact("proc-server label",
		statusReport{Pool: &types.StatusOutput{
			Mode: "proc", Capacity: 8, Running: 1,
			RunningTargets: []types.StatusRunningTarget{
				{Args: []string{"test", "web"}, Workspace: "/w", StartedAt: at(400 * time.Millisecond)},
			},
		}},
		"pool · 1/8 running · web:test(0.4s)\n")

	assertCompact("daemon running with targets, sorted oldest first",
		statusReport{Pool: &types.StatusOutput{
			Mode: "daemon", Capacity: 8, Running: 3,
			RunningTargets: []types.StatusRunningTarget{
				{Args: []string{"test", "ui"}, Workspace: "/w", StartedAt: at(500 * time.Millisecond)},
				{Args: []string{"build", "api"}, Workspace: "/w", StartedAt: at(2100 * time.Millisecond)},
				{Args: []string{"lint", "ledger"}, Workspace: "/w", StartedAt: at(300 * time.Millisecond)},
			},
			Workspaces: []types.StatusWorkspace{{Root: "/w", LastAccess: now}},
		}},
		"daemon · 3/8 running · api:build(2.1s) · ui:test(0.5s) · ledger:lint(0.3s) · 1 ws\n")

	assertCompact("daemon queued and overflow running",
		statusReport{Pool: &types.StatusOutput{
			Mode: "daemon", Capacity: 8, Running: 8, Queued: 2,
			RunningTargets: []types.StatusRunningTarget{
				{Args: []string{"build", "api"}, Workspace: "/w", StartedAt: at(15 * time.Second)},
				{Args: []string{"test", "ui"}, Workspace: "/w", StartedAt: at(4 * time.Second)},
				{Args: []string{"lint", "ledger"}, Workspace: "/w", StartedAt: at(2 * time.Second)},
				{Args: []string{"build", "store"}, Workspace: "/w", StartedAt: at(1 * time.Second)},
				{Args: []string{"test", "search"}, Workspace: "/w", StartedAt: at(900 * time.Millisecond)},
			},
			Workspaces: []types.StatusWorkspace{
				{Root: "/w1", LastAccess: now},
				{Root: "/w2", LastAccess: now},
			},
		}},
		"daemon · 8/8 running · +2 queued · api:build(15s) · ui:test(4.0s) · ledger:lint(2.0s) · +2 more · 2 ws\n")

	assertCompact("multi-workspace running prefixes ws",
		statusReport{Pool: &types.StatusOutput{
			Mode: "daemon", Capacity: 4, Running: 2,
			RunningTargets: []types.StatusRunningTarget{
				{Args: []string{"build", "api"}, Workspace: "/srv/alpha", StartedAt: at(1 * time.Second)},
				{Args: []string{"test", "ui"}, Workspace: "/srv/beta", StartedAt: at(500 * time.Millisecond)},
			},
		}},
		"daemon · 2/4 running · alpha/api:build(1.0s) · beta/ui:test(0.5s)\n")

	assertCompact("unparsable args fall back to ?:?",
		statusReport{Pool: &types.StatusOutput{
			Mode: "daemon", Capacity: 4, Running: 1,
			RunningTargets: []types.StatusRunningTarget{{Args: []string{}, Workspace: "/w", StartedAt: at(100 * time.Millisecond)}},
		}},
		"daemon · 1/4 running · ?:?(0.1s)\n")
}

func TestPrintStatusCompactTruncatesLongLabel(t *testing.T) {
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	long := strings.Repeat("x", 80)
	r := statusReport{Pool: &types.StatusOutput{
		Mode: "daemon", Capacity: 4, Running: 1,
		RunningTargets: []types.StatusRunningTarget{{
			Args:      []string{"build", long},
			Workspace: "/w",
			StartedAt: now.Add(-time.Second),
		}},
	}}
	var buf bytes.Buffer
	printStatusCompact(&buf, r, now)
	out := buf.String()
	assert.Contains(t, out, "…", "expected truncation ellipsis")
	for _, part := range strings.Split(strings.TrimRight(out, "\n"), " · ") {
		assert.LessOrEqual(t, utf8.RuneCountInString(part), compactRunningBudget,
			"part %q exceeds compactRunningBudget=%d", part, compactRunningBudget)
	}
}

// TestStartupNoArgsReturnsExitZero locks the shape of startup(): when args
// is empty it prints usage and returns exit code 0 without dispatching.
// This is the cheapest assertion that exercises the full pre-dispatch path
// without requiring a workspace fixture, so it doubles as a guard against
// the refactor accidentally calling os.Exit directly.
func TestStartupNoArgsReturnsExitZero(t *testing.T) {
	// Isolate socket discovery from the host: clearing MAGUS_DAEMON_SOCKET is not
	// enough, because startup still scans proc.SockDir() for the stable daemon
	// socket. Point that dir (XDG_RUNTIME_DIR/magus) at an empty temp dir so a real
	// `magus server start` daemon running on the developer's machine is not found
	// and forwarded to - otherwise its exit code, not this path's, is returned.
	t.Setenv("MAGUS_DAEMON_SOCKET", "")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	res, code := startup(context.Background(), nil)
	if res.cleanup != nil {
		t.Cleanup(res.cleanup)
	}
	require.Equal(t, 0, code, "startup(nil) exit code")
	assert.Empty(t, res.sub, "startup(nil) sub should be empty (no dispatch)")
}

func TestCellState(t *testing.T) {
	assertCell := func(name string, i, running, capacity, numCPU int, want cellKind) {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, want, cellState(i, running, capacity, numCPU))
		})
	}

	assertCell("active first", 0, 3, 8, 16, cellRunning)
	assertCell("active last", 2, 3, 8, 16, cellRunning)
	assertCell("idle first after active", 3, 3, 8, 16, cellIdle)
	assertCell("idle last", 7, 3, 8, 16, cellIdle)
	assertCell("out-of-pool first", 8, 3, 8, 16, cellOutOfPool)
	assertCell("out-of-pool last", 15, 3, 8, 16, cellOutOfPool)
	assertCell("no active workers", 0, 0, 8, 16, cellIdle)
	assertCell("full capacity active", 7, 8, 8, 16, cellRunning)
	assertCell("single cpu", 0, 1, 1, 1, cellRunning)
	assertCell("over-subscribed active", 0, 2, 4, 2, cellRunning)
	assertCell("over-subscribed idle in pool beyond cpu", 3, 2, 4, 2, cellOverSubscribed)
	assertCell("over-subscribed in pool at cpu boundary", 2, 2, 4, 2, cellOverSubscribed)
	assertCell("capacity equals numcpu idle", 4, 2, 8, 8, cellIdle)
	assertCell("capacity equals numcpu out", 8, 2, 8, 8, cellOutOfPool)
}

func TestParseRunning(t *testing.T) {
	assertRunning := func(name string, args []string, wantProj, wantName string) {
		t.Run(name, func(t *testing.T) {
			gotProj, gotTarget := parseRunning(args)
			assert.Equal(t, wantProj, gotProj)
			assert.Equal(t, wantName, gotTarget)
		})
	}

	assertRunning("run target only", []string{"run", "build"}, "", "build")
	assertRunning("run target + project", []string{"run", "build", "api"}, "api", "build")
	assertRunning("run target with charm", []string{"run", "lint:read", "api"}, "api", "lint")
	assertRunning("build subcommand bare", []string{"build", "api"}, "api", "build")
	assertRunning("test subcommand + project", []string{"test", "api"}, "api", "test")
	assertRunning("lint subcommand no project", []string{"lint"}, "", "lint")
	assertRunning("global flag before subcommand", []string{"-x", "run", "build", "api"}, "api", "build")
	assertRunning("unknown subcommand", []string{"weirdcmd", "thing"}, "thing", "weirdcmd")
	assertRunning("empty args", []string{}, "", "")
	assertRunning("only flags", []string{"-x", "--y"}, "", "")
}

func TestDrawRunningTreeGrouping(t *testing.T) {
	running := []types.StatusRunningTarget{
		{Args: []string{"build", "api"}, Workspace: "/home/u/foo"},
		{Args: []string{"test", "api"}, Workspace: "/home/u/foo"},
		{Args: []string{"test", "pkg/x"}, Workspace: "/home/u/foo"},
		{Args: []string{"lint", "web"}, Workspace: "/home/u/bar"},
	}
	var buf bytes.Buffer
	drawRunningTree(&buf, running, time.Now())
	out := buf.String()

	// Multi-workspace: both basenames should appear.
	for _, want := range []string{"foo", "bar", "api", "pkg/x", "web", "build", "test", "lint"} {
		assert.Contains(t, out, want)
	}
	// Tree characters present.
	assert.Contains(t, out, "├")
	assert.Contains(t, out, "└")
}

func TestFormatDur(t *testing.T) {
	assertDur := func(name string, d time.Duration, want string) {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, want, formatDur(d))
		})
	}

	assertDur("zero", 0, "")
	assertDur("negative", -time.Second, "")
	assertDur("sub-second", 300*time.Millisecond, "0.3s")
	assertDur("under 10s", 3*time.Second+400*time.Millisecond, "3.4s")
	assertDur("10s boundary", 10*time.Second, "10s")
	assertDur("under 1m", 47*time.Second, "47s")
	assertDur("1m boundary", time.Minute, "1m0s")
	assertDur("minutes+seconds", 2*time.Minute+17*time.Second, "2m17s")
	assertDur("1h boundary", time.Hour, "1h0m")
	assertDur("hours+minutes", 3*time.Hour+5*time.Minute, "3h5m")
}

func TestDrawRunningTreeDuration(t *testing.T) {
	now := time.Now()
	running := []types.StatusRunningTarget{
		{Args: []string{"build", "api"}, Workspace: "/home/u/foo", StartedAt: now.Add(-3*time.Second - 400*time.Millisecond)},
		{Args: []string{"test", "api"}, Workspace: "/home/u/foo", StartedAt: now.Add(-45 * time.Second)},
	}
	var buf bytes.Buffer
	drawRunningTree(&buf, running, now)
	out := buf.String()
	assert.Contains(t, out, "(3.4s)")
	assert.Contains(t, out, "(45s)")
}

func TestDrawRunningTreeSingleWorkspaceCollapses(t *testing.T) {
	running := []types.StatusRunningTarget{
		{Args: []string{"build", "api"}, Workspace: "/home/u/foo"},
		{Args: []string{"test", "api"}, Workspace: "/home/u/foo"},
	}
	var buf bytes.Buffer
	drawRunningTree(&buf, running, time.Now())
	out := buf.String()

	assert.NotContains(t, out, "foo", "single-workspace output should not show workspace label")
	assert.Contains(t, out, "api")
	assert.Contains(t, out, "build")
	assert.Contains(t, out, "test")
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

func TestProbeMCPReadiness(t *testing.T) {
	t.Run("ready-200", func(t *testing.T) {
		assert.Equal(t, http.StatusOK, probeMCPReadiness(context.Background(), readyzServer(t, http.StatusOK)))
	})
	t.Run("not-ready-503", func(t *testing.T) {
		assert.Equal(t, http.StatusServiceUnavailable, probeMCPReadiness(context.Background(), readyzServer(t, http.StatusServiceUnavailable)))
	})
	t.Run("answered-other-status-reads-as-ok", func(t *testing.T) {
		// An older daemon without /readyz still proves a listener is up: any answered
		// status collapses to OK so the endpoint reads as reachable.
		assert.Equal(t, http.StatusOK, probeMCPReadiness(context.Background(), readyzServer(t, http.StatusTeapot)))
	})
	t.Run("nothing-listening-returns-zero", func(t *testing.T) {
		// Reserved-for-docs TEST-NET-1 address that refuses fast; 0 == unreachable.
		assert.Equal(t, 0, probeMCPReadiness(context.Background(), "127.0.0.1:1"))
	})
}

func boolPtr(b bool) *bool { return &b }

// mcpServing returns an MCP config enabled and pointing at a live endpoint whose /readyz
// returns code (200 serving, 503 not-ready). mcpUnreachable and mcpDisabled are the other
// two states. They build an explicit config to pass in, so no test mutates package state.
func mcpServing(t *testing.T, code int) config.MCP {
	return config.MCP{Address: readyzServer(t, code), Enabled: boolPtr(true)}
}

// mcpUnreachable returns an enabled MCP config whose address nothing listens on.
func mcpUnreachable() config.MCP {
	return config.MCP{Address: "127.0.0.1:1", Enabled: boolPtr(true)}
}

// mcpDisabled returns an MCP config with the server turned off.
func mcpDisabled() config.MCP {
	return config.MCP{Enabled: boolPtr(false)}
}

func TestMCPAddress(t *testing.T) {
	assert.Equal(t, "127.0.0.1:9000", mcpAddress(config.MCP{Address: "127.0.0.1:9000"}))
	assert.Equal(t, internalmcp.DefaultAddress, mcpAddress(config.MCP{}), "empty address falls back to the default")
}

func TestBuildMCPEndpointStatus(t *testing.T) {
	ctx := context.Background()
	t.Run("disabled", func(t *testing.T) {
		got := buildMCPEndpointStatus(ctx, mcpDisabled())
		require.NotNil(t, got)
		assert.False(t, got.Enabled)
		assert.Equal(t, "disabled", got.State)
		assert.False(t, got.Reachable)
	})
	t.Run("serving", func(t *testing.T) {
		got := buildMCPEndpointStatus(ctx, mcpServing(t, http.StatusOK))
		require.NotNil(t, got)
		assert.True(t, got.Enabled)
		assert.True(t, got.Reachable)
		assert.Equal(t, "serving", got.State)
		assert.Contains(t, got.URL, "/mcp")
	})
	t.Run("not-ready", func(t *testing.T) {
		got := buildMCPEndpointStatus(ctx, mcpServing(t, http.StatusServiceUnavailable))
		require.NotNil(t, got)
		assert.True(t, got.Reachable)
		assert.Equal(t, "not-ready", got.State)
	})
	t.Run("unreachable-points-at-server-start", func(t *testing.T) {
		got := buildMCPEndpointStatus(ctx, mcpUnreachable())
		require.NotNil(t, got)
		assert.False(t, got.Reachable)
		assert.Equal(t, "unreachable", got.State)
		assert.Contains(t, got.Note, "magus server start")
	})
}

func TestPrintMCPEndpointStatus(t *testing.T) {
	render := func(m *types.MCPEndpointStatus) string {
		var b strings.Builder
		printMCPEndpointStatus(&b, m)
		return b.String()
	}

	t.Run("nil-renders-nothing", func(t *testing.T) {
		assert.Empty(t, render(nil))
	})
	t.Run("serving-shows-url-and-state", func(t *testing.T) {
		out := render(&types.MCPEndpointStatus{Enabled: true, URL: "http://127.0.0.1:7391/mcp", Reachable: true, State: "serving"})
		assert.Contains(t, out, "mcp endpoint")
		assert.Contains(t, out, "http://127.0.0.1:7391/mcp")
		assert.Contains(t, out, "serving")
	})
	t.Run("unreachable-shows-note", func(t *testing.T) {
		out := render(&types.MCPEndpointStatus{Enabled: true, URL: "http://127.0.0.1:7391/mcp", State: "unreachable", Note: "start the daemon: magus server start"})
		assert.Contains(t, out, "unreachable")
		assert.Contains(t, out, "magus server start")
	})
	t.Run("disabled-omits-url", func(t *testing.T) {
		out := render(&types.MCPEndpointStatus{State: "disabled", Note: "MCP is disabled (mcp.enabled=false); no agent tools are served."})
		assert.Contains(t, out, "disabled")
		assert.NotContains(t, out, "url")
	})
}

func TestParseProbeKind(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want probeKind
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
	assert.Equal(t, "unknown", probeName(probeKind(99)))
}

func TestParseProbeKinds(t *testing.T) {
	t.Run("single", func(t *testing.T) {
		kinds, err := parseProbeKinds("liveness")
		require.NoError(t, err)
		assert.Equal(t, []probeKind{probeLiveness}, kinds)
	})
	t.Run("comma-combined-in-order", func(t *testing.T) {
		kinds, err := parseProbeKinds("liveness,mcp")
		require.NoError(t, err)
		assert.Equal(t, []probeKind{probeLiveness, probeMCP}, kinds)
	})
	t.Run("trims-and-tolerates-empty-segments", func(t *testing.T) {
		kinds, err := parseProbeKinds(" mcp , ,liveness,")
		require.NoError(t, err)
		assert.Equal(t, []probeKind{probeMCP, probeLiveness}, kinds)
	})
	t.Run("dedupes", func(t *testing.T) {
		kinds, err := parseProbeKinds("mcp,mcp")
		require.NoError(t, err)
		assert.Equal(t, []probeKind{probeMCP}, kinds)
	})
	t.Run("unknown-errors", func(t *testing.T) {
		_, err := parseProbeKinds("liveness,bogus")
		require.Error(t, err)
	})
	t.Run("all-empty-errors", func(t *testing.T) {
		_, err := parseProbeKinds(" , ,")
		require.Error(t, err)
	})
}

func TestEvaluateMCPHealth(t *testing.T) {
	t.Run("serving-passes", func(t *testing.T) {
		ok, reason := evaluateMCPHealth(&types.MCPEndpointStatus{Reachable: true, State: "serving", URL: "http://127.0.0.1:7391/mcp"})
		assert.True(t, ok)
		assert.Contains(t, reason, "serving")
	})
	t.Run("not-ready-passes", func(t *testing.T) {
		// The endpoint is up; a liveness/ensure check should not restart the daemon.
		ok, _ := evaluateMCPHealth(&types.MCPEndpointStatus{Reachable: true, State: "not-ready", URL: "http://127.0.0.1:7391/mcp"})
		assert.True(t, ok)
	})
	t.Run("unreachable-fails-with-note", func(t *testing.T) {
		ok, reason := evaluateMCPHealth(&types.MCPEndpointStatus{State: "unreachable", Note: "start the daemon: magus server start"})
		assert.False(t, ok)
		assert.Contains(t, reason, "magus server start")
	})
	t.Run("disabled-fails", func(t *testing.T) {
		ok, reason := evaluateMCPHealth(&types.MCPEndpointStatus{State: "disabled", Note: "MCP is disabled (mcp.enabled=false); no agent tools are served."})
		assert.False(t, ok)
		assert.Contains(t, reason, "disabled")
	})
	t.Run("nil-fails", func(t *testing.T) {
		ok, _ := evaluateMCPHealth(nil)
		assert.False(t, ok)
	})
	t.Run("unreachable-without-note-falls-back-to-state", func(t *testing.T) {
		ok, reason := evaluateMCPHealth(&types.MCPEndpointStatus{State: "unreachable"})
		assert.False(t, ok)
		assert.Equal(t, "mcp endpoint unreachable", reason)
	})
}

// recordingStatus is a statusFunc that counts calls and returns a fixed snapshot/err, so
// tests can assert how many times (if at all) the daemon socket was dialed.
func recordingStatus(calls *int, out *types.StatusOutput, err error) statusFunc {
	return func(context.Context) (*types.StatusOutput, error) {
		*calls++
		return out, err
	}
}

func TestEvaluateProbes(t *testing.T) {
	ctx := context.Background()
	aliveDaemon := &types.StatusOutput{ParentPID: 42, Mode: "daemon", Workspaces: []types.StatusWorkspace{{Root: "/ws"}}}

	t.Run("liveness-alone-passes-and-dials-once", func(t *testing.T) {
		calls := 0
		res := evaluateProbes(ctx, recordingStatus(&calls, aliveDaemon, nil), config.MCP{}, []probeKind{probeLiveness}, "")
		require.Len(t, res, 1)
		assert.True(t, res[0].ok)
		assert.Equal(t, 1, calls)
	})
	t.Run("two-socket-probes-dial-once", func(t *testing.T) {
		calls := 0
		res := evaluateProbes(ctx, recordingStatus(&calls, aliveDaemon, nil), config.MCP{}, []probeKind{probeLiveness, probeReadiness}, "")
		require.Len(t, res, 2)
		assert.True(t, res[0].ok)
		assert.True(t, res[1].ok)
		assert.Equal(t, 1, calls, "the daemon snapshot is fetched once and reused")
	})
	t.Run("mcp-only-never-dials-the-daemon", func(t *testing.T) {
		calls := 0
		res := evaluateProbes(ctx, recordingStatus(&calls, nil, errors.New("must not be called")), mcpServing(t, http.StatusOK), []probeKind{probeMCP}, "")
		require.Len(t, res, 1)
		assert.True(t, res[0].ok)
		assert.Equal(t, 0, calls, "an mcp-only probe makes no proc RPC")
	})
	t.Run("combined-liveness-and-mcp-both-evaluated", func(t *testing.T) {
		calls := 0
		res := evaluateProbes(ctx, recordingStatus(&calls, aliveDaemon, nil), mcpUnreachable(), []probeKind{probeLiveness, probeMCP}, "")
		require.Len(t, res, 2)
		assert.True(t, res[0].ok, "daemon is alive")
		assert.False(t, res[1].ok, "mcp endpoint is down")
		assert.Equal(t, probeMCP, res[1].kind)
	})
}

func TestRunProbesMCPOnly(t *testing.T) {
	ctx := context.Background()
	t.Run("serving-returns-nil", func(t *testing.T) {
		// socket "" is never dialed for an mcp-only probe, so this makes no proc RPC.
		assert.NoError(t, runProbes(ctx, "", mcpServing(t, http.StatusOK), []probeKind{probeMCP}, ""))
	})
	t.Run("unreachable-exits-1", func(t *testing.T) {
		err := runProbes(ctx, "", mcpUnreachable(), []probeKind{probeMCP}, "")
		require.Error(t, err)
		var silent errSilent
		require.ErrorAs(t, err, &silent)
		assert.Equal(t, 1, silent.exitCode)
	})
}

func TestRenderProbeResults(t *testing.T) {
	render := func(results []probeResult) (string, string, bool) {
		var out, errb strings.Builder
		ok := renderProbeResults(&out, &errb, results)
		return out.String(), errb.String(), ok
	}

	t.Run("single-pass-no-label", func(t *testing.T) {
		out, errb, ok := render([]probeResult{{kind: probeLiveness, ok: true, reason: "daemon pid 42 is alive"}})
		assert.True(t, ok)
		assert.Equal(t, "ok: daemon pid 42 is alive\n", out)
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

// TestPrintStatusTextRendersMCPEndpoint proves the mcp endpoint block appears in the full
// text render (not just the isolated helper). printStatusText writes to an *os.File, so a
// temp file stands in for stdout.
func TestPrintStatusTextRendersMCPEndpoint(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "status-*")
	require.NoError(t, err)
	r := statusReport{
		MCPEndpoint: &types.MCPEndpointStatus{Enabled: true, URL: "http://127.0.0.1:7391/mcp", Reachable: true, State: "serving"},
	}
	printStatusText(f, r, false, 0)
	require.NoError(t, f.Close())
	body, err := os.ReadFile(f.Name())
	require.NoError(t, err)
	out := string(body)
	assert.Contains(t, out, "mcp endpoint")
	assert.Contains(t, out, "http://127.0.0.1:7391/mcp")
	assert.Contains(t, out, "serving")
}

// TestPrintStatusTextFullReport exercises printStatusText's populated branches (telemetry
// note, a running-pool with targets and workspaces, the mcp endpoint block) in one render,
// confirming the mcp block coexists with the daemon block rather than replacing it.
func TestPrintStatusTextFullReport(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "status-*")
	require.NoError(t, err)
	r := statusReport{
		Telemetry: telemetryStatus{Note: "telemetry is disabled."},
		Cache:     cacheStatus{Dir: "/cache", SizeMB: 10},
		Pool: &types.StatusOutput{
			ParentPID: 4242, Mode: "daemon", Capacity: 8, Running: 1,
			RunningTargets: []types.StatusRunningTarget{{Args: []string{"run", "build", "web"}, Workspace: "/repo"}},
			Workspaces:     []types.StatusWorkspace{{Root: "/repo"}},
		},
		MCPEndpoint: &types.MCPEndpointStatus{Enabled: true, URL: "http://127.0.0.1:7391/mcp", Reachable: true, State: "serving"},
	}
	printStatusText(f, r, false, 0)
	require.NoError(t, f.Close())
	body, err := os.ReadFile(f.Name())
	require.NoError(t, err)
	out := string(body)
	assert.Contains(t, out, "daemon pid 4242", "the daemon block still renders")
	assert.Contains(t, out, "loaded workspaces")
	assert.Contains(t, out, "mcp endpoint", "the mcp block renders alongside the daemon block")
	assert.Contains(t, out, "serving")
	assert.Contains(t, out, "telemetry is disabled.")
}

func TestCompactMCPToken(t *testing.T) {
	assert.Empty(t, compactMCPToken(nil))
	assert.Empty(t, compactMCPToken(&types.MCPEndpointStatus{State: "serving"}), "serving is the steady state; omitted from the compact line")
	assert.Equal(t, "mcp unreachable", compactMCPToken(&types.MCPEndpointStatus{State: "unreachable"}))
	assert.Equal(t, "mcp not-ready", compactMCPToken(&types.MCPEndpointStatus{State: "not-ready"}))
}
