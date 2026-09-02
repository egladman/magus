package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
	internalmcp "github.com/egladman/magus/internal/handler/mcp"
	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/types"
)

func TestPrintStatusCompact(t *testing.T) {
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	at := func(ago time.Duration) time.Time { return now.Add(-ago) }

	assertCompact := func(name string, report types.StatusReport, want string) {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			printStatusCompact(&buf, report, now)
			assert.Equal(t, want, buf.String())
			assert.Equal(t, 1, strings.Count(buf.String(), "\n"), "compact must emit exactly one line")
		})
	}

	assertCompact("no parent",
		types.StatusReport{PoolError: "no running magus proc server found"},
		"daemon: off\n")

	assertCompact("daemon idle",
		types.StatusReport{Pool: &types.StatusOutput{
			Mode: "daemon", Capacity: 8, Running: 0,
		}},
		"daemon · 0/8 idle\n")

	assertCompact("proc-server label",
		types.StatusReport{Pool: &types.StatusOutput{
			Mode: "proc", Capacity: 8, Running: 1,
			RunningTargets: []types.StatusRunningTarget{
				{Args: []string{"test", "web"}, Workspace: "/w", StartedAt: at(400 * time.Millisecond)},
			},
		}},
		"pool · 1/8 running · web:test(0.4s)\n")

	assertCompact("daemon running with targets, sorted oldest first",
		types.StatusReport{Pool: &types.StatusOutput{
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
		types.StatusReport{Pool: &types.StatusOutput{
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
		types.StatusReport{Pool: &types.StatusOutput{
			Mode: "daemon", Capacity: 4, Running: 2,
			RunningTargets: []types.StatusRunningTarget{
				{Args: []string{"build", "api"}, Workspace: "/srv/alpha", StartedAt: at(1 * time.Second)},
				{Args: []string{"test", "ui"}, Workspace: "/srv/beta", StartedAt: at(500 * time.Millisecond)},
			},
		}},
		"daemon · 2/4 running · alpha/api:build(1.0s) · beta/ui:test(0.5s)\n")

	assertCompact("shared services report activity and dependents",
		types.StatusReport{
			Pool: &types.StatusOutput{Mode: "daemon", Capacity: 4},
			Services: []types.StatusService{
				{State: "running", Dependents: 2},
				{State: "idle", Dependents: 0},
			},
		},
		"daemon · 0/4 idle · services 1/2 active, 2 dependents\n")

	assertCompact("unparsable args fall back to ?:?",
		types.StatusReport{Pool: &types.StatusOutput{
			Mode: "daemon", Capacity: 4, Running: 1,
			RunningTargets: []types.StatusRunningTarget{{Args: []string{}, Workspace: "/w", StartedAt: at(100 * time.Millisecond)}},
		}},
		"daemon · 1/4 running · ?:?(0.1s)\n")
}

func TestClampStatusWatch(t *testing.T) {
	assert.Equal(t, time.Duration(0), clampStatusWatch(0))
	assert.Equal(t, statusWatchMin, clampStatusWatch(time.Second))
	assert.Equal(t, statusWatchMin, clampStatusWatch(statusWatchMin))
	assert.Equal(t, 30*time.Second, clampStatusWatch(30*time.Second))
}

// A negative --watch reached time.NewTicker, which PANICS at or below zero: the
// clamp guarded on `interval > 0` and the single-snapshot branch above it on
// `== 0`, so anything negative fell between them and took the process down.
func TestClampStatusWatchFloorsANegativeInterval(t *testing.T) {
	for _, d := range []time.Duration{-time.Nanosecond, -time.Second, -time.Hour} {
		assert.Equalf(t, statusWatchMin, clampStatusWatch(d),
			"clampStatusWatch(%s) must floor at the minimum, not hand a panic to NewTicker", d)
	}
	// The value the watch loop actually builds its ticker from must be positive.
	assert.Positive(t, clampStatusWatch(-time.Second))
}

func TestPrintStatusCompactTruncatesLongLabel(t *testing.T) {
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	long := strings.Repeat("x", 80)
	r := types.StatusReport{Pool: &types.StatusOutput{
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
	assert.Contains(t, out, "...", "expected truncation ellipsis")
	assert.NotContains(t, out, "…", "the ellipsis is three ASCII dots, not U+2026")
	for _, part := range strings.Split(strings.TrimRight(out, "\n"), " · ") {
		assert.LessOrEqual(t, utf8.RuneCountInString(part), compactRunningBudget,
			"part %q exceeds compactRunningBudget=%d", part, compactRunningBudget)
	}
}

// TestStartupNoSubcommandExitsUsage locks the shape of startup(): with no
// subcommand it prints usage and returns exitUsage WITHOUT dispatching. This is the
// cheapest assertion that exercises the full pre-dispatch path without a workspace
// fixture, so it doubles as a guard against the refactor accidentally calling
// os.Exit directly.
//
// It asserted 0 until the exit-code contract in helpers.go was applied here: no
// subcommand is a wrong invocation, not work that succeeded, and returning 0 made
// `magus $CMD` with an empty CMD a green step in anything that builds its argv
// dynamically. An EXPLICIT help flag still exits 0 - see the subtest - because there
// the usage text IS what was asked for, and conflating the two is the easy mistake:
// both reach this branch with no subcommand left to run.
func TestStartupNoSubcommandExitsUsage(t *testing.T) {
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
	require.Equal(t, exitUsage, code, "startup(nil) exit code")
	assert.Empty(t, res.sub, "startup(nil) sub should be empty (no dispatch)")

	// An explicit global help flag lands in the SAME no-subcommand branch and must
	// still exit 0: the flag package has already printed usage, and that is the work
	// the caller asked for.
	for _, flagName := range []string{"-h", "--help"} {
		t.Run(flagName, func(t *testing.T) {
			t.Setenv("MAGUS_DAEMON_SOCKET", "")
			t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
			res, code := startup(context.Background(), []string{flagName})
			if res.cleanup != nil {
				t.Cleanup(res.cleanup)
			}
			assert.Equal(t, 0, code, "an explicit %s exits 0, not exitUsage", flagName)
		})
	}
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

// TestPrintMachineStatusNamesEveryClaim covers the question the section exists to
// answer: WHERE the machine's budget went. One daemon serves every worktree, so a pid
// alone does not say which tree to go and look at.
func TestPrintMachineStatusNamesEveryClaim(t *testing.T) {
	var buf bytes.Buffer
	printMachineStatus(&buf, &types.MachineSnapshot{
		BudgetMB: 48 << 10, HeldMB: 10 << 10, BudgetSlots: 8, HeldSlots: 6,
		Holders: []types.MachineClaimant{
			{Project: ".", Target: "test", PID: 41221, MemoryMB: 10 << 10, Dir: "/tree/polish", Since: time.Now().Add(-90 * time.Second)},
		},
		Waiters: []types.MachineClaimant{
			{Project: "docs", Target: "ci", PID: 41999, Dir: "/tree/hardening"},
		},
	})
	out := buf.String()
	assert.Contains(t, out, "memory  10.0 GiB of 48.0 GiB held")
	assert.Contains(t, out, "slots   6 of 8 held")
	assert.Contains(t, out, "held  . test  pid 41221  10.0 GiB")
	assert.Contains(t, out, "in /tree/polish")
	assert.Contains(t, out, "queued docs ci  pid 41999")
	assert.Contains(t, out, "in /tree/hardening")
}

// An idle budget still prints. "Nothing is queued" is the answer to the question people
// open this section to ask, and a silent section reads as a missing feature.
func TestPrintMachineStatusIdleAndAbsent(t *testing.T) {
	var idle bytes.Buffer
	printMachineStatus(&idle, &types.MachineSnapshot{BudgetMB: 48 << 10, BudgetSlots: 8})
	assert.Contains(t, idle.String(), "nothing is holding or waiting")

	// No daemon answered, so there is no machine budget to report on.
	var absent bytes.Buffer
	printMachineStatus(&absent, nil)
	assert.Empty(t, absent.String())
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
	r := types.StatusReport{
		MCPEndpoint: &types.MCPEndpointStatus{Enabled: true, URL: "http://127.0.0.1:7391/mcp", Reachable: true, State: "serving"},
		Services:    []types.StatusService{{ID: "service-1", Label: "postgres", Command: "docker run postgres", Ports: []string{"5432"}, State: "running", Dependents: 2}},
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
	r := types.StatusReport{
		Telemetry: types.TelemetryStatus{Note: "telemetry is disabled."},
		Cache:     types.CacheStatus{Dir: "/cache", SizeMB: 10},
		Pool: &types.StatusOutput{
			ParentPID: 4242, Mode: "daemon", Capacity: 8, Running: 1,
			RunningTargets: []types.StatusRunningTarget{{Args: []string{"run", "build", "web"}, Workspace: "/repo"}},
			Workspaces:     []types.StatusWorkspace{{Root: "/repo"}},
		},
		MCPEndpoint: &types.MCPEndpointStatus{Enabled: true, URL: "http://127.0.0.1:7391/mcp", Reachable: true, State: "serving"},
		Services:    []types.StatusService{{ID: "service-1", Label: "postgres", State: "running", Dependents: 2}},
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
	assert.Contains(t, out, "shared services (1)")
	assert.Contains(t, out, "2 dependents")
}

func TestPrintStatusTextDoesNotCallActiveLocalWorkIdle(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "status-*")
	require.NoError(t, err)
	printStatusText(f, types.StatusReport{Pool: &types.StatusOutput{
		Mode: "proc", Capacity: 8, Running: 1,
	}}, false, 0)
	require.NoError(t, f.Close())
	body, err := os.ReadFile(f.Name())
	require.NoError(t, err)
	assert.Contains(t, string(body), "local work active; detailed target data unavailable")
	assert.NotContains(t, string(body), "nothing running")
}

// fakeProcServers returns a statusQuery answering for exactly the addresses in replies,
// standing in for live proc servers. An address with no entry fails the way a socket that
// died between discovery and the query does.
func fakeProcServers(replies map[string]*proc.StatusReply) statusQuery {
	return func(_ context.Context, addr string) (*proc.StatusReply, error) {
		reply, ok := replies[addr]
		if !ok {
			return nil, errors.New("connection refused")
		}
		return reply, nil
	}
}

func TestApplyStatusPools(t *testing.T) {
	ctx := context.Background()
	const sockA, sockB = "unix:///run/magus-111.sock", "unix:///run/magus-222.sock"
	servers := map[string]*proc.StatusReply{
		sockA: {ParentPID: 111, Mode: "proc", Capacity: 8, Running: 3,
			Services: []types.StatusService{{ID: "service-1", State: "running", Dependents: 3}}},
		sockB: {ParentPID: 222, Mode: "proc", Capacity: 4, Running: 4},
	}

	t.Run("one server fills pool and leaves the list empty", func(t *testing.T) {
		report := types.StatusReport{}
		applyStatusPools(ctx, &report, []string{sockA}, fakeProcServers(servers))
		require.NotNil(t, report.Pool)
		assert.Equal(t, 111, report.Pool.ParentPID)
		assert.Equal(t, sockA, report.Pool.Socket)
		assert.Equal(t, 5, report.Pool.Available, "8 capacity less 3 running")
		assert.Empty(t, report.Pools, "a single server is not repeated as a list")
		assert.Empty(t, report.PoolError)
	})

	t.Run("carries the shared services of the first server", func(t *testing.T) {
		report := types.StatusReport{}
		applyStatusPools(ctx, &report, []string{sockA, sockB}, fakeProcServers(servers))
		assert.Equal(t, servers[sockA].Services, report.Services)
	})

	t.Run("two servers report as two entries, never an error", func(t *testing.T) {
		report := types.StatusReport{}
		applyStatusPools(ctx, &report, []string{sockA, sockB}, fakeProcServers(servers))
		assert.Empty(t, report.PoolError, "more than one server is reported, not refused")
		require.Len(t, report.Pools, 2)
		assert.Equal(t, []string{sockA, sockB}, []string{report.Pools[0].Socket, report.Pools[1].Socket})
		assert.Equal(t, []int{111, 222}, []int{report.Pools[0].ParentPID, report.Pools[1].ParentPID})
		assert.Equal(t, []int{5, 0}, []int{report.Pools[0].Available, report.Pools[1].Available})
		require.NotNil(t, report.Pool)
		assert.Equal(t, sockA, report.Pool.Socket, "the first server is still THE pool")
	})

	t.Run("a server that died is dropped, the rest still report", func(t *testing.T) {
		report := types.StatusReport{}
		applyStatusPools(ctx, &report, []string{"unix:///run/magus-gone.sock", sockB}, fakeProcServers(servers))
		assert.Empty(t, report.PoolError)
		require.NotNil(t, report.Pool)
		assert.Equal(t, sockB, report.Pool.Socket)
		assert.Empty(t, report.Pools, "one survivor is the single-server case")
	})

	t.Run("nothing answered reports why", func(t *testing.T) {
		report := types.StatusReport{}
		applyStatusPools(ctx, &report, []string{"unix:///run/magus-gone.sock"}, fakeProcServers(servers))
		assert.Nil(t, report.Pool)
		assert.Contains(t, report.PoolError, "magus-gone.sock")
	})
}

// TestResolveStatusSocketsNarrowing pins that --socket (and the env pin) still select one
// server, and that discovery is only consulted when neither pins one.
func TestResolveStatusSocketsNarrowing(t *testing.T) {
	ctx := context.Background()

	t.Run("--socket narrows to one", func(t *testing.T) {
		t.Setenv("MAGUS_DAEMON_SOCKET", "unix:///run/magus-env.sock")
		addrs, err := resolveStatusSockets(ctx, "unix:///run/magus-flag.sock")
		require.NoError(t, err)
		assert.Equal(t, []string{"unix:///run/magus-flag.sock"}, addrs, "the flag wins over the env")
	})

	t.Run("env pins one", func(t *testing.T) {
		t.Setenv("MAGUS_DAEMON_SOCKET", "unix:///run/magus-env.sock")
		addrs, err := resolveStatusSockets(ctx, "")
		require.NoError(t, err)
		assert.Equal(t, []string{"unix:///run/magus-env.sock"}, addrs)
	})

	t.Run("unpinned discovers, and says so when there is nothing", func(t *testing.T) {
		t.Setenv("MAGUS_DAEMON_SOCKET", "")
		t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
		_, err := resolveStatusSockets(ctx, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no running magus proc server")
	})
}

// TestBuildConfigStatusConcurrency covers the fact a lease orchestrator budgets
// against: concurrency 0 means "unset", and only the effective value answers how wide a
// run gets. The wants are stated independently of internal/cache.ResolveConcurrency, which
// is the function under test here.
func TestBuildConfigStatusConcurrency(t *testing.T) {
	cpus := runtime.NumCPU()
	for _, tc := range []struct {
		name       string
		env        string
		configured int
		want       int
	}{
		{"unset resolves to the machine default", "", 0, min(cpus, 8)},
		{"unset honours MAGUS_CONCURRENCY", "3", 0, min(3, cpus)},
		{"an explicit value passes through", "3", 2, min(2, cpus)},
		{"a value above the machine is clamped", "", cpus + 4, cpus},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MAGUS_CONCURRENCY", tc.env)
			t.Setenv("GITHUB_ACTIONS", "") // the hosted-runner default must not decide this
			got := buildConfigStatus(config.Config{Concurrency: tc.configured})
			assert.Equal(t, tc.want, got.ConcurrencyEffective)
			assert.Equal(t, tc.configured, got.Concurrency, "the configured value is kept alongside")
		})
	}
}

// TestPrintStatusTextListsEveryProcServer proves the multi-server case renders as a list
// with each server's slots rather than an error demanding --socket.
func TestPrintStatusTextListsEveryProcServer(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "status-*")
	require.NoError(t, err)
	pools := []types.StatusOutput{
		{ParentPID: 111, Mode: "proc", Socket: "unix:///run/magus-111.sock", Capacity: 8, Running: 3, Available: 5},
		{ParentPID: 222, Mode: "proc", Socket: "unix:///run/magus-222.sock", Capacity: 4, Running: 4},
	}
	printStatusText(f, types.StatusReport{Pool: &pools[0], Pools: pools}, false, 0)
	require.NoError(t, f.Close())
	body, err := os.ReadFile(f.Name())
	require.NoError(t, err)
	out := string(body)

	assert.Contains(t, out, "proc servers (2)")
	assert.Contains(t, out, "unix:///run/magus-111.sock")
	assert.Contains(t, out, "unix:///run/magus-222.sock")
	assert.Contains(t, out, "pid 111")
	assert.Contains(t, out, "3/8 in use")
	assert.Contains(t, out, "4/4 in use")
	assert.Contains(t, out, "5 available")
	assert.NotContains(t, out, "use --socket", "a second server is reported, not refused")
}

// TestPrintStatusTextReportsSlotsAndConcurrency covers the single-server case: the pool
// line carries available slots, and the concurrency block carries the effective width.
func TestPrintStatusTextReportsSlotsAndConcurrency(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "status-*")
	require.NoError(t, err)
	r := types.StatusReport{
		Config: types.StatusConfig{ConcurrencyEffective: 8},
		Pool:   &types.StatusOutput{ParentPID: 4242, Mode: "daemon", Capacity: 8, Running: 2, Available: 6},
	}
	printStatusText(f, r, false, 0)
	require.NoError(t, f.Close())
	body, err := os.ReadFile(f.Name())
	require.NoError(t, err)
	out := string(body)

	assert.Contains(t, out, "available: 6")
	assert.Contains(t, out, "concurrency")
	assert.Contains(t, out, "(default)", "an unset configured value says so instead of printing 0")
	assert.Contains(t, out, "effective")
	assert.Contains(t, out, "8")
}

func TestCompactMCPToken(t *testing.T) {
	assert.Empty(t, compactMCPToken(nil))
	assert.Empty(t, compactMCPToken(&types.MCPEndpointStatus{State: "serving"}), "serving is the steady state; omitted from the compact line")
	assert.Equal(t, "mcp unreachable", compactMCPToken(&types.MCPEndpointStatus{State: "unreachable"}))
	assert.Equal(t, "mcp not-ready", compactMCPToken(&types.MCPEndpointStatus{State: "not-ready"}))
}

// statusFixture is one report carrying every optional section, so the renderers below are
// exercised against the same data instead of each against a fixture shaped to suit it.
// now anchors the durations: every StartedAt is an offset from it, so the rendered clock
// is the same on every run.
func statusFixture(now time.Time) types.StatusReport {
	return types.StatusReport{
		Telemetry: types.TelemetryStatus{
			Enabled:     true,
			Endpoint:    "localhost:4317",
			Protocol:    "grpc",
			Insecure:    true,
			ServiceName: "magus",
			SampleRatio: 0.25,
			Note:        "traces are sampled",
		},
		Cache: types.CacheStatus{Immutable: true, Dir: "/tmp/magus-cache", SizeMB: 512},
		Build: types.BuildStatus{SelfUpdate: true},
		Config: types.StatusConfig{
			Concurrency:          8,
			ConcurrencyEffective: 8,
			DefaultCharms:        []string{"rw"},
		},
		Pool: &types.StatusOutput{
			ParentPID:     4242,
			DaemonVersion: "v1.2.3",
			Mode:          "daemon",
			Capacity:      4,
			Running:       2,
			Available:     2,
			Queued:        3,
			RunningTargets: []types.StatusRunningTarget{
				{Args: []string{"run", "build", "web"}, Workspace: "/repos/alpha", StartedAt: now.Add(-90 * time.Second), Step: "tsc --noEmit"},
				{Args: []string{"run", "test", "api"}, Workspace: "/repos/beta", StartedAt: now.Add(-3 * time.Second)},
				{Args: []string{"--totally", "--flags"}, Workspace: "/repos/beta"},
			},
			Workspaces: []types.StatusWorkspace{{Root: "/repos/alpha", LastAccess: now.Add(-time.Minute)}},
		},
		Pools: []types.StatusOutput{
			{ParentPID: 4242, Capacity: 4, Running: 2, Available: 2, Socket: "unix:///tmp/a.sock"},
			{ParentPID: 5353, Capacity: 2, Running: 0, Available: 2, Socket: "unix:///tmp/b.sock"},
		},
		Services: []types.StatusService{
			{ID: "svc-db", Label: "postgres", Command: "docker compose up db", Ports: []string{"5432"}, State: types.ServiceRunning, Dependents: 2},
			{ID: "svc-bare"},
		},
		SymbolIndexes: []types.SymbolIndexStatus{
			{Project: types.ProjectRef{Path: "web"}, Language: "typescript", Freshness: types.SymbolIndexFresh},
			{Project: types.ProjectRef{Path: ".", Dir: "/repos/named-root"}, Freshness: types.SymbolIndexNotBuilt},
		},
		Locks: []types.StatusLock{
			{Project: "web", PID: 999, Command: "magus run build", AcquireTime: now.Add(-2 * time.Hour)},
			{Project: "."},
		},
		MCPEndpoint: &types.MCPEndpointStatus{Enabled: true, URL: "http://127.0.0.1:7777/mcp", State: "serving"},
	}
}

// TestPrintStatusTextRendersEverySection covers the full text frame: the tabwriter block,
// the non-grid pool listing, and each optional section's own printer.
func TestPrintStatusTextRendersEverySection(t *testing.T) {
	var buf bytes.Buffer
	printStatusText(&buf, statusFixture(time.Now()), false, 0)
	out := buf.String()

	assert.Contains(t, out, "telemetry")
	assert.Contains(t, out, "localhost:4317")
	assert.Contains(t, out, "grpc")
	assert.Contains(t, out, "insecure")
	assert.Contains(t, out, "0.25")
	assert.Contains(t, out, "traces are sampled")

	assert.Contains(t, out, "/tmp/magus-cache")
	assert.Contains(t, out, "512")
	assert.Contains(t, out, "concurrency")

	assert.Contains(t, out, "daemon pid 4242")
	assert.Contains(t, out, "capacity: 4   running: 2   available: 2   queued: 3")
	assert.Contains(t, out, "/repos/alpha")
	assert.Contains(t, out, "run build web")
	assert.Contains(t, out, "loaded workspaces (1)")

	assert.Contains(t, out, "proc servers (2)")
	assert.Contains(t, out, "unix:///tmp/b.sock")

	assert.Contains(t, out, "mcp endpoint")
	assert.Contains(t, out, "http://127.0.0.1:7777/mcp")

	assert.Contains(t, out, "shared services (2)")
	assert.Contains(t, out, "postgres")
	assert.Contains(t, out, "2 dependents")
	assert.Contains(t, out, "ports 5432")
	assert.Contains(t, out, "docker compose up db")
	// A service with no label falls back to its id, and no state renders as "unknown"
	// rather than as an empty column that reads like a healthy blank.
	assert.Contains(t, out, "svc-bare")
	assert.Contains(t, out, "unknown")
	assert.Contains(t, out, "0 dependents")

	assert.Contains(t, out, "symbol indexes (2)")
	assert.Contains(t, out, "up-to-date")
	assert.Contains(t, out, "not-indexed")
	// The workspace-root project renders as its directory name, never as a bare ".".
	assert.Contains(t, out, "named-root")

	assert.Contains(t, out, "locks held:")
	assert.Contains(t, out, "pid 999")
	assert.Contains(t, out, "magus run build")
}

func TestPrintStatusTextOmitsWhatWasNotMeasured(t *testing.T) {
	var buf bytes.Buffer
	printStatusText(&buf, types.StatusReport{}, false, 0)
	out := buf.String()

	assert.Contains(t, out, "daemon: off")
	assert.NotContains(t, out, "proc servers")
	assert.NotContains(t, out, "shared services")
	assert.NotContains(t, out, "symbol indexes")
	assert.NotContains(t, out, "locks held")
	assert.NotContains(t, out, "mcp endpoint")
}

// TestPrintStatusTextPoolWithoutTargetDetail distinguishes the two empty-list readings the
// pool block has to keep apart: nothing is running, versus work is running but this
// reporter cannot see what.
func TestPrintStatusTextPoolWithoutTargetDetail(t *testing.T) {
	t.Run("idle", func(t *testing.T) {
		var buf bytes.Buffer
		printStatusText(&buf, types.StatusReport{Pool: &types.StatusOutput{Capacity: 4}}, false, 0)
		assert.Contains(t, buf.String(), "nothing running")
		assert.Contains(t, buf.String(), "pool pid 0")
	})

	t.Run("busy but blind", func(t *testing.T) {
		var buf bytes.Buffer
		printStatusText(&buf, types.StatusReport{Pool: &types.StatusOutput{Capacity: 4, Running: 2}}, false, 0)
		assert.Contains(t, buf.String(), "local work active; detailed target data unavailable")
	})
}

// TestPrintStatusTextVerboseAddsTheBuildBlock pins the -v gate: the build block is
// diagnostic detail, absent from the default frame.
func TestPrintStatusTextVerboseAddsTheBuildBlock(t *testing.T) {
	prev := global.verbose
	t.Cleanup(func() { global.verbose = prev })

	var quiet bytes.Buffer
	global.verbose = 0
	printStatusText(&quiet, types.StatusReport{}, false, 0)
	assert.NotContains(t, quiet.String(), "selfupdate")

	var loud bytes.Buffer
	global.verbose = 1
	printStatusText(&loud, types.StatusReport{Build: types.BuildStatus{SelfUpdate: true}}, false, 0)
	assert.Contains(t, loud.String(), "selfupdate")
	assert.Contains(t, loud.String(), "engine")
}

// TestDrawPoolGridDistinguishesEverySlotKind covers the four cellState arms through the
// glyph each one paints. The counts are chosen so both asymmetries appear: a pool wider
// than the machine, and a machine wider than the pool.
func TestDrawPoolGridDistinguishesEverySlotKind(t *testing.T) {
	t.Run("oversubscribed pool", func(t *testing.T) {
		var buf bytes.Buffer
		pool := &types.StatusOutput{Mode: "daemon", ParentPID: 7, DaemonVersion: "v9", Capacity: 12, Running: 2, Available: 10, Queued: 1}
		drawPoolGrid(&buf, pool, 4, 0)
		out := buf.String()

		assert.Contains(t, out, "daemon")
		assert.Contains(t, out, "pid 7")
		assert.Contains(t, out, "v9")
		assert.Contains(t, out, "2/12 running")
		assert.Contains(t, out, "10 available")
		assert.Contains(t, out, "4 cpu")
		assert.Contains(t, out, "(+1 queued)")
		assert.Contains(t, out, "running  ○ idle")
		// 12 slots over 8 columns is two rows; the second is padded, never truncated.
		assert.GreaterOrEqual(t, strings.Count(out, "\n"), 5)
	})

	t.Run("machine wider than the pool", func(t *testing.T) {
		var buf bytes.Buffer
		drawPoolGrid(&buf, &types.StatusOutput{Capacity: 2, Running: 1, Available: 1}, 8, 3)
		out := buf.String()

		assert.Contains(t, out, "pool")
		assert.Contains(t, out, "1/2 running")
		// An idle in-pool slot is painted bare; a CPU thread outside the pool is dimmed,
		// so the reset sequence sits between its glyph and the following space.
		assert.Contains(t, out, "○ ")
		assert.Contains(t, out, tty.Colorize("·", sgrPoolIdle))
		assert.NotContains(t, out, "queued")
	})

	t.Run("no slots at all draws nothing", func(t *testing.T) {
		var buf bytes.Buffer
		drawPoolGrid(&buf, &types.StatusOutput{}, 0, 0)
		assert.Empty(t, buf.String())
	})

	t.Run("running targets get the tree and a spinner frame", func(t *testing.T) {
		var buf bytes.Buffer
		pool := &types.StatusOutput{Capacity: 2, Running: 1, RunningTargets: []types.StatusRunningTarget{
			{Args: []string{"run", "build", "web"}, Workspace: "/repos/alpha"},
		}}
		drawPoolGrid(&buf, pool, 2, 1)
		out := buf.String()

		assert.Contains(t, out, "running")
		assert.Contains(t, out, "web")
		assert.Contains(t, out, spinnerFrames[1])
	})
}

func TestPoolHeader(t *testing.T) {
	plain := poolHeader(&types.StatusOutput{ParentPID: 1, Capacity: 2, Available: 2}, 4)
	assert.Contains(t, plain, "pool")
	assert.Contains(t, plain, "pid 1")
	assert.Contains(t, plain, "0/2 running")
	assert.Contains(t, plain, "2 available")
	assert.Contains(t, plain, "4 cpu")
	assert.NotContains(t, plain, "queued")

	busy := poolHeader(&types.StatusOutput{Mode: "daemon", ParentPID: 1, DaemonVersion: "v1", Queued: 5}, 4)
	assert.Contains(t, busy, "daemon")
	assert.Contains(t, busy, "v1")
	assert.Contains(t, busy, "(+5 queued)")
}

// TestPrintStatusCompactStaysOneLine pins the sidebar contract: one line, no ANSI, oldest
// work first, and the tail collapsed rather than allowed to run past the pane.
func TestPrintStatusCompactStaysOneLine(t *testing.T) {
	now := time.Now()

	t.Run("no daemon", func(t *testing.T) {
		var buf bytes.Buffer
		printStatusCompact(&buf, types.StatusReport{}, now)
		assert.Equal(t, "daemon: off\n", buf.String())
	})

	t.Run("a full report", func(t *testing.T) {
		var buf bytes.Buffer
		printStatusCompact(&buf, statusFixture(now), now)
		out := buf.String()

		require.Equal(t, 1, strings.Count(out, "\n"))
		assert.NotContains(t, out, "\x1b")
		assert.Contains(t, out, "daemon")
		assert.Contains(t, out, "2/4 running")
		assert.Contains(t, out, "+3 queued")
		assert.Contains(t, out, "1 ws")
		assert.Contains(t, out, "services 1/2 active, 2 dependents")
		// Two workspaces are in flight, so each entry is qualified by its own.
		assert.Contains(t, out, "alpha/web:build")
		assert.Contains(t, out, "beta/api:test")
		// The 90s build outranks the 3s test, and the durationless entry sorts last.
		assert.Less(t, strings.Index(out, "web:build"), strings.Index(out, "api:test"))
		// A serving endpoint is the steady state and must not spend width in the line.
		assert.NotContains(t, out, "mcp ")
	})

	t.Run("an idle pool says idle", func(t *testing.T) {
		var buf bytes.Buffer
		printStatusCompact(&buf, types.StatusReport{Pool: &types.StatusOutput{Capacity: 4}}, now)
		assert.Contains(t, buf.String(), "0/4 idle")
	})

	t.Run("a degraded endpoint earns its width", func(t *testing.T) {
		var buf bytes.Buffer
		printStatusCompact(&buf, types.StatusReport{
			Pool:        &types.StatusOutput{Capacity: 1},
			MCPEndpoint: &types.MCPEndpointStatus{State: "unreachable"},
		}, now)
		assert.Contains(t, buf.String(), "mcp unreachable")
	})
}

func TestCompactRunningPartsCollapsesTheTail(t *testing.T) {
	now := time.Now()
	var targets []types.StatusRunningTarget
	for i := range 6 {
		targets = append(targets, types.StatusRunningTarget{
			Args:      []string{"run", "build", "p" + string(rune('a'+i))},
			StartedAt: now.Add(-time.Duration(i+1) * time.Minute),
		})
	}

	parts := compactRunningParts(targets, now)
	require.Len(t, parts, compactRunningMax+1)
	assert.Equal(t, "+3 more", parts[compactRunningMax])
	// One workspace across the set, so no entry is qualified by it.
	assert.NotContains(t, parts[0], "/")

	assert.Nil(t, compactRunningParts(nil, now))
}

func TestDurationOfTreatsAnUnsetStartAsZero(t *testing.T) {
	now := time.Now()
	assert.Zero(t, durationOf(types.StatusRunningTarget{}, now))
	assert.Equal(t, time.Minute, durationOf(types.StatusRunningTarget{StartedAt: now.Add(-time.Minute)}, now))
}

func TestFormatCompactRunningTargetNamesTheUnparseable(t *testing.T) {
	now := time.Now()

	assert.Equal(t, "web:build(30s)", formatCompactRunningTarget(
		types.StatusRunningTarget{Args: []string{"run", "build", "web"}, StartedAt: now.Add(-30 * time.Second)}, false, now))

	assert.Equal(t, "alpha/web:build", formatCompactRunningTarget(
		types.StatusRunningTarget{Args: []string{"run", "build", "web"}, Workspace: "/repos/alpha"}, true, now))

	assert.Equal(t, "?:?", formatCompactRunningTarget(types.StatusRunningTarget{}, false, now))
}

func TestCompactServiceToken(t *testing.T) {
	assert.Equal(t, "", compactServiceToken(nil))
	assert.Equal(t, "services 0/1 active, 1 dependent",
		compactServiceToken([]types.StatusService{{State: types.ServiceIdle, Dependents: 1}}))
	assert.Equal(t, "services 2/2 active, 3 dependents", compactServiceToken([]types.StatusService{
		{State: types.ServiceRunning, Dependents: 1},
		{State: types.ServiceStarting, Dependents: 2},
	}))
}

func TestGridEnabledNeedsATerminalAndColor(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	assert.True(t, gridEnabled(OutputOptions{Format: outputText}, true))
	assert.False(t, gridEnabled(OutputOptions{Format: outputText}, false))
	assert.False(t, gridEnabled(OutputOptions{Format: outputJSON}, true))

	t.Setenv("NO_COLOR", "1")
	assert.False(t, gridEnabled(OutputOptions{Format: outputText}, true))
}

// TestWriteStatusStructuredFormats covers the dispatch: a machine-readable format bypasses
// every text renderer above and emits the report itself.
func TestWriteStatusStructuredFormats(t *testing.T) {
	// --tee is process-global, so a leftover value from another test would send this
	// render at a file that may no longer be writable.
	prevTee := global.tee
	t.Cleanup(func() { global.tee = prevTee })
	global.tee = ""

	r := types.StatusReport{Cache: types.CacheStatus{Dir: "/tmp/c"}}

	t.Run("json", func(t *testing.T) {
		out := captureStdout(t, func() {
			require.NoError(t, writeStatus(io.Discard, r, OutputOptions{Format: outputJSON}, 0, false))
		})
		assert.Contains(t, out, `"dir": "/tmp/c"`)
	})

	t.Run("yaml", func(t *testing.T) {
		out := captureStdout(t, func() {
			require.NoError(t, writeStatus(io.Discard, r, OutputOptions{Format: outputYAML}, 0, false))
		})
		assert.Contains(t, out, "dir: /tmp/c")
	})

	t.Run("compact wins over the text frame", func(t *testing.T) {
		var buf bytes.Buffer
		require.NoError(t, writeStatus(&buf, r, OutputOptions{Format: outputText}, 0, true))
		assert.Equal(t, "daemon: off\n", buf.String())
	})
}

func TestTruncateBoundsALabel(t *testing.T) {
	assert.Equal(t, "abc", truncate("abc", 10))
	assert.LessOrEqual(t, len(truncate(strings.Repeat("x", 100), 10)), 10)
}
