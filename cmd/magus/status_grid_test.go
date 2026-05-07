package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/types"
)

func TestCellState(t *testing.T) {
	tests := []struct {
		name     string
		i        int
		inUse    int
		capacity int
		numCPU   int
		want     cellKind
	}{
		{"active first", 0, 3, 8, 16, cellActive},
		{"active last", 2, 3, 8, 16, cellActive},
		{"idle first after active", 3, 3, 8, 16, cellIdle},
		{"idle last", 7, 3, 8, 16, cellIdle},
		{"out-of-pool first", 8, 3, 8, 16, cellOutOfPool},
		{"out-of-pool last", 15, 3, 8, 16, cellOutOfPool},
		{"no active workers", 0, 0, 8, 16, cellIdle},
		{"full capacity active", 7, 8, 8, 16, cellActive},
		{"single cpu", 0, 1, 1, 1, cellActive},
		{"over-subscribed active", 0, 2, 4, 2, cellActive},
		{"over-subscribed idle in pool beyond cpu", 3, 2, 4, 2, cellOverSubscribed},
		{"over-subscribed in pool at cpu boundary", 2, 2, 4, 2, cellOverSubscribed},
		{"capacity equals numcpu idle", 4, 2, 8, 8, cellIdle},
		{"capacity equals numcpu out", 8, 2, 8, 8, cellOutOfPool},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cellState(tt.i, tt.inUse, tt.capacity, tt.numCPU)
			if got != tt.want {
				t.Errorf("cellState(%d, inUse=%d, cap=%d, cpu=%d) = %v, want %v",
					tt.i, tt.inUse, tt.capacity, tt.numCPU, got, tt.want)
			}
		})
	}
}

func TestParseInflight(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantProj string
		wantName string
	}{
		{"run target only", []string{"run", "build"}, "", "build"},
		{"run target + project", []string{"run", "build", "api"}, "api", "build"},
		{"run target with charm", []string{"run", "lint:read", "api"}, "api", "lint"},
		{"build subcommand bare", []string{"build", "api"}, "api", "build"},
		{"test subcommand + project", []string{"test", "api"}, "api", "test"},
		{"lint subcommand no project", []string{"lint"}, "", "lint"},
		{"global flag before subcommand", []string{"-x", "run", "build", "api"}, "api", "build"},
		{"unknown subcommand", []string{"weirdcmd", "thing"}, "thing", "weirdcmd"},
		{"empty args", []string{}, "", ""},
		{"only flags", []string{"-x", "--y"}, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotProj, gotTarget := parseInflight(tt.args)
			if gotProj != tt.wantProj || gotTarget != tt.wantName {
				t.Errorf("parseInflight(%v) = (%q, %q), want (%q, %q)",
					tt.args, gotProj, gotTarget, tt.wantProj, tt.wantName)
			}
		})
	}
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
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
	// Tree characters present.
	if !strings.Contains(out, "├") || !strings.Contains(out, "└") {
		t.Errorf("tree characters missing in output:\n%s", out)
	}
}

func TestFormatDur(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"zero", 0, ""},
		{"negative", -time.Second, ""},
		{"sub-second", 300 * time.Millisecond, "0.3s"},
		{"under 10s", 3*time.Second + 400*time.Millisecond, "3.4s"},
		{"10s boundary", 10 * time.Second, "10s"},
		{"under 1m", 47 * time.Second, "47s"},
		{"1m boundary", time.Minute, "1m0s"},
		{"minutes+seconds", 2*time.Minute + 17*time.Second, "2m17s"},
		{"1h boundary", time.Hour, "1h0m"},
		{"hours+minutes", 3*time.Hour + 5*time.Minute, "3h5m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatDur(tt.d); got != tt.want {
				t.Errorf("formatDur(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
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
	if !strings.Contains(out, "(3.4s)") {
		t.Errorf("expected (3.4s) in output:\n%s", out)
	}
	if !strings.Contains(out, "(45s)") {
		t.Errorf("expected (45s) in output:\n%s", out)
	}
}

func TestDrawInflightTreeSingleWorkspaceCollapses(t *testing.T) {
	inflight := []types.StatusCall{
		{Args: []string{"build", "api"}, Workspace: "/home/u/foo"},
		{Args: []string{"test", "api"}, Workspace: "/home/u/foo"},
	}
	var buf bytes.Buffer
	drawInflightTree(&buf, inflight, time.Now())
	out := buf.String()

	if strings.Contains(out, "foo") {
		t.Errorf("single-workspace output should not show workspace label:\n%s", out)
	}
	if !strings.Contains(out, "api") || !strings.Contains(out, "build") || !strings.Contains(out, "test") {
		t.Errorf("expected api/build/test in output:\n%s", out)
	}
}
