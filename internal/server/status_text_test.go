package server

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/broker"
	"github.com/egladman/magus/libs/safetyscissors/budget"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/daemon"
)

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

// TestBrokerRowsNameEveryClaim covers the question the rows exist to answer: WHERE the
// host's capacity went. One broker serves every worktree, so a pid alone does not say
// which tree to go and look at; and each row leads with its record type and pid, so awk
// works on it.
func TestBrokerRowsNameEveryClaim(t *testing.T) {
	var buf bytes.Buffer
	PrintBrokerRows(&buf, daemon.Broker{
		PID: 48213,
		Capacity: budget.Snapshot{
			BudgetMB: 48 << 10, HeldMB: 10 << 10, BudgetSlots: 8, HeldSlots: 6,
			Holders: []budget.Claimant{
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
		Capacity: budget.Snapshot{
			BudgetSlots: 8, HeldSlots: 8,
			Holders: []budget.Claimant{{Project: ".", Target: "test", PID: 41221, Slots: 8, Dir: "/tree/polish", Owner: "fix-cache"}},
			Waiting: []budget.Waiter{
				{PID: 41300, Project: ".", Target: "lint", Slots: 2, Dir: "/tree/a", Owner: "unleased", Since: now.Add(-time.Minute)},
				{PID: 41301, Project: "docs", Target: "ci", Owner: "job-b", Tree: "root-job", Since: now},
			},
			Refused: []budget.Admission{{
				At: now.Add(-2 * time.Minute), PID: 41400, Project: ".", Target: "ci", Slots: 4, Dir: "/tree/b",
				Owner: "spawned by root", Fits: true,
				Holders: []budget.Claimant{{PID: 41221, Owner: "fix-cache"}, {PID: 41222}},
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
		Capacity: budget.Snapshot{BudgetSlots: 8, HeldSlots: 8, Waiting: []budget.Waiter{
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
		holderMemText(budget.Claimant{MemoryMB: 5879, Sizing: budget.Sizing{Samples: 54}}))
	assert.Equal(t, "mem 10.0 GiB", holderMemText(budget.Claimant{MemoryMB: 10 << 10}), "a declaration reads bare")
	assert.Empty(t, holderMemText(budget.Claimant{Slots: 2}), "a slots-only claim has no memory to explain")
}

// TestStatusBreaksDownOnlyHoldersOverAQuarterOfTheBudget: a big claim says
// where its figure came from, and a small one adds no row.
func TestStatusBreaksDownOnlyHoldersOverAQuarterOfTheBudget(t *testing.T) {
	var buf bytes.Buffer
	PrintBrokerRows(&buf, daemon.Broker{
		PID: 1,
		Capacity: budget.Snapshot{BudgetMB: 16 << 10, BudgetSlots: 8, Holders: []budget.Claimant{
			{Project: ".", Target: "test", PID: 10, MemoryMB: 5 << 10, Sizing: budget.Sizing{Samples: 54}},
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
	PrintBrokerRows(&idle, daemon.Broker{PID: 7, Capacity: budget.Snapshot{BudgetMB: 48 << 10, BudgetSlots: 8}}, "", time.Now())
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
			Capacity: budget.Snapshot{BudgetSlots: 8, HeldSlots: 2, BudgetMB: 4096, HeldMB: 1024},
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
		capacityText(budget.Snapshot{
			BudgetSlots: 10, HeldSlots: 8, BudgetMB: 21504, HeldMB: 14746,
			Reserve: budget.Reserve{TotalMB: 24576, ReservedMB: 3072, Rule: "the aggressive profile keeps 1/8, at least 1 GiB"},
		}))
}

func (s *CapacityReserveSuite) TestABrokerThatSendsNoReserveShowsTheBudgetAlone() {
	s.Equal("mem 1.0 GiB/4.0 GiB", capacityText(budget.Snapshot{BudgetMB: 4096, HeldMB: 1024}))
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
