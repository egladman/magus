package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/egladman/magus/types"
)

func TestPrintStatusCompact(t *testing.T) {
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	at := func(ago time.Duration) time.Time { return now.Add(-ago) }

	tests := []struct {
		name   string
		report statusReport
		want   string
	}{
		{
			name:   "no parent",
			report: statusReport{PoolError: "no running magus proc server found"},
			want:   "daemon: off\n",
		},
		{
			name: "daemon idle",
			report: statusReport{Pool: &types.StatusOutput{
				Mode: "daemon", Capacity: 8, InUse: 0,
			}},
			want: "daemon · 0/8 idle\n",
		},
		{
			name: "proc-server label",
			report: statusReport{Pool: &types.StatusOutput{
				Mode: "proc", Capacity: 8, InUse: 1,
				Calls: []types.StatusCall{
					{Args: []string{"test", "web"}, Workspace: "/w", StartedAt: at(400 * time.Millisecond)},
				},
			}},
			want: "pool · 1/8 busy · web:test(0.4s)\n",
		},
		{
			name: "daemon busy with calls, sorted oldest first",
			report: statusReport{Pool: &types.StatusOutput{
				Mode: "daemon", Capacity: 8, InUse: 3,
				Calls: []types.StatusCall{
					{Args: []string{"test", "ui"}, Workspace: "/w", StartedAt: at(500 * time.Millisecond)},
					{Args: []string{"build", "api"}, Workspace: "/w", StartedAt: at(2100 * time.Millisecond)},
					{Args: []string{"lint", "ledger"}, Workspace: "/w", StartedAt: at(300 * time.Millisecond)},
				},
				Workspaces: []types.StatusWorkspace{{Root: "/w", LastAccess: now}},
			}},
			want: "daemon · 3/8 busy · api:build(2.1s) · ui:test(0.5s) · ledger:lint(0.3s) · 1 ws\n",
		},
		{
			name: "daemon waiting and overflow inflight",
			report: statusReport{Pool: &types.StatusOutput{
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
			want: "daemon · 8/8 busy · +2 waiting · api:build(15s) · ui:test(4.0s) · ledger:lint(2.0s) · +2 more · 2 ws\n",
		},
		{
			name: "multi-workspace inflight prefixes ws",
			report: statusReport{Pool: &types.StatusOutput{
				Mode: "daemon", Capacity: 4, InUse: 2,
				Calls: []types.StatusCall{
					{Args: []string{"build", "api"}, Workspace: "/srv/alpha", StartedAt: at(1 * time.Second)},
					{Args: []string{"test", "ui"}, Workspace: "/srv/beta", StartedAt: at(500 * time.Millisecond)},
				},
			}},
			want: "daemon · 2/4 busy · alpha/api:build(1.0s) · beta/ui:test(0.5s)\n",
		},
		{
			name: "unparsable args fall back to ?:?",
			report: statusReport{Pool: &types.StatusOutput{
				Mode: "daemon", Capacity: 4, InUse: 1,
				Calls: []types.StatusCall{{Args: []string{}, Workspace: "/w", StartedAt: at(100 * time.Millisecond)}},
			}},
			want: "daemon · 1/4 busy · ?:?(0.1s)\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			printStatusCompact(&buf, tc.report, now)
			if got := buf.String(); got != tc.want {
				t.Errorf("compact mismatch\n got: %q\nwant: %q", got, tc.want)
			}
			if strings.Count(buf.String(), "\n") != 1 {
				t.Errorf("compact must emit exactly one line, got: %q", buf.String())
			}
		})
	}
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
	if !strings.Contains(out, "…") {
		t.Errorf("expected truncation ellipsis in %q", out)
	}
	for _, part := range strings.Split(strings.TrimRight(out, "\n"), " · ") {
		if n := utf8.RuneCountInString(part); n > compactInflightBudget {
			t.Errorf("part %q has %d runes, exceeds compactInflightBudget=%d", part, n, compactInflightBudget)
		}
	}
}
