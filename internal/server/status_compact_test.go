package server

import (
	"bytes"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/daemon"
)

func TestPrintStatusCompact(t *testing.T) {
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	at := func(ago time.Duration) time.Time { return now.Add(-ago) }

	assertCompact := func(name string, snapshot daemon.Snapshot, want string) {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			PrintStatusCompact(&buf, snapshot, now)
			assert.Equal(t, want, buf.String())
			assert.Equal(t, 1, strings.Count(buf.String(), "\n"), "compact must emit exactly one line")
		})
	}

	server := &daemon.Server{PID: 1}
	assertCompact("nothing running",
		daemon.Snapshot{PoolError: "no running magus proc server found"},
		"no broker · no server\n")

	// Off is the setting, not an absence: a run under it never starts one.
	assertCompact("the broker policy is off",
		daemon.Snapshot{BrokerPolicy: types.BrokerOff},
		"broker off · no server\n")

	assertCompact("a broker that measured the host shows its seated slots",
		daemon.Snapshot{Broker: &daemon.Broker{Capacity: types.MachineSnapshot{BudgetSlots: 8, HeldSlots: 3}}},
		"broker 3/8 slots · no server\n")

	assertCompact("server idle",
		daemon.Snapshot{Server: server, Pool: &daemon.Output{Capacity: 8, Running: 0}},
		"no broker · server up · pool 0/8 idle\n")

	assertCompact("a per-process pool without a server",
		daemon.Snapshot{Pool: &daemon.Output{
			Capacity: 8, Running: 1,
			RunningTargets: []daemon.RunningTarget{
				{Args: []string{"test", "web"}, Workspace: "/w", StartedAt: at(400 * time.Millisecond)},
			},
		}},
		"no broker · no server · pool 1/8 running · web:test(0.4s)\n")

	assertCompact("running with targets, sorted oldest first",
		daemon.Snapshot{Server: server, Pool: &daemon.Output{
			Capacity: 8, Running: 3,
			RunningTargets: []daemon.RunningTarget{
				{Args: []string{"test", "ui"}, Workspace: "/w", StartedAt: at(500 * time.Millisecond)},
				{Args: []string{"build", "api"}, Workspace: "/w", StartedAt: at(2100 * time.Millisecond)},
				{Args: []string{"lint", "ledger"}, Workspace: "/w", StartedAt: at(300 * time.Millisecond)},
			},
			Workspaces: []daemon.Workspace{{Root: "/w", LastAccess: now}},
		}},
		"no broker · server up · pool 3/8 running · api:build(2.1s) · ui:test(0.5s) · ledger:lint(0.3s) · 1 workspace\n")

	assertCompact("queued and overflow running",
		daemon.Snapshot{Server: server, Pool: &daemon.Output{
			Capacity: 8, Running: 8, Queued: 2,
			RunningTargets: []daemon.RunningTarget{
				{Args: []string{"build", "api"}, Workspace: "/w", StartedAt: at(15 * time.Second)},
				{Args: []string{"test", "ui"}, Workspace: "/w", StartedAt: at(4 * time.Second)},
				{Args: []string{"lint", "ledger"}, Workspace: "/w", StartedAt: at(2 * time.Second)},
				{Args: []string{"build", "store"}, Workspace: "/w", StartedAt: at(1 * time.Second)},
				{Args: []string{"test", "search"}, Workspace: "/w", StartedAt: at(900 * time.Millisecond)},
			},
			Workspaces: []daemon.Workspace{
				{Root: "/w1", LastAccess: now},
				{Root: "/w2", LastAccess: now},
			},
		}},
		"no broker · server up · pool 8/8 running +2 queued · api:build(15s) · ui:test(4.0s) · ledger:lint(2.0s) · +2 more · 2 workspaces\n")

	assertCompact("multi-workspace running prefixes ws",
		daemon.Snapshot{Server: server, Pool: &daemon.Output{
			Capacity: 4, Running: 2,
			RunningTargets: []daemon.RunningTarget{
				{Args: []string{"build", "api"}, Workspace: "/srv/alpha", StartedAt: at(1 * time.Second)},
				{Args: []string{"test", "ui"}, Workspace: "/srv/beta", StartedAt: at(500 * time.Millisecond)},
			},
		}},
		"no broker · server up · pool 2/4 running · alpha/api:build(1.0s) · beta/ui:test(0.5s)\n")

	assertCompact("the broker's services report activity and dependents",
		daemon.Snapshot{
			Server: server,
			Pool:   &daemon.Output{Capacity: 4},
			Broker: &daemon.Broker{Services: []daemon.Service{
				{State: "running", Dependents: 2},
				{State: "idle", Dependents: 0},
			}},
		},
		"broker up · server up · pool 0/4 idle · services 1/2 active, 2 dependents\n")

	assertCompact("unparsable args fall back to ?:?",
		daemon.Snapshot{Server: server, Pool: &daemon.Output{
			Capacity: 4, Running: 1,
			RunningTargets: []daemon.RunningTarget{{Args: []string{}, Workspace: "/w", StartedAt: at(100 * time.Millisecond)}},
		}},
		"no broker · server up · pool 1/4 running · ?:?(0.1s)\n")
}

func TestPrintStatusCompactTruncatesLongLabel(t *testing.T) {
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	long := strings.Repeat("x", 80)
	r := daemon.Snapshot{Pool: &daemon.Output{
		Capacity: 4, Running: 1,
		RunningTargets: []daemon.RunningTarget{{
			Args:      []string{"build", long},
			Workspace: "/w",
			StartedAt: now.Add(-time.Second),
		}},
	}}
	var buf bytes.Buffer
	PrintStatusCompact(&buf, r, now)
	out := buf.String()
	assert.Contains(t, out, "...", "expected truncation ellipsis")
	assert.NotContains(t, out, "…", "the ellipsis is three ASCII dots, not U+2026")
	for _, part := range strings.Split(strings.TrimRight(out, "\n"), " · ") {
		assert.LessOrEqual(t, utf8.RuneCountInString(part), compactRunningBudget,
			"part %q exceeds compactRunningBudget=%d", part, compactRunningBudget)
	}
}

func TestCompactMCPToken(t *testing.T) {
	assert.Empty(t, compactMCPToken(nil))
	assert.Empty(t, compactMCPToken(&daemon.MCPEndpoint{State: "serving"}), "serving is the steady state; omitted from the compact line")
	assert.Equal(t, "mcp unreachable", compactMCPToken(&daemon.MCPEndpoint{State: "unreachable"}))
	assert.Equal(t, "mcp not-ready", compactMCPToken(&daemon.MCPEndpoint{State: "not-ready"}))
}

// TestPrintStatusCompactStaysOneLine pins the sidebar contract: one line, no ANSI, oldest
// work first, and the tail collapsed rather than allowed to run past the pane.
func TestPrintStatusCompactStaysOneLine(t *testing.T) {
	now := time.Now()

	t.Run("nothing running", func(t *testing.T) {
		var buf bytes.Buffer
		PrintStatusCompact(&buf, daemon.Snapshot{}, now)
		assert.Equal(t, "no broker · no server\n", buf.String())
	})

	t.Run("a full snapshot", func(t *testing.T) {
		var buf bytes.Buffer
		PrintStatusCompact(&buf, statusFixture(now), now)
		out := buf.String()

		require.Equal(t, 1, strings.Count(out, "\n"))
		assert.NotContains(t, out, "\x1b")
		assert.True(t, strings.HasPrefix(out, "broker 2/8 slots · server up · pool 2/4 running +3 queued"), out)
		assert.Contains(t, out, "1 workspace")
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
		PrintStatusCompact(&buf, daemon.Snapshot{Pool: &daemon.Output{Capacity: 4}}, now)
		assert.Contains(t, buf.String(), "0/4 idle")
	})

	t.Run("a degraded endpoint earns its width", func(t *testing.T) {
		var buf bytes.Buffer
		PrintStatusCompact(&buf, daemon.Snapshot{
			Pool:        &daemon.Output{Capacity: 1},
			MCPEndpoint: &daemon.MCPEndpoint{State: "unreachable"},
		}, now)
		assert.Contains(t, buf.String(), "mcp unreachable")
	})
}

func TestCompactRunningPartsCollapsesTheTail(t *testing.T) {
	now := time.Now()
	var targets []daemon.RunningTarget
	for i := range 6 {
		targets = append(targets, daemon.RunningTarget{
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
	assert.Zero(t, runningDuration(daemon.RunningTarget{}, now))
	assert.Equal(t, time.Minute, runningDuration(daemon.RunningTarget{StartedAt: now.Add(-time.Minute)}, now))
}

func TestFormatCompactRunningTargetNamesTheUnparseable(t *testing.T) {
	now := time.Now()

	assert.Equal(t, "web:build(30s)", formatCompactRunningTarget(
		daemon.RunningTarget{Args: []string{"run", "build", "web"}, StartedAt: now.Add(-30 * time.Second)}, false, now))

	assert.Equal(t, "alpha/web:build", formatCompactRunningTarget(
		daemon.RunningTarget{Args: []string{"run", "build", "web"}, Workspace: "/repos/alpha"}, true, now))

	assert.Equal(t, "?:?", formatCompactRunningTarget(daemon.RunningTarget{}, false, now))
}

func TestCompactServiceToken(t *testing.T) {
	assert.Equal(t, "", compactServiceToken(nil))
	assert.Equal(t, "services 0/1 active, 1 dependent",
		compactServiceToken([]daemon.Service{{State: daemon.ServiceIdle, Dependents: 1}}))
	assert.Equal(t, "services 2/2 active, 3 dependents", compactServiceToken([]daemon.Service{
		{State: daemon.ServiceRunning, Dependents: 1},
		{State: daemon.ServiceStarting, Dependents: 2},
	}))
}
