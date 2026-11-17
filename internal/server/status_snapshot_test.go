package server

import (
	"bytes"
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/libs/codec"
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
	raw, err := codec.Marshal(report)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, codec.Unmarshal(raw, &got))
	assert.Contains(t, got, "silent_leases")
	assert.NotContains(t, got, "StatusSnapshot", "the snapshot's fields sit at the top level")
	assert.Contains(t, string(raw), "no pool")

	raw, err = codec.Marshal(StatusReport{})
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "silent_leases", "no silent lease and no error adds no key")
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

func TestGridEnabledNeedsATerminalAndColor(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	assert.True(t, GridEnabled(output.Options{Format: output.Text}, true))
	assert.False(t, GridEnabled(output.Options{Format: output.Text}, false))
	assert.False(t, GridEnabled(output.Options{Format: output.JSON}, true))

	t.Setenv("NO_COLOR", "1")
	assert.False(t, GridEnabled(output.Options{Format: output.Text}, true))
}
