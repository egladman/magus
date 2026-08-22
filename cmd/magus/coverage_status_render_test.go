package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/types"
)

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
