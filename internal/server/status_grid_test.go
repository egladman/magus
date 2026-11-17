package server

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/libs/termkit"
	"github.com/egladman/magus/types/daemon"
)

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
	running := []daemon.RunningTarget{
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

func TestDrawRunningTreeDuration(t *testing.T) {
	now := time.Now()
	running := []daemon.RunningTarget{
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
	running := []daemon.RunningTarget{
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

// TestDrawPoolGridDistinguishesEverySlotKind covers the four cellState arms through the
// glyph each one paints. The counts are chosen so both asymmetries appear: a pool wider
// than the machine, and a machine wider than the pool.
func TestDrawPoolGridDistinguishesEverySlotKind(t *testing.T) {
	t.Run("oversubscribed pool", func(t *testing.T) {
		var buf bytes.Buffer
		pool := &daemon.Output{ParentPID: 7, Version: "v9", Capacity: 12, Running: 2, Available: 10, Queued: 1}
		drawPoolGrid(&buf, pool, 4, 0)
		out := buf.String()

		assert.Contains(t, out, "pool")
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
		drawPoolGrid(&buf, &daemon.Output{Capacity: 2, Running: 1, Available: 1}, 8, 3)
		out := buf.String()

		assert.Contains(t, out, "pool")
		assert.Contains(t, out, "1/2 running")
		// An idle in-pool slot is painted bare; a CPU thread outside the pool is dimmed,
		// so the reset sequence sits between its glyph and the following space.
		assert.Contains(t, out, "○ ")
		assert.Contains(t, out, termkit.Colorize("·", sgrPoolIdle))
		assert.NotContains(t, out, "queued")
	})

	t.Run("no slots at all draws nothing", func(t *testing.T) {
		var buf bytes.Buffer
		drawPoolGrid(&buf, &daemon.Output{}, 0, 0)
		assert.Empty(t, buf.String())
	})

	t.Run("running targets get the tree and a spinner frame", func(t *testing.T) {
		var buf bytes.Buffer
		pool := &daemon.Output{Capacity: 2, Running: 1, RunningTargets: []daemon.RunningTarget{
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
	plain := poolHeader(&daemon.Output{ParentPID: 1, Capacity: 2, Available: 2}, 4)
	assert.Contains(t, plain, "pool")
	assert.Contains(t, plain, "pid 1")
	assert.Contains(t, plain, "0/2 running")
	assert.Contains(t, plain, "2 available")
	assert.Contains(t, plain, "4 cpu")
	assert.NotContains(t, plain, "queued")

	busy := poolHeader(&daemon.Output{ParentPID: 1, Version: "v1", Queued: 5}, 4)
	assert.Contains(t, busy, "v1")
	assert.Contains(t, busy, "(+5 queued)")
}

func TestTruncateBoundsALabel(t *testing.T) {
	assert.Equal(t, "abc", truncate("abc", 10))
	assert.LessOrEqual(t, len(truncate(strings.Repeat("x", 100), 10)), 10)
}
