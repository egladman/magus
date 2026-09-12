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
	"github.com/egladman/magus/internal/jobs"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/trail"
	jobv1 "github.com/egladman/magus/proto/gen/go/magus/job/v1alpha1"
	"github.com/egladman/magus/types"
)

// fakeWS is a workspace whose trail lives at dir and whose cache reports a fixed size.
type fakeWS struct {
	dir        string
	cacheBytes int64
}

func (f fakeWS) CacheDir() string      { return f.dir }
func (f fakeWS) CacheDiskBytes() int64 { return f.cacheBytes }

// newTestService builds a Service with injected proc seams so no live daemon is needed.
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
	// The daemon reports an identical rotate-activities job already in flight.
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
	s.socket = func() string { return "" } // no daemon socket

	_, err := s.RunJob(context.Background(), connect.NewRequest(&jobv1.RunJobRequest{Name: "jobs/sync-graph"}))
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
}

func TestJobInfo_LastRunFromTrailAndTargetSize(t *testing.T) {
	dir := t.TempDir()
	// A completed rotate-activities job in the trail, plus two more events so Stat has a count of 3.
	start := time.UnixMilli(1_000_000).UnixMilli()
	trail.Append(t.Context(), dir, trail.Event{Ts: start, Kind: trail.KindJob, Actor: "daemon", Action: "server rotate-activities", Outcome: trail.OutcomeOK, DurMs: 250})
	trail.Append(t.Context(), dir, trail.Event{Ts: start + 1, Kind: trail.KindMCPToolCall, Actor: "a", Action: "query", Outcome: trail.OutcomeOK})
	trail.Append(t.Context(), dir, trail.Event{Ts: start + 2, Kind: trail.KindMCPToolCall, Actor: "a", Action: "explain", Outcome: trail.OutcomeOK})

	s := newTestService(fakeWS{dir: dir}, nil, nil)
	running := map[string]string{argvKey([]string{"server", "rotate-activities"}): "inv-live"}

	j, ok := jobs.Lookup("rotate-activities")
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
		"jobs/clear-cache", "jobs/check-review",
	}, names)
}

// TestListJobs_ReturnsCatalogAndDelegatedJobs is the listing the merge exists for: the
// daemon's own catalog beside the jobs a session holds, in one response with one state
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
	require.NotNil(t, catalog, "the daemon's own job is missing from the listing")
	require.Equal(t, jobv1.JobHolder_JOB_HOLDER_DAEMON, catalog.Holder)
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
		row.Goal = "ship the store"
		row.Checkpoint = "60dc9151"
		row.WritePaths = []string{"internal/job", "types/job.go"}
		row.State = types.StateRunning
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
	require.Equal(t, "ship the store", got.Goal)
	require.Equal(t, "60dc9151", got.Checkpoint)
	require.Equal(t, []string{"internal/job"}, got.WritePaths)
	require.Equal(t, stored.Updated, got.Updated, "the row's own stamp, not the moment it was read")
	require.Len(t, got.Releases, 1)
	require.Equal(t, "types/job.go", got.Releases[0].Path)
	require.NotEmpty(t, got.Releases[0].Digest, "a release says which version of the path the next worker inherits")

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
	require.Len(t, resp.Msg.Jobs, len(jobs.All()), "an unwritten store handed the listing a row")
	require.NotNil(t, resp.Msg.Overlaps)
	require.Empty(t, resp.Msg.Overlaps)
}
