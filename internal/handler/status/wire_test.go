package status

import (
	"encoding/base64"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	statusv1 "github.com/egladman/magus/proto/gen/go/magus/status/v1"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStatusProtoMapsPool maps a running-pool status report onto the wire message:
// health, slots, and the in-flight calls a dashboard shows.
func TestStatusProtoMapsPool(t *testing.T) {
	started := time.UnixMilli(1700)
	r := types.StatusReport{
		Pool: &types.StatusOutput{
			ParentPID: 42, Mode: "daemon", Capacity: 8, Running: 3, Queued: 1,
			RunningTargets: []types.StatusRunningTarget{{Args: []string{"run", "build", "api"}, Workspace: "/ws", StartedAt: started, Step: "go-build"}},
		},
	}
	s := statusReportToProto(r, "v1.2.3")

	assert.Equal(t, statusv1.Health_HEALTH_HEALTHY, s.GetHealth())
	assert.Equal(t, "v1.2.3", s.GetMagusVersion())

	p := s.GetPool()
	require.NotNil(t, p)
	assert.Equal(t, int32(8), p.GetCapacity())
	assert.Equal(t, int32(3), p.GetRunning())
	assert.Equal(t, int32(1), p.GetQueued())
	require.Len(t, p.GetRunningTargets(), 1)
	assert.Equal(t, []string{"run", "build", "api"}, p.GetRunningTargets()[0].GetArgs())
	assert.Equal(t, "go-build", p.GetRunningTargets()[0].GetStep())
	assert.Equal(t, int64(1700), p.GetRunningTargets()[0].GetStartTime().AsTime().UnixMilli())
}

// TestStatusProtoMapsCacheAndInv maps per-workspace cache activity onto each Workspace,
// the invocation id onto each RunningTarget, and the pool-wide aggregate (summed counters + the
// configured cap) onto Pool.cache - the data the dashboard's cache tiles and per-target
// live-log deep-links read.
func TestStatusProtoMapsCacheAndInv(t *testing.T) {
	r := types.StatusReport{
		Cache: types.CacheStatus{SizeMB: 2048},
		Pool: &types.StatusOutput{
			Mode: "daemon", Capacity: 4, Running: 2,
			RunningTargets: []types.StatusRunningTarget{{Args: []string{"run", "build"}, Inv: "inv7c3a9f2"}},
			Workspaces: []types.StatusWorkspace{
				{Root: "/repo", CacheHit: 1284, CacheMiss: 217, CacheError: 3, CacheBytes: 734003200},
				{Root: "/svc", CacheHit: 512, CacheMiss: 98, CacheBytes: 120586240},
			},
		},
	}
	p := statusReportToProto(r, "v1").GetPool()
	require.NotNil(t, p)

	// Per-running-target invocation id.
	require.Len(t, p.GetRunningTargets(), 1)
	assert.Equal(t, "inv7c3a9f2", p.GetRunningTargets()[0].GetInvocation())

	// Per-workspace cache.
	require.Len(t, p.GetWorkspaces(), 2)
	require.NotNil(t, p.GetWorkspaces()[0].GetCache())
	assert.Equal(t, int64(1284), p.GetWorkspaces()[0].GetCache().GetHits())
	assert.Equal(t, int64(734003200), p.GetWorkspaces()[0].GetCache().GetSizeBytes())

	// Pool-wide aggregate: summed counters + the configured cap.
	agg := p.GetCache()
	require.NotNil(t, agg)
	assert.Equal(t, int64(1796), agg.GetHits())
	assert.Equal(t, int64(315), agg.GetMisses())
	assert.Equal(t, int64(3), agg.GetErrors())
	assert.Equal(t, int64(854589440), agg.GetSizeBytes())
	assert.Equal(t, int32(2048), agg.GetSizeCapMb())
}

// TestStatusProtoHealth derives DOWN when no pool is present and DEGRADED on a pool error.
func TestStatusProtoHealth(t *testing.T) {
	assert.Equal(t, statusv1.Health_HEALTH_DOWN, statusReportToProto(types.StatusReport{}, "v1").GetHealth())
	assert.Equal(t, statusv1.Health_HEALTH_DEGRADED,
		statusReportToProto(types.StatusReport{Pool: &types.StatusOutput{}, PoolError: "boom"}, "v1").GetHealth())
}

// TestEncodeStatusEventRoundTrip confirms a status snapshot decodes back: base64 -> proto.
func TestEncodeStatusEventRoundTrip(t *testing.T) {
	ev, err := EncodeStatusEvent(types.StatusReport{Pool: &types.StatusOutput{Capacity: 4}}, "v1")
	require.NoError(t, err)
	raw, err := base64.StdEncoding.DecodeString(ev)
	require.NoError(t, err)
	var got statusv1.Status
	require.NoError(t, proto.Unmarshal(raw, &got))
	assert.Equal(t, int32(4), got.GetPool().GetCapacity())
	assert.Equal(t, statusv1.Health_HEALTH_HEALTHY, got.GetHealth())
}

// TestStatusProtoMapsRuns maps the daemon's live runs and their per-target execution
// state onto the wire message's runs - the same status frame that carries the pool.
func TestStatusProtoMapsRuns(t *testing.T) {
	started := time.UnixMilli(1_000)
	execAt := time.UnixMilli(2_000)
	doneAt := time.UnixMilli(5_000)
	r := types.StatusReport{
		Runs: []types.StatusRun{{
			Inv:       "inv1a2b3c",
			Trigger:   "run",
			StartedAt: started,
			Targets: []types.StatusTargetRun{
				{Project: "svc/api", Target: "build", State: types.TargetRunPassed, StartedAt: execAt, EndedAt: doneAt, OutputRef: "refcafef00d", DurationMs: 3_000},
				{Project: "svc/api", Target: "test", State: types.TargetRunRunning, StartedAt: execAt},
				{Project: "svc/web", Target: "lint", State: types.TargetRunCached, StartedAt: doneAt, EndedAt: doneAt, OutputRef: "refbeef"},
			},
		}},
	}
	s := statusReportToProto(r, "v1")

	require.Len(t, s.GetRuns(), 1)
	run := s.GetRuns()[0]
	assert.Equal(t, "inv1a2b3c", run.GetInv())
	assert.Equal(t, "run", run.GetTrigger())
	assert.Equal(t, int64(1_000), run.GetStartedAt().AsTime().UnixMilli())

	require.Len(t, run.GetTargets(), 3)
	assert.Equal(t, statusv1.TargetRun_PASSED, run.GetTargets()[0].GetState())
	assert.Equal(t, "refcafef00d", run.GetTargets()[0].GetOutputRef())
	assert.Equal(t, int64(3_000), run.GetTargets()[0].GetDurationMs())
	assert.Equal(t, int64(5_000), run.GetTargets()[0].GetEndedAt().AsTime().UnixMilli())
	assert.Equal(t, statusv1.TargetRun_RUNNING, run.GetTargets()[1].GetState())
	assert.Nil(t, run.GetTargets()[1].GetEndedAt()) // still running: no end
	assert.Equal(t, statusv1.TargetRun_CACHED, run.GetTargets()[2].GetState())
}
