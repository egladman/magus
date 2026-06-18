package main

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/types"
)

func TestCellState(t *testing.T) {
	assertCell := func(name string, i, inUse, capacity, numCPU int, want cellKind) {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, want, cellState(i, inUse, capacity, numCPU))
		})
	}

	assertCell("active first", 0, 3, 8, 16, cellActive)
	assertCell("active last", 2, 3, 8, 16, cellActive)
	assertCell("idle first after active", 3, 3, 8, 16, cellIdle)
	assertCell("idle last", 7, 3, 8, 16, cellIdle)
	assertCell("out-of-pool first", 8, 3, 8, 16, cellOutOfPool)
	assertCell("out-of-pool last", 15, 3, 8, 16, cellOutOfPool)
	assertCell("no active workers", 0, 0, 8, 16, cellIdle)
	assertCell("full capacity active", 7, 8, 8, 16, cellActive)
	assertCell("single cpu", 0, 1, 1, 1, cellActive)
	assertCell("over-subscribed active", 0, 2, 4, 2, cellActive)
	assertCell("over-subscribed idle in pool beyond cpu", 3, 2, 4, 2, cellOverSubscribed)
	assertCell("over-subscribed in pool at cpu boundary", 2, 2, 4, 2, cellOverSubscribed)
	assertCell("capacity equals numcpu idle", 4, 2, 8, 8, cellIdle)
	assertCell("capacity equals numcpu out", 8, 2, 8, 8, cellOutOfPool)
}

func TestParseInflight(t *testing.T) {
	assertInflight := func(name string, args []string, wantProj, wantName string) {
		t.Run(name, func(t *testing.T) {
			gotProj, gotTarget := parseInflight(args)
			assert.Equal(t, wantProj, gotProj)
			assert.Equal(t, wantName, gotTarget)
		})
	}

	assertInflight("run target only", []string{"run", "build"}, "", "build")
	assertInflight("run target + project", []string{"run", "build", "api"}, "api", "build")
	assertInflight("run target with charm", []string{"run", "lint:read", "api"}, "api", "lint")
	assertInflight("build subcommand bare", []string{"build", "api"}, "api", "build")
	assertInflight("test subcommand + project", []string{"test", "api"}, "api", "test")
	assertInflight("lint subcommand no project", []string{"lint"}, "", "lint")
	assertInflight("global flag before subcommand", []string{"-x", "run", "build", "api"}, "api", "build")
	assertInflight("unknown subcommand", []string{"weirdcmd", "thing"}, "thing", "weirdcmd")
	assertInflight("empty args", []string{}, "", "")
	assertInflight("only flags", []string{"-x", "--y"}, "", "")
}

func TestDrawInflightTreeGrouping(t *testing.T) {
	inflight := []types.StatusCall{
		{Args: []string{"build", "api"}, Workspace: "/home/u/foo"},
		{Args: []string{"test", "api"}, Workspace: "/home/u/foo"},
		{Args: []string{"test", "pkg/x"}, Workspace: "/home/u/foo"},
		{Args: []string{"lint", "web"}, Workspace: "/home/u/bar"},
	}
	var buf bytes.Buffer
	drawInflightTree(&buf, inflight, time.Now())
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

func TestDrawInflightTreeDuration(t *testing.T) {
	now := time.Now()
	inflight := []types.StatusCall{
		{Args: []string{"build", "api"}, Workspace: "/home/u/foo", StartedAt: now.Add(-3*time.Second - 400*time.Millisecond)},
		{Args: []string{"test", "api"}, Workspace: "/home/u/foo", StartedAt: now.Add(-45 * time.Second)},
	}
	var buf bytes.Buffer
	drawInflightTree(&buf, inflight, now)
	out := buf.String()
	assert.Contains(t, out, "(3.4s)")
	assert.Contains(t, out, "(45s)")
}

func TestDrawInflightTreeSingleWorkspaceCollapses(t *testing.T) {
	inflight := []types.StatusCall{
		{Args: []string{"build", "api"}, Workspace: "/home/u/foo"},
		{Args: []string{"test", "api"}, Workspace: "/home/u/foo"},
	}
	var buf bytes.Buffer
	drawInflightTree(&buf, inflight, time.Now())
	out := buf.String()

	assert.NotContains(t, out, "foo", "single-workspace output should not show workspace label")
	assert.Contains(t, out, "api")
	assert.Contains(t, out, "build")
	assert.Contains(t, out, "test")
}
