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
			Mode: "daemon", Capacity: 8, InUse: 0,
		}},
		"daemon · 0/8 idle\n")

	assertCompact("proc-server label",
		statusReport{Pool: &types.StatusOutput{
			Mode: "proc", Capacity: 8, InUse: 1,
			Calls: []types.StatusCall{
				{Args: []string{"test", "web"}, Workspace: "/w", StartedAt: at(400 * time.Millisecond)},
			},
		}},
		"pool · 1/8 busy · web:test(0.4s)\n")

	assertCompact("daemon busy with calls, sorted oldest first",
		statusReport{Pool: &types.StatusOutput{
			Mode: "daemon", Capacity: 8, InUse: 3,
			Calls: []types.StatusCall{
				{Args: []string{"test", "ui"}, Workspace: "/w", StartedAt: at(500 * time.Millisecond)},
				{Args: []string{"build", "api"}, Workspace: "/w", StartedAt: at(2100 * time.Millisecond)},
				{Args: []string{"lint", "ledger"}, Workspace: "/w", StartedAt: at(300 * time.Millisecond)},
			},
			Workspaces: []types.StatusWorkspace{{Root: "/w", LastAccess: now}},
		}},
		"daemon · 3/8 busy · api:build(2.1s) · ui:test(0.5s) · ledger:lint(0.3s) · 1 ws\n")

	assertCompact("daemon waiting and overflow inflight",
		statusReport{Pool: &types.StatusOutput{
			Mode: "daemon", Capacity: 8, InUse: 8, Waiting: 2,
			Calls: []types.StatusCall{
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
		"daemon · 8/8 busy · +2 waiting · api:build(15s) · ui:test(4.0s) · ledger:lint(2.0s) · +2 more · 2 ws\n")

	assertCompact("multi-workspace inflight prefixes ws",
		statusReport{Pool: &types.StatusOutput{
			Mode: "daemon", Capacity: 4, InUse: 2,
			Calls: []types.StatusCall{
				{Args: []string{"build", "api"}, Workspace: "/srv/alpha", StartedAt: at(1 * time.Second)},
				{Args: []string{"test", "ui"}, Workspace: "/srv/beta", StartedAt: at(500 * time.Millisecond)},
			},
		}},
		"daemon · 2/4 busy · alpha/api:build(1.0s) · beta/ui:test(0.5s)\n")

	assertCompact("unparsable args fall back to ?:?",
		statusReport{Pool: &types.StatusOutput{
			Mode: "daemon", Capacity: 4, InUse: 1,
			Calls: []types.StatusCall{{Args: []string{}, Workspace: "/w", StartedAt: at(100 * time.Millisecond)}},
		}},
		"daemon · 1/4 busy · ?:?(0.1s)\n")
}

func TestPrintStatusCompactTruncatesLongLabel(t *testing.T) {
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	long := strings.Repeat("x", 80)
	r := statusReport{Pool: &types.StatusOutput{
		Mode: "daemon", Capacity: 4, InUse: 1,
		Calls: []types.StatusCall{{
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
		assert.LessOrEqual(t, utf8.RuneCountInString(part), compactInflightBudget,
			"part %q exceeds compactInflightBudget=%d", part, compactInflightBudget)
	}
}

// TestStartupNoArgsReturnsExitZero locks the shape of startup(): when args
// is empty it prints usage and returns exit code 0 without dispatching.
// This is the cheapest assertion that exercises the full pre-dispatch path
// without requiring a workspace fixture, so it doubles as a guard against
// the refactor accidentally calling os.Exit directly.
func TestStartupNoArgsReturnsExitZero(t *testing.T) {
	// Suppress any inherited daemon socket so the test doesn't forward to
	// a real magus daemon on the host.
	t.Setenv("MAGUS_DAEMON_SOCKET", "")

	res, code := startup(context.Background(), nil)
	if res.cleanup != nil {
		t.Cleanup(res.cleanup)
	}
	require.Equal(t, 0, code, "startup(nil) exit code")
	assert.Empty(t, res.sub, "startup(nil) sub should be empty (no dispatch)")
}
