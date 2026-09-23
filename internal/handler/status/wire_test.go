package status

import (
	"encoding/base64"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/egladman/magus/internal/rpcerr"
	statusv1 "github.com/egladman/magus/proto/gen/go/magus/status/v1alpha1"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func toProto(t *testing.T, r types.StatusSnapshot, build types.BuildInfo) *statusv1.Status {
	t.Helper()
	s, err := statusSnapshotToProto(r, build)
	require.NoError(t, err)
	return s
}

// TestStatusProtoMapsPool maps a running-pool status report onto the wire message:
// health, slots, and the in-flight calls a dashboard shows.
func TestStatusProtoMapsPool(t *testing.T) {
	started := time.UnixMilli(1700)
	r := types.StatusSnapshot{
		Pool: &types.StatusOutput{
			ParentPID: 42, Mode: "daemon", Capacity: 8, Running: 3, Queued: 1,
			RunningTargets: []types.StatusRunningTarget{{Args: []string{"run", "build", "api"}, Workspace: "/ws", StartedAt: started, Step: "go-build"}},
		},
	}
	s := toProto(t, r, types.BuildInfo{Version: "v1.2.3"})

	assert.Equal(t, statusv1.Health_HEALTH_HEALTHY, s.GetHealth())
	assert.Equal(t, "v1.2.3", s.GetBuild().GetVersion())

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
// configured cap) onto Pool.cache, the data the dashboard's cache tiles and per-target
// live-log deep-links read.
func TestStatusProtoMapsCacheAndInv(t *testing.T) {
	r := types.StatusSnapshot{
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
	p := toProto(t, r, types.BuildInfo{Version: "v1"}).GetPool()
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
	assert.Equal(t, statusv1.Health_HEALTH_DOWN, toProto(t, types.StatusSnapshot{}, types.BuildInfo{Version: "v1"}).GetHealth())
	assert.Equal(t, statusv1.Health_HEALTH_DEGRADED,
		toProto(t, types.StatusSnapshot{Pool: &types.StatusOutput{}, PoolError: "boom"}, types.BuildInfo{Version: "v1"}).GetHealth())
}

// A failed workspace is a resource in STATE_FAILED carrying the same google.rpc.Status a
// call against it returns, and it rolls the dashboard's health up to degraded.
func TestStatusProtoCarriesAFailedWorkspace(t *testing.T) {
	failure := &types.WorkspaceFailure{Message: "boom", Diagnostics: []types.SourceDiagnostic{{
		Code: "BZZ1005", File: "magusfile.buzz", Line: 3, Column: 3, Message: "cannot assign",
	}}}
	s := toProto(t, types.StatusSnapshot{Pool: &types.StatusOutput{Workspaces: []types.StatusWorkspace{
		{Root: "/ok"},
		{Root: "/loading", State: types.WorkspaceLoading},
		{Root: "/repo", State: types.WorkspaceFailed, Error: failure},
	}}}, types.BuildInfo{Version: "v1"})

	assert.Equal(t, statusv1.Health_HEALTH_DEGRADED, s.GetHealth())
	ws := s.GetPool().GetWorkspaces()
	require.Len(t, ws, 3)
	assert.Equal(t, statusv1.Workspace_STATE_ACTIVE, ws[0].GetState(), "an older daemon's workspace is loaded")
	assert.Nil(t, ws[0].GetError())
	assert.Equal(t, statusv1.Workspace_STATE_LOADING, ws[1].GetState())
	assert.Nil(t, ws[1].GetError())
	assert.Equal(t, statusv1.Workspace_STATE_FAILED, ws[2].GetState())
	want, err := rpcerr.WorkspaceFailed("/repo", failure).Status()
	require.NoError(t, err)
	assert.True(t, proto.Equal(want, ws[2].GetError()))
}

// Health agrees with readiness: every workspace that tried to load failing is down, and a
// loaded one beside a failed one is degraded.
func TestStatusProtoHealthMatchesReadiness(t *testing.T) {
	failed := types.StatusWorkspace{Root: "/bad", State: types.WorkspaceFailed}
	cases := []struct {
		name string
		ws   []types.StatusWorkspace
		want statusv1.Health
	}{
		{"every workspace failed", []types.StatusWorkspace{failed}, statusv1.Health_HEALTH_DOWN},
		{"one failed, one still loading", []types.StatusWorkspace{failed, {Root: "/l", State: types.WorkspaceLoading}}, statusv1.Health_HEALTH_DOWN},
		{"one failed, one loaded", []types.StatusWorkspace{failed, {Root: "/ok", State: types.WorkspaceActive}}, statusv1.Health_HEALTH_DEGRADED},
		{"one loaded", []types.StatusWorkspace{{Root: "/ok", State: types.WorkspaceActive}}, statusv1.Health_HEALTH_HEALTHY},
		{"none yet", nil, statusv1.Health_HEALTH_HEALTHY},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := toProto(t, types.StatusSnapshot{Pool: &types.StatusOutput{Workspaces: tc.ws}}, types.BuildInfo{Version: "v1"})
			assert.Equal(t, tc.want, s.GetHealth())
		})
	}
}

// TestEncodeStatusEventRoundTrip confirms a status snapshot decodes back: base64 -> proto.
func TestEncodeStatusEventRoundTrip(t *testing.T) {
	ev, err := EncodeStatusEvent(toProto(t, types.StatusSnapshot{Pool: &types.StatusOutput{Capacity: 4}}, types.BuildInfo{Version: "v1"}))
	require.NoError(t, err)
	raw, err := base64.StdEncoding.DecodeString(ev)
	require.NoError(t, err)
	var got statusv1.Status
	require.NoError(t, proto.Unmarshal(raw, &got))
	assert.Equal(t, int32(4), got.GetPool().GetCapacity())
	assert.Equal(t, statusv1.Health_HEALTH_HEALTHY, got.GetHealth())
}

// TestStatusProtoMapsRuns maps the daemon's live runs and their per-target execution
// state onto the wire message's runs, the same status frame that carries the pool.
func TestStatusProtoMapsRuns(t *testing.T) {
	started := time.UnixMilli(1_000)
	execAt := time.UnixMilli(2_000)
	doneAt := time.UnixMilli(5_000)
	r := types.StatusSnapshot{
		Runs: []types.StatusRun{{
			Inv:       "inv1a2b3c",
			Trigger:   "run",
			StartedAt: started,
			Targets: []types.StatusTargetRun{
				{Project: "svc/api", Target: "build", State: types.TargetRunPassed, StartedAt: execAt, EndedAt: doneAt, OutputRef: "outcafef00d", DurationMs: 3_000},
				{Project: "svc/api", Target: "test", State: types.TargetRunRunning, StartedAt: execAt},
				{Project: "svc/web", Target: "lint", State: types.TargetRunCached, StartedAt: doneAt, EndedAt: doneAt, OutputRef: "outbeef"},
			},
		}},
	}
	s := toProto(t, r, types.BuildInfo{Version: "v1"})

	require.Len(t, s.GetRuns(), 1)
	run := s.GetRuns()[0]
	assert.Equal(t, "inv1a2b3c", run.GetInv())
	assert.Equal(t, "run", run.GetTrigger())
	assert.Equal(t, int64(1_000), run.GetStartTime().AsTime().UnixMilli())

	require.Len(t, run.GetTargets(), 3)
	assert.Equal(t, statusv1.TargetRun_STATE_PASSED, run.GetTargets()[0].GetState())
	assert.Equal(t, "outcafef00d", run.GetTargets()[0].GetOutputRef())
	assert.Equal(t, int64(3_000), run.GetTargets()[0].GetDurationMs())
	assert.Equal(t, int64(5_000), run.GetTargets()[0].GetEndTime().AsTime().UnixMilli())
	assert.Equal(t, statusv1.TargetRun_STATE_RUNNING, run.GetTargets()[1].GetState())
	assert.Nil(t, run.GetTargets()[1].GetEndTime()) // still running: no end
	assert.Equal(t, statusv1.TargetRun_STATE_CACHED, run.GetTargets()[2].GetState())
}

// TestStatusProtoCarriesSecretProviderName pins that the selected provider's NAME reaches
// the dashboard, and that a workspace which declared none reports empty rather than
// inventing the built-in one's name. The console keys "is a provider declared" off exactly
// that emptiness, so a default filled in here would put a badge on every row.
//
// The name and nothing else: there is no reference list and no value on this wire, and
// there must not be. magus does not store secrets (it reads them through a provider), so
// publishing what a build CAN reach would be a map of what to go after.
func TestStatusProtoCarriesSecretProviderName(t *testing.T) {
	r := types.StatusSnapshot{
		Pool: &types.StatusOutput{
			Mode: "daemon",
			Workspaces: []types.StatusWorkspace{
				{Root: "/repo", SecretProvider: "onepassword"},
				{Root: "/svc"},
			},
		},
	}
	p := toProto(t, r, types.BuildInfo{Version: "v1"}).GetPool()
	require.NotNil(t, p)
	require.Len(t, p.GetWorkspaces(), 2)

	assert.Equal(t, "onepassword", p.GetWorkspaces()[0].GetSecretProvider())
	assert.Empty(t, p.GetWorkspaces()[1].GetSecretProvider(),
		"a workspace with no declared provider reports empty, not the built-in's name")

	// Nothing resembling a credential or a reference list rides along.
	assert.NotContains(t, p.String(), "secret_ref")
}

// TestStatusProtoCarriesTheStaleLockThreshold pins the half of a lock row that decides how it
// is READ. The holder's age is meaningless on its own; the threshold is what separates a peer
// mid-run from a process nobody remembers starting, and it is on the wire so every renderer
// shares the daemon's judgment instead of picking its own constant.
func TestStatusProtoCarriesTheStaleLockThreshold(t *testing.T) {
	held := time.UnixMilli(1700)
	r := types.StatusSnapshot{Locks: []types.StatusLock{{
		Project: ".", PID: 4242, Command: "magus run build .", Dir: "/ws",
		AcquireTime: held, StaleAfterSeconds: 600,
	}}}

	locks := toProto(t, r, types.BuildInfo{Version: "v1"}).GetLocks()

	require.Len(t, locks, 1)
	assert.Equal(t, int32(600), locks[0].GetStaleAfterSeconds())
	assert.Equal(t, int64(1700), locks[0].GetAcquireTime().AsTime().UnixMilli())
	assert.Equal(t, int32(4242), locks[0].GetPid())
}
