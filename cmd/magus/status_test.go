package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
