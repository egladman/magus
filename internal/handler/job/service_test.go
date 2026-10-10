package job

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	jobstore "github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/testkit"
	activityv1 "github.com/egladman/magus/proto/gen/go/magus/activity/v1alpha1"
	jobv1 "github.com/egladman/magus/proto/gen/go/magus/job/v1alpha1"
	"github.com/egladman/magus/types"
)

func TestMain(m *testing.M) { testkit.Main(m) }

// fakeWS is a workspace whose trail lives at dir and whose cache reports a fixed size.
type fakeWS struct {
	dir        string
	cacheBytes int64
}

func (f fakeWS) CacheDir() string      { return f.dir }
func (f fakeWS) CacheDiskBytes() int64 { return f.cacheBytes }

// newTestService builds a Service with injected proc seams so no live server is needed.
func newTestService(ws workspace, submit func(context.Context, string, []string, string) (string, error), status func(context.Context, string) (*proc.StatusReply, error)) *Service {
	return &Service{
		ws:       ws,
		version:  "test",
		socket:   func() string { return "unix:///test.sock" },
		submitFn: submit,
		statusFn: status,
	}
}

func TestSubmit_NewJobIsSubmitted(t *testing.T) {
	ws := fakeWS{dir: t.TempDir(), cacheBytes: 4096}
	submit := func(context.Context, string, []string, string) (string, error) { return "inv-new", nil }
	status := func(context.Context, string) (*proc.StatusReply, error) { return &proc.StatusReply{}, nil }
	s := newTestService(ws, submit, status)

	resp, err := s.RunJob(context.Background(), connect.NewRequest(&jobv1.RunJobRequest{Name: "jobs/clear-cache"}))
	require.NoError(t, err)
	require.Equal(t, jobv1.SubmitState_SUBMIT_STATE_SUBMITTED, resp.Msg.State)
	require.Equal(t, "inv-new", resp.Msg.InvocationId)
	require.Equal(t, "jobs/clear-cache", resp.Msg.Job.Name)
	require.False(t, resp.Msg.Job.Running)
	require.Equal(t, int64(4096), resp.Msg.Job.Target.SizeBytes) // clear-cache target is the cache size
}

func TestSubmit_CoalescedReportsRunningInvocation(t *testing.T) {
	ws := fakeWS{dir: t.TempDir()}
	submit := func(context.Context, string, []string, string) (string, error) { return "", nil } // coalesced
	// The server reports an identical rotate-activities job already in flight.
	status := func(context.Context, string) (*proc.StatusReply, error) {
		return &proc.StatusReply{Calls: []proc.Call{{Args: []string{"server", "rotate-activities"}, Inv: "inv-running"}}}, nil
	}
	s := newTestService(ws, submit, status)

	resp, err := s.RunJob(context.Background(), connect.NewRequest(&jobv1.RunJobRequest{Name: "jobs/rotate-activities"}))
	require.NoError(t, err)
	require.Equal(t, jobv1.SubmitState_SUBMIT_STATE_ALREADY_RUNNING, resp.Msg.State)
	require.Equal(t, "inv-running", resp.Msg.InvocationId)
	require.True(t, resp.Msg.Job.Running)
}

func TestSubmit_ProcErrorIsInternal(t *testing.T) {
	submit := func(context.Context, string, []string, string) (string, error) { return "", errors.New("boom") }
	status := func(context.Context, string) (*proc.StatusReply, error) { return &proc.StatusReply{}, nil }
	s := newTestService(fakeWS{dir: t.TempDir()}, submit, status)

	_, err := s.RunJob(context.Background(), connect.NewRequest(&jobv1.RunJobRequest{Name: "jobs/sync-graph"}))
	require.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

// A name nobody registered is NotFound, not a SubmitState: the enum reports how a valid
// submission resolved, and this never became one. Reachable only now that the RPC takes a name.
func TestRunJob_UnknownNameIsNotFound(t *testing.T) {
	s := newTestService(fakeWS{dir: t.TempDir()},
		func(context.Context, string, []string, string) (string, error) { return "x", nil },
		func(context.Context, string) (*proc.StatusReply, error) { return &proc.StatusReply{}, nil })

	_, err := s.RunJob(context.Background(), connect.NewRequest(&jobv1.RunJobRequest{Name: "jobs/not-a-job"}))
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestSubmit_NoSocketIsUnavailable(t *testing.T) {
	s := newTestService(fakeWS{dir: t.TempDir()},
		func(context.Context, string, []string, string) (string, error) { return "x", nil },
		func(context.Context, string) (*proc.StatusReply, error) { return &proc.StatusReply{}, nil })
	s.socket = func() string { return "" } // no server socket

	_, err := s.RunJob(context.Background(), connect.NewRequest(&jobv1.RunJobRequest{Name: "jobs/sync-graph"}))
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
}

func TestJobInfo_LastRunFromTrailAndTargetSize(t *testing.T) {
	dir := t.TempDir()
	// A completed rotate-activities job in the trail, plus two more events so Stat has a count of 3.
	start := time.UnixMilli(1_000_000).UnixMilli()
	trail.Append(t.Context(), dir, trail.Event{Ts: start, Kind: trail.KindJob, Action: "server rotate-activities", Outcome: trail.OutcomeOK, DurationMs: 250})
	trail.Append(t.Context(), dir, trail.Event{Ts: start + 1, Kind: trail.KindMCPToolCall, Action: "query", Outcome: trail.OutcomeOK})
	trail.Append(t.Context(), dir, trail.Event{Ts: start + 2, Kind: trail.KindMCPToolCall, Action: "explain", Outcome: trail.OutcomeOK})

	s := newTestService(fakeWS{dir: dir}, nil, nil)
	running := map[string]string{argvKey([]string{"server", "rotate-activities"}): "inv-live"}

	j, ok := jobstore.Lookup("rotate-activities")
	require.True(t, ok)
	got := s.job(j, running, types.Job{})

	require.Equal(t, "jobs/rotate-activities", got.Name)
	require.True(t, got.Running)
	require.Equal(t, int64(3), got.Target.ItemCount) // three trail events on disk
	require.Positive(t, got.Target.SizeBytes)

	wantRun := &jobv1.JobRun{
		EndTime:  timestamppb.New(time.UnixMilli(start + 250)),
		Duration: durationpb.New(250 * time.Millisecond),
		Ok:       true,
	}
	require.True(t, proto.Equal(wantRun, got.LastRun), "last_run = %v, want %v", got.LastRun, wantRun)
}

func TestListJobs_ReturnsEveryRegisteredJob(t *testing.T) {
	s := newTestService(fakeWS{dir: t.TempDir()}, nil,
		func(context.Context, string) (*proc.StatusReply, error) { return &proc.StatusReply{}, nil })

	resp, err := s.ListJobs(context.Background(), connect.NewRequest(&jobv1.ListJobsRequest{}))
	require.NoError(t, err)
	names := make([]string, 0, len(resp.Msg.Jobs))
	for _, j := range resp.Msg.Jobs {
		names = append(names, j.Name)
	}
	require.Equal(t, []string{
		"jobs/sync-graph", "jobs/rotate-activities", "jobs/rotate-logs", "jobs/prune-preserved",
		"jobs/clear-cache", "jobs/check-review", "jobs/check-drift",
	}, names)
}

// TestListJobs_ReturnsCatalogAndDelegatedJobs is the listing the merge exists for: the
// server's own catalog beside the jobs a session holds, in one response with one state
// vocabulary, so a reader never asks which door to knock on for which kind.
func TestListJobs_ReturnsCatalogAndDelegatedJobs(t *testing.T) {
	dir := t.TempDir()
	store := jobstore.NewStore(jobstore.Location{CacheDir: dir, Root: dir})
	_, err := store.Update(t.Context(), "wave3/merge", func(row *types.Job) {
		row.State = types.StateRunning
		row.Model = "opus"
		row.WritePaths = []string{"internal/handler"}
	})
	require.NoError(t, err)

	s := newTestService(fakeWS{dir: dir}, nil,
		func(context.Context, string) (*proc.StatusReply, error) { return &proc.StatusReply{}, nil })
	s.store = store

	resp, err := s.ListJobs(t.Context(), connect.NewRequest(&jobv1.ListJobsRequest{}))
	require.NoError(t, err)
	byID := make(map[string]*jobv1.Job, len(resp.Msg.Jobs))
	for _, j := range resp.Msg.Jobs {
		byID[j.Id] = j
	}

	catalog := byID["sync-graph"]
	require.NotNil(t, catalog, "the server's own job is missing from the listing")
	require.Equal(t, jobv1.JobHolder_JOB_HOLDER_SERVER, catalog.Holder)
	require.Equal(t, string(types.StateDeclared), catalog.State, "a catalog job nobody has run yet is declared")

	delegated := byID["wave3/merge"]
	require.NotNil(t, delegated, "the delegated job is missing from the listing")
	require.Equal(t, jobv1.JobHolder_JOB_HOLDER_SESSION, delegated.Holder)
	require.Equal(t, string(types.StateRunning), delegated.State)
	require.Equal(t, "opus", delegated.Model)
	require.Equal(t, []string{"internal/handler"}, delegated.WritePaths)
}

// TestListJobs_ServesTheStoredRowVerbatim pins what a reader of a delegated row cannot work
// without: the join key, the state it reached, the tree it was handed, its own heartbeat,
// and what it released for whoever comes next.
func TestListJobs_ServesTheStoredRowVerbatim(t *testing.T) {
	dir := t.TempDir()
	store := jobstore.NewStore(jobstore.Location{StateBase: t.TempDir(), CacheDir: dir, Root: dir})
	_, err := store.Update(t.Context(), "job-a", func(row *types.Job) {
		row.Criteria = "ship the store"
		row.Checkpoint = "60dc9151"
		row.WritePaths = []string{"internal/job", "types/job.go"}
		row.State = types.StateRunning
		row.Goals = []types.Goal{
			{
				ID:     "lint",
				Kind:   types.GoalKindCheck,
				Expect: types.ExpectPassed,
				Check:  types.LeaseCheck{Target: "lint", Project: "."},
			},
			{
				ID:     "store-touched",
				Kind:   types.GoalKindPaths,
				Expect: types.ExpectChanged,
				Paths:  []string{"internal/job/store.go"},
			},
		}
		row.Result = &types.JobResult{
			ChangedPaths:    []string{"internal/job/store.go"},
			UnresolvedRisks: []string{"schema churn"},
			Descendants:     []string{"job-b"},
		}
	})
	require.NoError(t, err)
	// The store derives a release from a claim that shrinks, so giving up types/job.go is
	// the only way to get one onto the wire.
	stored, err := store.Update(t.Context(), "job-a", func(row *types.Job) {
		row.WritePaths = []string{"internal/job"}
	})
	require.NoError(t, err)
	_, err = store.Update(t.Context(), "scout", func(row *types.Job) {
		row.ReadOnly = true
		row.State = types.StateNoReturn
	})
	require.NoError(t, err)

	s := newTestService(fakeWS{dir: dir}, nil,
		func(context.Context, string) (*proc.StatusReply, error) { return &proc.StatusReply{}, nil })
	s.store = store

	resp, err := s.ListJobs(t.Context(), connect.NewRequest(&jobv1.ListJobsRequest{}))
	require.NoError(t, err)
	byID := make(map[string]*jobv1.Job, len(resp.Msg.Jobs))
	for _, j := range resp.Msg.Jobs {
		byID[j.Id] = j
	}

	got := byID["job-a"]
	require.NotNil(t, got, "the delegated row is missing from the listing")
	require.Equal(t, "ship the store", got.Criteria)
	require.Equal(t, "60dc9151", got.Checkpoint)
	require.Equal(t, []string{"internal/job"}, got.WritePaths)
	require.Equal(t, stored.Updated, got.Updated, "the row's own stamp, not the moment it was read")
	require.Len(t, got.Releases, 1)
	require.Equal(t, "types/job.go", got.Releases[0].Path)
	require.NotEmpty(t, got.Releases[0].Digest, "a release says which version of the path the next worker inherits")

	require.Len(t, got.Goals, 2)
	require.Equal(t, "lint", got.Goals[0].Id)
	require.Equal(t, "check", got.Goals[0].Kind)
	require.Equal(t, "passed", got.Goals[0].Expect)
	require.Equal(t, "magus run lint .", got.Goals[0].Check, "check is rendered as the command that runs it")
	require.Equal(t, "store-touched", got.Goals[1].Id)
	require.Equal(t, []string{"internal/job/store.go"}, got.Goals[1].Paths)
	require.Empty(t, got.Goals[1].Check, "a paths gate carries no check to render")

	require.NotNil(t, got.Result)
	require.Equal(t, []string{"internal/job/store.go"}, got.Result.ChangedPaths)
	require.Equal(t, []string{"schema churn"}, got.Result.UnresolvedRisks)
	require.Equal(t, []string{"job-b"}, got.Result.Descendants)

	scout := byID["scout"]
	require.NotNil(t, scout, "the abbreviated row is missing from the listing")
	require.True(t, scout.ReadOnly)
	require.Equal(t, string(types.StateNoReturn), scout.State)
}

// TestListJobs_ReportsOverlappingWritePaths pins that the pairs are derived on the read and
// stored nowhere, so the listing reports one without either row saying anything about the
// other. A fact for the reader, not a verdict: nothing is blocked or reordered on account of it.
func TestListJobs_ReportsOverlappingWritePaths(t *testing.T) {
	dir := t.TempDir()
	store := jobstore.NewStore(jobstore.Location{StateBase: t.TempDir(), CacheDir: dir, Root: dir})
	for _, row := range []types.Job{
		{ID: "job-a", WritePaths: []string{"internal/job"}, State: types.StateRunning},
		{ID: "job-b", WritePaths: []string{"internal/job/store.go"}, State: types.StateDeclared},
		{ID: "job-done", WritePaths: []string{"internal/job"}, State: types.StatePass},
	} {
		_, err := store.Update(t.Context(), row.ID, func(cur *types.Job) {
			cur.WritePaths, cur.State = row.WritePaths, row.State
		})
		require.NoError(t, err)
	}

	s := newTestService(fakeWS{dir: dir}, nil,
		func(context.Context, string) (*proc.StatusReply, error) { return &proc.StatusReply{}, nil })
	s.store = store

	resp, err := s.ListJobs(t.Context(), connect.NewRequest(&jobv1.ListJobsRequest{}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.Overlaps, 1, "the finished job is not competing for anything")
	require.Equal(t, "job-a", resp.Msg.Overlaps[0].JobA)
	require.Equal(t, "job-b", resp.Msg.Overlaps[0].JobB)
	// Each side's own declaration, kept apart: a reader who cannot tell which job claimed
	// which has nothing to act on.
	require.Equal(t, []string{"internal/job"}, resp.Msg.Overlaps[0].PathsA)
	require.Equal(t, []string{"internal/job/store.go"}, resp.Msg.Overlaps[0].PathsB)
}

// TestListJobs_EmptyStoreServesEmptyList pins the shape both read doors promise: a workspace
// where nobody has handed out a job yet lists the catalog and nothing else, never null.
func TestListJobs_EmptyStoreServesEmptyList(t *testing.T) {
	dir := t.TempDir()
	s := newTestService(fakeWS{dir: dir}, nil,
		func(context.Context, string) (*proc.StatusReply, error) { return &proc.StatusReply{}, nil })
	s.store = jobstore.NewStore(jobstore.Location{StateBase: t.TempDir(), CacheDir: dir, Root: dir})

	resp, err := s.ListJobs(t.Context(), connect.NewRequest(&jobv1.ListJobsRequest{}))
	require.NoError(t, err)
	require.NotNil(t, resp.Msg.Jobs)
	require.Len(t, resp.Msg.Jobs, len(jobstore.All()), "an unwritten store handed the listing a row")
	require.NotNil(t, resp.Msg.Overlaps)
	require.Empty(t, resp.Msg.Overlaps)
}

// TestDelegatedJob_CarriesEveryStoredFact pins that a delegated row reaches the wire whole:
// the facts the store computes (why it ended, what fork could prove, the base the worker
// landed on, who declared it, who wrote into it, the integrator's verdict) are what a
// reader watching the plan needs, and `magus ls jobs` already prints every one.
func TestDelegatedJob_CarriesEveryStoredFact(t *testing.T) {
	by := types.Origin{
		User: "eli", UID: "501", EntryPoint: types.EntryPointHook, Host: "mac", Session: "s1", Agent: "a1",
		Credential: types.Credential{Kind: types.KindOperator, ID: "deadbeef", Grant: types.GrantOperator},
	}
	row := types.Job{
		ID:           "api/store",
		State:        types.StateNoReturn,
		EndReason:    "its checkout no longer exists",
		WriteProof:   types.WriteProofDisjoint,
		ReportedBase: "94d434ac0",
		BaseVerdict:  types.BaseDiverged,
		CheckoutRoot: "/w/agent-1",
		Registered:   1700000100,
		RegisteredBy: by,
		Attempt:      &types.JobAttempt{Found: true, Ref: "out1", TimestampMs: 5, Project: "api", Target: "go-test", Failed: true},
		GateAttempts: []types.JobGateAttempt{{GateID: "lint", Attempt: types.JobAttempt{Found: true, Ref: "out2"}}},
		Unattributed: []types.JobUnattributedWrite{{Path: "api/store.go", Digest: "sha", At: 7}},
		Entries:      []types.JobEntry{{Path: "api/new.go", By: by, At: 8, Consumed: 9}},
		Integration: &types.JobIntegration{
			Checkout: "/w", At: 10, Verified: true,
			Gates: []types.GateStatus{{ID: "lint", Verified: false, OutputRef: "out3", Violations: []string{"red"}}},
		},
		Result: &types.JobResult{
			Validation:   types.JobResultValidation{Command: "magus run go-test api", OutputRef: "out1"},
			GateEvidence: []types.GateEvidence{{GateID: "lint", OutputRef: "out2"}},
		},
	}
	row.Version = 11
	row.Requires = []string{"claims"}

	got := delegatedJob(row)

	require.Equal(t, "its checkout no longer exists", got.EndReason)
	require.Equal(t, "disjoint", got.WriteProof)
	require.Equal(t, "94d434ac0", got.ReportedBase)
	require.Equal(t, "diverged", got.BaseVerdict)
	require.Equal(t, "/w/agent-1", got.CheckoutRoot)
	require.Equal(t, int64(1700000100), got.Registered)
	require.Equal(t, int32(11), got.SchemaVersion)
	require.Equal(t, []string{"claims"}, got.Requires)

	wantBy := &jobv1.JobOrigin{
		User: "eli", Uid: "501", EntryPoint: "hook", Host: "mac", Session: "s1", Agent: "a1",
		Credential: &activityv1.Credential{Class: "operator", Id: "deadbeef", Grant: types.GrantOperator.String()},
	}
	require.True(t, proto.Equal(wantBy, got.RegisteredBy), "registered_by: %v", got.RegisteredBy)

	require.True(t, proto.Equal(&jobv1.JobAttempt{Found: true, Ref: "out1", TimestampMs: 5, Project: "api", Target: "go-test", Failed: true}, got.Attempt))
	require.Len(t, got.GateAttempts, 1)
	require.Equal(t, "lint", got.GateAttempts[0].GateId)
	require.Equal(t, "out2", got.GateAttempts[0].Attempt.GetRef())
	require.True(t, proto.Equal(&jobv1.JobUnattributedWrite{Path: "api/store.go", Digest: "sha", At: 7}, got.Unattributed[0]))
	require.Len(t, got.Entries, 1)
	require.Equal(t, int64(9), got.Entries[0].Consumed)
	require.True(t, proto.Equal(wantBy, got.Entries[0].By))
	require.True(t, proto.Equal(&jobv1.JobIntegration{
		Checkout: "/w", At: 10, Verified: true,
		Gates: []*jobv1.JobGateStatus{{Id: "lint", OutputRef: "out3", Violations: []string{"red"}}},
	}, got.Integration))
	require.Equal(t, "magus run go-test api", got.Result.ValidationCommand)
	require.Equal(t, "out1", got.Result.ValidationOutputRef)
	require.True(t, proto.Equal(&jobv1.JobGateEvidence{GateId: "lint", OutputRef: "out2"}, got.Result.GateEvidence[0]))

	bare := delegatedJob(types.Job{ID: "old", State: types.StatePass})
	require.Nil(t, bare.RegisteredBy, "a row from before origins were recorded has no origin to report")
	require.Nil(t, bare.Attempt)
	require.Nil(t, bare.Integration)
}

// TestListJobs_ServesTheStoresReport pins that the listing's flags are the ones the store's
// report derives, the same report `magus ls jobs` prints, so the console and the CLI
// cannot disagree about which jobs are overdue or blocked.
func TestListJobs_ServesTheStoresReport(t *testing.T) {
	dir := t.TempDir()
	store := jobstore.NewStore(jobstore.Location{StateBase: t.TempDir(), CacheDir: dir, Root: dir})
	for _, row := range []types.Job{
		{ID: "late", State: types.StateRunning, Deadline: 1},
		{ID: "waiter", State: types.StateDeclared, DependsOn: []string{"never-declared"}},
		{ID: "a", State: types.StateRunning, WritePaths: []string{"run.go#RunCI"}},
		{ID: "b", State: types.StateRunning, WritePaths: []string{"run.go#executeStages"}},
	} {
		_, err := store.Update(t.Context(), row.ID, func(cur *types.Job) {
			cur.State, cur.Deadline, cur.DependsOn, cur.WritePaths = row.State, row.Deadline, row.DependsOn, row.WritePaths
		})
		require.NoError(t, err)
	}

	s := newTestService(fakeWS{dir: dir}, nil,
		func(context.Context, string) (*proc.StatusReply, error) { return &proc.StatusReply{}, nil })
	s.store = store

	resp, err := s.ListJobs(t.Context(), connect.NewRequest(&jobv1.ListJobsRequest{}))
	require.NoError(t, err)
	staleAfter, err := store.StaleAfter()
	require.NoError(t, err)
	want, err := store.Report(t.Context(), time.Now().Unix(), staleAfter)
	require.NoError(t, err)

	require.Equal(t, []string{"late"}, resp.Msg.Overdue)
	require.Equal(t, want.Overdue, resp.Msg.Overdue)
	require.Equal(t, want.Orphans, resp.Msg.Orphans)
	require.Equal(t, want.Stale, resp.Msg.Stale)
	require.Len(t, resp.Msg.Blocked, 1)
	require.True(t, proto.Equal(&jobv1.JobBlock{Job: "waiter", On: "never-declared"}, resp.Msg.Blocked[0]))
	require.Len(t, resp.Msg.Overlaps, 1)
	require.Equal(t, types.ClaimsDisjoint, resp.Msg.Overlaps[0].Claims, "different declarations of one file are disjoint claims")
	require.Equal(t, want.Overlaps[0].Claims, resp.Msg.Overlaps[0].Claims)
}
