package server

import (
	"bytes"
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/broker"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/libs/termkit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/daemon"
)

func TestPrintSilentLeases(t *testing.T) {
	silent := []guard.SilentLease{
		{Lease: "job-a", Session: "s1", SilentSeconds: 2700},
		{Lease: "job-b", Session: "s2", SilentSeconds: 4000, BackgroundNote: "the host named no transcript, so background commands were not counted"},
	}
	var buf bytes.Buffer
	PrintSilentLeases(&buf, silent, nil)
	out := buf.String()
	assert.Contains(t, out, "job-a  session s1")
	assert.Contains(t, out, "job-b  session s2")
	assert.Contains(t, out, "no transcript")
	assert.Contains(t, out, "nothing was ended")

	buf.Reset()
	PrintSilentLeases(&buf, nil, nil)
	assert.Empty(t, buf.String(), "no silent lease prints nothing")

	buf.Reset()
	PrintSilentLeases(&buf, nil, errors.New("store unreadable"))
	assert.Contains(t, buf.String(), "not read: store unreadable", "a failed read is said, not dropped")
}

func TestStatusReportInlinesTheSnapshot(t *testing.T) {
	report := StatusReport{
		Snapshot:     daemon.Snapshot{PoolError: "no pool"},
		SilentLeases: []guard.SilentLease{{Lease: "job-a", Session: "s1", SilentSeconds: 2700}},
	}
	raw, err := json.Marshal(report)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Contains(t, got, "silent_leases")
	assert.NotContains(t, got, "StatusSnapshot", "the snapshot's fields sit at the top level")
	assert.Contains(t, string(raw), "no pool")

	raw, err = json.Marshal(StatusReport{})
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "silent_leases", "no silent lease and no error adds no key")
}

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

func TestPrintWorkByOwnerGroupsWhatRunsAndSaysWhatWasStopped(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	PrintWorkByOwner(&out, broker.WorkReport{
		Running: []broker.Work{
			{Kind: broker.WorkRun, Lease: "plan/a", PID: 10, Command: "magus run go-test .", Dir: "/w/a", Since: now.Add(-12 * time.Minute), Slots: 4},
			{Kind: broker.WorkExec, PID: 11, Command: "rg -l TODO", Dir: "/w", Since: now.Add(-3 * time.Minute), Slots: 1, Deadline: now.Add(7 * time.Minute)},
			{Kind: broker.WorkExec, Lease: "plan/a", PID: 12, Command: "sleep 600", Dir: "/w/a", Since: now.Add(-time.Hour), Deadline: now.Add(-time.Second)},
		},
		Ended: []broker.Ended{{Work: broker.Work{Kind: broker.WorkExec, Lease: "plan/b", Command: "rg x"}, Reason: "passed its deadline (2026-10-13T11:58:00Z)", At: now.Add(-2 * time.Minute)}},
	}, now)
	text := out.String()

	assert.Contains(t, text, "work by owner")
	assert.Less(t, strings.Index(text, "  plan/a\n"), strings.Index(text, "  unleased\n"), "owners in the order their oldest work started")
	assert.Equal(t, 1, strings.Count(text, "  plan/a\n"), "one heading per owner")
	assert.Regexp(t, `run\s+pid 10\s+up 12m0s\s+slots 4\s+no deadline\s+magus run go-test \.\s+/w/a`, text)
	assert.Regexp(t, `exec\s+pid 11\s+up 3m0s\s+slots 1\s+deadline in 7m0s\s+rg -l TODO`, text)
	assert.Contains(t, text, "past its deadline")
	assert.Regexp(t, `plan/b\s+exec\s+2m0s ago\s+rg x\s+passed its deadline`, text)

	out.Reset()
	PrintWorkByOwner(&out, broker.WorkReport{}, now)
	assert.Empty(t, out.String(), "nothing runs, nothing to say")
}

func TestClampStatusWatch(t *testing.T) {
	assert.Equal(t, time.Duration(0), ClampStatusWatch(0))
	assert.Equal(t, statusWatchMin, ClampStatusWatch(time.Second))
	assert.Equal(t, statusWatchMin, ClampStatusWatch(statusWatchMin))
	assert.Equal(t, 30*time.Second, ClampStatusWatch(30*time.Second))
}

// A negative --watch reached time.NewTicker, which PANICS at or below zero: the
// clamp guarded on `interval > 0` and the single-snapshot branch above it on
// `== 0`, so anything negative fell between them and took the process down.
func TestClampStatusWatchFloorsANegativeInterval(t *testing.T) {
	for _, d := range []time.Duration{-time.Nanosecond, -time.Second, -time.Hour} {
		assert.Equalf(t, statusWatchMin, ClampStatusWatch(d),
			"ClampStatusWatch(%s) must floor at the minimum, not hand a panic to NewTicker", d)
	}
	// The value the watch loop actually builds its ticker from must be positive.
	assert.Positive(t, ClampStatusWatch(-time.Second))
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

// TestBrokerRowsNameEveryClaim covers the question the rows exist to answer: WHERE the
// host's capacity went. One broker serves every worktree, so a pid alone does not say
// which tree to go and look at; and each row leads with its record type and pid, so awk
// works on it.
func TestBrokerRowsNameEveryClaim(t *testing.T) {
	var buf bytes.Buffer
	PrintBrokerRows(&buf, daemon.Broker{
		PID: 48213,
		Capacity: types.MachineSnapshot{
			BudgetMB: 48 << 10, HeldMB: 10 << 10, BudgetSlots: 8, HeldSlots: 6,
			Holders: []types.MachineClaimant{
				{Project: ".", Target: "test", PID: 41221, MemoryMB: 10 << 10, Slots: 4, Dir: "/tree/polish", Command: "magus affected ci", Since: time.Now().Add(-90 * time.Second)},
				{Project: "docs", Target: "ci", PID: 41999, Slots: 2, Dir: "/tree/hardening"},
			},
		},
		Services: []daemon.Service{{Label: "postgres-15", State: daemon.ServiceRunning, Dependents: 2, Ports: []string{"5432"}}},
	}, types.BrokerRequired, time.Now())
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 5)
	assert.True(t, strings.HasPrefix(lines[0], "broker    48213"), lines[0])
	assert.Contains(t, lines[1], "slots 6/8  mem 10.0 GiB/48.0 GiB  broker: required")
	assert.True(t, strings.HasPrefix(lines[2], "held      41221"), lines[2])
	assert.Contains(t, lines[2], "slots 4  mem 10.0 GiB  (root) test  /tree/polish  magus affected ci")
	assert.True(t, strings.HasPrefix(lines[3], "held      41999"), lines[3])
	assert.Contains(t, lines[3], "docs ci  /tree/hardening")
	assert.Contains(t, lines[4], "service   -      postgres-15  running  deps 2  ports 5432")
}

// Each holder names its owner, and the callers waiting on a seat and the refusals the
// broker answered get rows of their own, so a person sees whose run keeps theirs out.
func TestBrokerRowsNameOwnersWaitersAndRefusals(t *testing.T) {
	now := time.Now()
	var buf bytes.Buffer
	PrintBrokerRows(&buf, daemon.Broker{
		PID: 48213,
		Capacity: types.MachineSnapshot{
			BudgetSlots: 8, HeldSlots: 8,
			Holders: []types.MachineClaimant{{Project: ".", Target: "test", PID: 41221, Slots: 8, Dir: "/tree/polish", Owner: "fix-cache"}},
			Waiting: []types.MachineWaiter{
				{PID: 41300, Project: ".", Target: "lint", Slots: 2, Dir: "/tree/a", Owner: "unleased", Since: now.Add(-time.Minute)},
				{PID: 41301, Project: "docs", Target: "ci", Owner: "job-b", Tree: "root-job", Since: now},
			},
			Refused: []types.MachineAdmission{{
				At: now.Add(-2 * time.Minute), PID: 41400, Project: ".", Target: "ci", Slots: 4, Dir: "/tree/b",
				Owner: "spawned by root", Fits: true,
				Holders: []types.MachineClaimant{{PID: 41221, Owner: "fix-cache"}, {PID: 41222}},
			}},
		},
	}, types.BrokerRequired, now)
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 6)
	assert.Contains(t, lines[2], "(root) test  fix-cache  /tree/polish")
	assert.True(t, strings.HasPrefix(lines[3], "waiting   41300"), lines[3])
	assert.Contains(t, lines[3], "slots 2  (root) lint  unleased  /tree/a")
	assert.Contains(t, lines[4], "docs ci  seat in root-job  job-b")
	assert.True(t, strings.HasPrefix(lines[5], "refused   41400"), lines[5])
	assert.Contains(t, lines[5], "2m0s ago  slots 4  (root) ci  spawned by root  /tree/b  held by pid 41221 fix-cache, pid 41222 owner unknown")
}

// A request the broker holds parked shows as its own row, after the claims keeping it out.
func TestBrokerRowsNameEveryParkedRequest(t *testing.T) {
	var buf bytes.Buffer
	PrintBrokerRows(&buf, daemon.Broker{
		PID: 48213,
		Capacity: types.MachineSnapshot{BudgetSlots: 8, HeldSlots: 8, Waiting: []types.MachineWaiter{
			{Project: "docs", Target: "lint", PID: 41300, MemoryMB: 2 << 10, Slots: 2, Dir: "/tree/polish", Command: "magus run lint docs"},
		}},
	}, types.BrokerRequired, time.Now())
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	require.Len(t, lines, 3)
	assert.Equal(t, "waiting   41300  slots 2  mem 2.0 GiB  docs lint  /tree/polish  magus run lint docs", lines[2])
}

// A claim says where its figure came from, since a measured one sits below what the
// magusfile declares and a reader comparing the two would otherwise see a mismatch.
func TestHolderMemTextNamesTheSizing(t *testing.T) {
	assert.Equal(t, "mem 5.7 GiB measured over 54 runs",
		holderMemText(types.MachineClaimant{MemoryMB: 5879, Sizing: types.MemorySizing{Samples: 54}}))
	assert.Equal(t, "mem 10.0 GiB", holderMemText(types.MachineClaimant{MemoryMB: 10 << 10}), "a declaration reads bare")
	assert.Empty(t, holderMemText(types.MachineClaimant{Slots: 2}), "a slots-only claim has no memory to explain")
}

// TestStatusBreaksDownOnlyHoldersOverAQuarterOfTheBudget: a big claim says
// where its figure came from, and a small one adds no row.
func TestStatusBreaksDownOnlyHoldersOverAQuarterOfTheBudget(t *testing.T) {
	var buf bytes.Buffer
	PrintBrokerRows(&buf, daemon.Broker{
		PID: 1,
		Capacity: types.MachineSnapshot{BudgetMB: 16 << 10, BudgetSlots: 8, Holders: []types.MachineClaimant{
			{Project: ".", Target: "test", PID: 10, MemoryMB: 5 << 10, Sizing: types.MemorySizing{Samples: 54}},
			{Project: ".", Target: "lint", PID: 11, MemoryMB: 1 << 10},
		}},
	}, types.BrokerRequired, time.Now())
	assert.Contains(t, buf.String(), "sized     10  (root) test 5.0 GiB: own peak over 54 runs")
	assert.NotContains(t, buf.String(), "sized     11")
}

// An idle broker still prints its capacity: "nothing is held" is the answer to the
// question people open status to ask, and a silent section reads as a missing feature.
func TestBrokerRowsIdle(t *testing.T) {
	var idle bytes.Buffer
	PrintBrokerRows(&idle, daemon.Broker{PID: 7, Capacity: types.MachineSnapshot{BudgetMB: 48 << 10, BudgetSlots: 8}}, "", time.Now())
	assert.Contains(t, idle.String(), "slots 0/8  mem 0 MiB/48.0 GiB  broker: best-effort")
	assert.NotContains(t, idle.String(), "held")
}

// TestPrintStatusTextFullSnapshot exercises PrintStatusText's populated branches (telemetry
// note, a running-pool with targets and workspaces, the mcp endpoint block) in one render,
// confirming the mcp block coexists with the broker and server rows rather than replacing
// them.
func TestPrintStatusTextFullSnapshot(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "status-*")
	require.NoError(t, err)
	r := daemon.Snapshot{
		Telemetry: daemon.Telemetry{Note: "telemetry is disabled."},
		Cache:     daemon.Cache{Dir: "/cache", SizeMB: 10},
		Pool: &daemon.Output{
			ParentPID: 4242, Capacity: 8, Running: 1,
			RunningTargets: []daemon.RunningTarget{{Args: []string{"run", "build", "web"}, Workspace: "/repo"}},
			Workspaces:     []daemon.Workspace{{Root: "/repo"}},
		},
		MCPEndpoint:  &daemon.MCPEndpoint{Enabled: true, URL: "http://127.0.0.1:7391/mcp", Reachable: true, State: "serving"},
		BrokerPolicy: types.BrokerBestEffort,
		Broker: &daemon.Broker{PID: 48213, Services: []daemon.Service{
			{ID: "service-1", Label: "postgres", State: "running", Dependents: 2},
		}},
		Server: &daemon.Server{PID: 4242},
	}
	PrintStatusText(f, r, StatusView{})
	require.NoError(t, f.Close())
	body, err := os.ReadFile(f.Name())
	require.NoError(t, err)
	out := string(body)
	assert.Contains(t, out, "broker    48213")
	assert.Contains(t, out, "server  4242")
	assert.Contains(t, out, "pool pid 4242", "the pool block still renders")
	assert.Contains(t, out, "workspaces (")
	assert.Contains(t, out, "mcp endpoint", "the mcp block renders alongside the rows")
	assert.Contains(t, out, "serving")
	assert.Contains(t, out, "telemetry is disabled.")
	assert.Contains(t, out, "postgres  running  deps 2")
}

// TestPrintStatusTextNamesWhatIsNotRunning pins the down rows: each process says it is
// not running and what would start it, and the broker row names the policy in force.
func TestPrintStatusTextNamesWhatIsNotRunning(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "status-*")
	require.NoError(t, err)
	PrintStatusText(f, daemon.Snapshot{BrokerPolicy: types.BrokerRequired}, StatusView{})
	require.NoError(t, f.Close())
	body, err := os.ReadFile(f.Name())
	require.NoError(t, err)
	assert.Contains(t, string(body), "broker   -      not running (a run starts one; broker: required)")
	assert.Contains(t, string(body), "server   -      not running")
}

func TestPrintStatusTextDoesNotCallActiveLocalWorkIdle(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "status-*")
	require.NoError(t, err)
	PrintStatusText(f, daemon.Snapshot{Pool: &daemon.Output{
		Capacity: 8, Running: 1,
	}}, StatusView{})
	require.NoError(t, f.Close())
	body, err := os.ReadFile(f.Name())
	require.NoError(t, err)
	assert.Contains(t, string(body), "local work active; detailed target data unavailable")
	assert.NotContains(t, string(body), "nothing running")
}

// answeringServers returns a StatusQuery answering for exactly the addresses in replies,
// in place of live proc servers. An address with no entry fails the way a socket that
// died between discovery and the query does.
func answeringServers(replies map[string]*proc.StatusReply) StatusQuery {
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
		sockA: {ParentPID: 111, Capacity: 8, Running: 3},
		sockB: {ParentPID: 222, Capacity: 4, Running: 4, Server: &daemon.Server{PID: 222}},
	}

	t.Run("one server fills pool and leaves the list empty", func(t *testing.T) {
		snapshot := daemon.Snapshot{}
		ApplyStatusPools(ctx, &snapshot, []string{sockA}, answeringServers(servers))
		require.NotNil(t, snapshot.Pool)
		// Available is 8 capacity less 3 running.
		assert.Equal(t, daemon.Output{ParentPID: 111, Socket: sockA, Capacity: 8, Running: 3, Available: 5}, *snapshot.Pool)
		assert.Empty(t, snapshot.Pools, "a single server is not repeated as a list")
		assert.Empty(t, snapshot.PoolError)
	})

	t.Run("the reply that reports itself as the server fills the server section", func(t *testing.T) {
		snapshot := daemon.Snapshot{}
		ApplyStatusPools(ctx, &snapshot, []string{sockA, sockB}, answeringServers(servers))
		require.NotNil(t, snapshot.Server)
		assert.Equal(t, 222, snapshot.Server.PID)

		alone := daemon.Snapshot{}
		ApplyStatusPools(ctx, &alone, []string{sockA}, answeringServers(servers))
		assert.Nil(t, alone.Server, "a per-process pool is never reported as the server")
	})

	t.Run("two servers report as two entries, never an error", func(t *testing.T) {
		snapshot := daemon.Snapshot{}
		ApplyStatusPools(ctx, &snapshot, []string{sockA, sockB}, answeringServers(servers))
		assert.Empty(t, snapshot.PoolError, "more than one server is reported, not refused")
		require.Len(t, snapshot.Pools, 2)
		assert.Equal(t, []string{sockA, sockB}, []string{snapshot.Pools[0].Socket, snapshot.Pools[1].Socket})
		assert.Equal(t, []int{111, 222}, []int{snapshot.Pools[0].ParentPID, snapshot.Pools[1].ParentPID})
		assert.Equal(t, []int{5, 0}, []int{snapshot.Pools[0].Available, snapshot.Pools[1].Available})
		require.NotNil(t, snapshot.Pool)
		assert.Equal(t, sockA, snapshot.Pool.Socket, "the first server is still THE pool")
	})

	t.Run("a server that died is dropped, the rest still report", func(t *testing.T) {
		snapshot := daemon.Snapshot{}
		ApplyStatusPools(ctx, &snapshot, []string{"unix:///run/magus-gone.sock", sockB}, answeringServers(servers))
		assert.Empty(t, snapshot.PoolError)
		require.NotNil(t, snapshot.Pool)
		assert.Equal(t, sockB, snapshot.Pool.Socket)
		assert.Empty(t, snapshot.Pools, "one survivor is the single-server case")
	})

	t.Run("nothing answered reports why", func(t *testing.T) {
		snapshot := daemon.Snapshot{}
		ApplyStatusPools(ctx, &snapshot, []string{"unix:///run/magus-gone.sock"}, answeringServers(servers))
		assert.Nil(t, snapshot.Pool)
		assert.Contains(t, snapshot.PoolError, "magus-gone.sock")
	})
}

// TestResolveStatusSocketsNarrowing pins that --socket (and the env pin) still select one
// server, and that discovery is only consulted when neither pins one.
func TestResolveStatusSocketsNarrowing(t *testing.T) {
	ctx := context.Background()

	t.Run("--socket narrows to one", func(t *testing.T) {
		t.Setenv("MAGUS_PROC_SOCKET", "unix:///run/magus-env.sock")
		addrs, err := ResolveStatusSockets(ctx, "unix:///run/magus-flag.sock")
		require.NoError(t, err)
		assert.Equal(t, []string{"unix:///run/magus-flag.sock"}, addrs, "the flag wins over the env")
	})

	t.Run("env pins one", func(t *testing.T) {
		t.Setenv("MAGUS_PROC_SOCKET", "unix:///run/magus-env.sock")
		addrs, err := ResolveStatusSockets(ctx, "")
		require.NoError(t, err)
		assert.Equal(t, []string{"unix:///run/magus-env.sock"}, addrs)
	})

	t.Run("unpinned discovers, and says so when there is nothing", func(t *testing.T) {
		t.Setenv("MAGUS_PROC_SOCKET", "")
		t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
		_, err := ResolveStatusSockets(ctx, "")
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
			got := BuildConfigStatus(config.Config{Concurrency: tc.configured})
			// The configured value is kept alongside the effective one.
			assert.Equal(t, daemon.Concurrency{Configured: tc.configured, Effective: tc.want}, got.Concurrency)
		})
	}
}

// TestPrintStatusTextListsEveryProcServer proves the multi-server case renders as a list
// with each server's slots rather than an error demanding --socket.
func TestPrintStatusTextListsEveryProcServer(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "status-*")
	require.NoError(t, err)
	pools := []daemon.Output{
		{ParentPID: 111, Socket: "unix:///run/magus-111.sock", Capacity: 8, Running: 3, Available: 5},
		{ParentPID: 222, Socket: "unix:///run/magus-222.sock", Capacity: 4, Running: 4},
	}
	PrintStatusText(f, daemon.Snapshot{Pool: &pools[0], Pools: pools}, StatusView{})
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
	r := daemon.Snapshot{
		Config: daemon.Config{Concurrency: daemon.Concurrency{Effective: 8}},
		Pool:   &daemon.Output{ParentPID: 4242, Capacity: 8, Running: 2, Available: 6},
	}
	PrintStatusText(f, r, StatusView{})
	require.NoError(t, f.Close())
	body, err := os.ReadFile(f.Name())
	require.NoError(t, err)
	out := string(body)

	assert.Contains(t, out, "available: 6")
	assert.Contains(t, out, "concurrency")
	assert.Contains(t, out, "(from profile)", "an unset configured value says so instead of printing 0")
	assert.Contains(t, out, "effective")
	assert.Contains(t, out, "8")
}

func TestCompactMCPToken(t *testing.T) {
	assert.Empty(t, compactMCPToken(nil))
	assert.Empty(t, compactMCPToken(&daemon.MCPEndpoint{State: "serving"}), "serving is the steady state; omitted from the compact line")
	assert.Equal(t, "mcp unreachable", compactMCPToken(&daemon.MCPEndpoint{State: "unreachable"}))
	assert.Equal(t, "mcp not-ready", compactMCPToken(&daemon.MCPEndpoint{State: "not-ready"}))
}

// statusFixture is one snapshot carrying every optional section, so the renderers below are
// exercised against the same data instead of each against a fixture shaped to suit it.
// now anchors the durations: every StartedAt is an offset from it, so the rendered clock
// is the same on every run.
func statusFixture(now time.Time) daemon.Snapshot {
	return daemon.Snapshot{
		Telemetry: daemon.Telemetry{
			Enabled:     true,
			Endpoint:    "localhost:4317",
			Protocol:    "grpc",
			Insecure:    true,
			ServiceName: "magus",
			SampleRatio: 0.25,
			Note:        "traces are sampled",
		},
		Cache: daemon.Cache{Immutable: true, Dir: "/tmp/magus-cache", SizeMB: 512},
		Build: daemon.Build{SelfUpdate: true},
		Config: daemon.Config{
			Concurrency:   daemon.Concurrency{Configured: 8, Effective: 8},
			DefaultCharms: []string{"rw"},
		},
		Pool: &daemon.Output{
			ParentPID: 4242,
			Version:   "v1.2.3",
			Capacity:  4,
			Running:   2,
			Available: 2,
			Queued:    3,
			RunningTargets: []daemon.RunningTarget{
				{Args: []string{"run", "build", "web"}, Workspace: "/repos/alpha", StartedAt: now.Add(-90 * time.Second), Step: "tsc --noEmit"},
				{Args: []string{"run", "test", "api"}, Workspace: "/repos/beta", StartedAt: now.Add(-3 * time.Second)},
				{Args: []string{"--totally", "--flags"}, Workspace: "/repos/beta"},
			},
			Workspaces: []daemon.Workspace{{Root: "/repos/alpha", LastAccess: now.Add(-time.Minute)}},
		},
		Pools: []daemon.Output{
			{ParentPID: 4242, Capacity: 4, Running: 2, Available: 2, Socket: "unix:///tmp/a.sock"},
			{ParentPID: 5353, Capacity: 2, Running: 0, Available: 2, Socket: "unix:///tmp/b.sock"},
		},
		BrokerPolicy: types.BrokerBestEffort,
		Broker: &daemon.Broker{
			PID: 48213, Protocol: "magus.broker/v1alpha1", Socket: "unix:///run/magus/" + broker.SocketName, StartTime: now.Add(-time.Hour),
			Capacity: types.MachineSnapshot{BudgetSlots: 8, HeldSlots: 2, BudgetMB: 4096, HeldMB: 1024},
			Services: []daemon.Service{
				{ID: "svc-db", Label: "postgres", Command: "docker compose up db", Ports: []string{"5432"}, State: daemon.ServiceRunning, Dependents: 2},
				{ID: "svc-bare"},
			},
			IdleExitSeconds: 600,
		},
		Server: &daemon.Server{
			PID: 4242, Socket: "unix:///run/magus/server.sock", StartTime: now.Add(-2 * time.Hour),
			Listeners: []daemon.Listener{{Kind: daemon.ListenerHTTP, Address: "127.0.0.1:7777"}},
			Watch:     []string{"/repos/alpha"},
		},
		SymbolIndexes: []types.SymbolIndexStatus{
			{Project: types.ProjectRef{Path: "web"}, Language: "typescript", Freshness: types.SymbolIndexFresh},
			{Project: types.ProjectRef{Path: ".", Dir: "/repos/named-root"}, Freshness: types.SymbolIndexNotBuilt},
		},
		Locks: []daemon.Lock{
			{Project: "web", PID: 999, Command: "magus run build", AcquireTime: now.Add(-2 * time.Hour)},
			{Project: "."},
		},
		MCPEndpoint: &daemon.MCPEndpoint{Enabled: true, URL: "http://127.0.0.1:7777/mcp", State: "serving"},
	}
}

// TestPrintStatusTextRendersEverySection covers the full text frame: the tabwriter block,
// the non-grid pool listing, and each optional section's own printer.
func TestPrintStatusTextRendersEverySection(t *testing.T) {
	var buf bytes.Buffer
	PrintStatusText(&buf, statusFixture(time.Now()), StatusView{})
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

	assert.Contains(t, out, "broker    48213  up 1h0m  proto magus.broker/v1alpha1  unix:///run/magus/"+broker.SocketName)
	assert.Contains(t, out, "slots 2/8  mem 1.0 GiB/4.0 GiB  broker: best-effort")
	assert.Contains(t, out, "service   -      postgres  running  deps 2  ports 5432")
	// A service with no label falls back to its id.
	assert.Contains(t, out, "service   -      svc-bare  deps 0")
	assert.Contains(t, out, "idle      -      exits after 10m0s holding nothing")
	assert.Contains(t, out, "server  4242  up 2h0m  unix:///run/magus/server.sock")
	assert.Contains(t, out, "listen  4242  http 127.0.0.1:7777")
	assert.Contains(t, out, "watch   4242  graph+symbols  /repos/alpha")

	assert.Contains(t, out, "pool pid 4242")
	assert.Contains(t, out, "width: 4   running: 2   available: 2   queued: 3")
	assert.Contains(t, out, "/repos/alpha")
	assert.Contains(t, out, "run build web")
	assert.Contains(t, out, "workspaces (1)")

	assert.Contains(t, out, "proc servers (2)")
	assert.Contains(t, out, "unix:///tmp/b.sock")

	assert.Contains(t, out, "mcp endpoint")
	assert.Contains(t, out, "http://127.0.0.1:7777/mcp")

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
	PrintStatusText(&buf, daemon.Snapshot{}, StatusView{})
	out := buf.String()

	assert.Contains(t, out, "broker   -      not running")
	assert.Contains(t, out, "server   -      not running")
	assert.NotContains(t, out, "proc servers")
	assert.NotContains(t, out, "service ")
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
		PrintStatusText(&buf, daemon.Snapshot{Pool: &daemon.Output{Capacity: 4}}, StatusView{})
		assert.Contains(t, buf.String(), "nothing running")
		assert.Contains(t, buf.String(), "pool pid 0")
	})

	t.Run("busy but blind", func(t *testing.T) {
		var buf bytes.Buffer
		PrintStatusText(&buf, daemon.Snapshot{Pool: &daemon.Output{Capacity: 4, Running: 2}}, StatusView{})
		assert.Contains(t, buf.String(), "local work active; detailed target data unavailable")
	})
}

// TestPrintStatusTextVerboseAddsTheBuildBlock pins the -v gate: the build block is
// diagnostic detail, absent from the default frame.
func TestPrintStatusTextVerboseAddsTheBuildBlock(t *testing.T) {
	var quiet bytes.Buffer
	PrintStatusText(&quiet, daemon.Snapshot{}, StatusView{})
	assert.NotContains(t, quiet.String(), "selfupdate")

	var loud bytes.Buffer
	PrintStatusText(&loud, daemon.Snapshot{Build: daemon.Build{SelfUpdate: true}}, StatusView{Verbose: true})
	assert.Contains(t, loud.String(), "selfupdate")
	assert.Contains(t, loud.String(), "engine")
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

func TestGridEnabledNeedsATerminalAndColor(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	assert.True(t, GridEnabled(output.Options{Format: output.Text}, true))
	assert.False(t, GridEnabled(output.Options{Format: output.Text}, false))
	assert.False(t, GridEnabled(output.Options{Format: output.JSON}, true))

	t.Setenv("NO_COLOR", "1")
	assert.False(t, GridEnabled(output.Options{Format: output.Text}, true))
}

func TestTruncateBoundsALabel(t *testing.T) {
	assert.Equal(t, "abc", Truncate("abc", 10))
	assert.LessOrEqual(t, len(Truncate(strings.Repeat("x", 100), 10)), 10)
}

// An unvouched index says why under its row, or the reader meets a verdict with no way to
// clear it.
func TestPrintSymbolIndexStatusPrintsTheDetailOfAnUnvouchedIndex(t *testing.T) {
	var buf bytes.Buffer
	printSymbolIndexStatus(&buf, []types.SymbolIndexStatus{
		{Project: types.ProjectRef{Path: "web"}, Language: "typescript", Freshness: types.SymbolIndexUnvouched,
			Detail: "[MGS3035] typescript:tsc runs in /repos/web but cannot say which build it is"},
		{Project: types.ProjectRef{Path: "api"}, Language: "go", Freshness: types.SymbolIndexFresh},
	})
	out := buf.String()

	assert.Contains(t, out, "unvouched")
	assert.Contains(t, out, "    [MGS3035] typescript:tsc runs in /repos/web but cannot say which build it is\n")
	assert.Equal(t, 1, strings.Count(out, "MGS3035"), "a fresh index prints no detail line")
}

func TestServerVersionSkewPoolParentRemedy(t *testing.T) {
	const version = "current-build"

	pool := &daemon.Output{
		ParentPID: 1314,
		Version:   "stale-build",
		Socket:    "unix:///tmp/magus-1314-deadbeef.sock",
	}
	got := serverVersionSkew(pool, version)
	assert.True(t, strings.HasPrefix(got, "[MGS3048] version skew: "), got)
	assert.Contains(t, got, "pool parent")
	assert.Contains(t, got, "--pools")
	assert.NotContains(t, got, "server start")

	server := &daemon.Output{
		ParentPID: 99,
		Version:   "stale-build",
		Socket:    "unix:///tmp/server.sock",
	}
	got = serverVersionSkew(server, version)
	assert.True(t, strings.HasPrefix(got, "[MGS3048] version skew: "), got)
	assert.Contains(t, got, "the server serving it")
	assert.Contains(t, got, "server start")
	assert.NotContains(t, got, "--pools")
}

func TestPrintEndpointStatus(t *testing.T) {
	render := func(m *daemon.Console) string {
		var b strings.Builder
		printEndpointStatus(&b, "mcp endpoint", m)
		return b.String()
	}

	t.Run("nil-renders-nothing", func(t *testing.T) {
		assert.Empty(t, render(nil))
	})
	t.Run("serving-shows-url-and-state", func(t *testing.T) {
		out := render(&daemon.Console{Enabled: true, URL: "http://127.0.0.1:7391/mcp", Reachable: true, State: "serving"})
		assert.Contains(t, out, "mcp endpoint")
		assert.Contains(t, out, "http://127.0.0.1:7391/mcp")
		assert.Contains(t, out, "serving")
	})
	t.Run("unreachable-shows-note", func(t *testing.T) {
		out := render(&daemon.Console{Enabled: true, URL: "http://127.0.0.1:7391/mcp", State: "unreachable", Note: "start the server: magus server start"})
		assert.Contains(t, out, "unreachable")
		assert.Contains(t, out, "magus server start")
	})
	t.Run("disabled-omits-url", func(t *testing.T) {
		out := render(&daemon.Console{State: "disabled", Note: "MCP is disabled (mcp.enabled=false); no agent tools are served."})
		assert.Contains(t, out, "disabled")
		assert.NotContains(t, out, "url")
	})
}

type CapacityReserveSuite struct{ suite.Suite }

func TestCapacityReserveSuite(t *testing.T) { suite.Run(t, new(CapacityReserveSuite)) }

func (s *CapacityReserveSuite) TestTheMemoryFigureNamesWhatItLeftTheOS() {
	s.Equal("slots 8/10  mem 14.4 GiB/21.0 GiB (24.0 GiB less 3.0 GiB: the aggressive profile keeps 1/8, at least 1 GiB)",
		capacityText(types.MachineSnapshot{
			BudgetSlots: 10, HeldSlots: 8, BudgetMB: 21504, HeldMB: 14746,
			Reserve: types.MemoryReserve{TotalMB: 24576, ReservedMB: 3072, Rule: "the aggressive profile keeps 1/8, at least 1 GiB"},
		}))
}

func (s *CapacityReserveSuite) TestABrokerThatSendsNoReserveShowsTheBudgetAlone() {
	s.Equal("mem 1.0 GiB/4.0 GiB", capacityText(types.MachineSnapshot{BudgetMB: 4096, HeldMB: 1024}))
}

func TestBrokerDownRowNamesThePolicy(t *testing.T) {
	var buf bytes.Buffer
	printBrokerDown(&buf, types.BrokerOff)
	assert.Contains(t, buf.String(), "not running (runs here never ask one; broker: off)")

	buf.Reset()
	printBrokerDown(&buf, "")
	assert.Contains(t, buf.String(), "a run starts one; broker: best-effort", "unset reads as the default it behaves as")
}

func TestServerRowsNameEveryListener(t *testing.T) {
	var buf bytes.Buffer
	printServerRows(&buf, &daemon.Server{
		PID: 47001, Socket: "unix:///run/magus/server.sock", StartTime: time.Now().Add(-time.Hour),
		Listeners: []daemon.Listener{
			{Kind: daemon.ListenerSocket, Address: "unix:///run/magus/server.sock"},
			{Kind: daemon.ListenerHTTP, Address: "127.0.0.1:7391"},
		},
		Watch: []string{"/repo"},
	}, time.Now())
	got := buf.String()
	assert.Contains(t, got, "server  47001")
	assert.Contains(t, got, "listen  47001  http 127.0.0.1:7391")
	assert.Contains(t, got, "watch   47001  graph+symbols  /repo")
}
