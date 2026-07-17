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

	"github.com/egladman/magus/internal/jobs"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/trail"
	jobv1 "github.com/egladman/magus/proto/gen/go/magus/job/v1"
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

	resp, err := s.ClearCache(context.Background(), connect.NewRequest(&jobv1.ClearCacheRequest{}))
	require.NoError(t, err)
	require.Equal(t, jobv1.SubmitState_SUBMIT_STATE_SUBMITTED, resp.Msg.State)
	require.Equal(t, "inv-new", resp.Msg.InvocationId)
	require.Equal(t, "clear-cache", resp.Msg.Job.Name)
	require.False(t, resp.Msg.Job.Running)
	require.Equal(t, int64(4096), resp.Msg.Job.Target.Bytes) // clear-cache target is the cache size
}

func TestSubmit_CoalescedReportsRunningInvocation(t *testing.T) {
	ws := fakeWS{dir: t.TempDir()}
	submit := func(context.Context, string, []string, string) (string, error) { return "", nil } // coalesced
	// The daemon reports an identical rotate-activities job already in flight.
	status := func(context.Context, string) (*proc.StatusReply, error) {
		return &proc.StatusReply{Calls: []proc.Call{{Args: []string{"server", "rotate-activities"}, Inv: "inv-running"}}}, nil
	}
	s := newTestService(ws, submit, status)

	resp, err := s.RotateActivities(context.Background(), connect.NewRequest(&jobv1.RotateActivitiesRequest{}))
	require.NoError(t, err)
	require.Equal(t, jobv1.SubmitState_SUBMIT_STATE_ALREADY_RUNNING, resp.Msg.State)
	require.Equal(t, "inv-running", resp.Msg.InvocationId)
	require.True(t, resp.Msg.Job.Running)
}

func TestSubmit_ProcErrorIsInternal(t *testing.T) {
	submit := func(context.Context, string, []string, string) (string, error) { return "", errors.New("boom") }
	status := func(context.Context, string) (*proc.StatusReply, error) { return &proc.StatusReply{}, nil }
	s := newTestService(fakeWS{dir: t.TempDir()}, submit, status)

	_, err := s.SyncGraph(context.Background(), connect.NewRequest(&jobv1.SyncGraphRequest{}))
	require.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

func TestSubmit_NoSocketIsUnavailable(t *testing.T) {
	s := newTestService(fakeWS{dir: t.TempDir()},
		func(context.Context, string, []string, string) (string, error) { return "x", nil },
		func(context.Context, string) (*proc.StatusReply, error) { return &proc.StatusReply{}, nil })
	s.socket = func() string { return "" } // no daemon socket

	_, err := s.SyncGraph(context.Background(), connect.NewRequest(&jobv1.SyncGraphRequest{}))
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(err))
}

func TestJobInfo_LastRunFromTrailAndTargetSize(t *testing.T) {
	dir := t.TempDir()
	// A completed rotate-activities job in the trail, plus two more events so Stat has a count of 3.
	start := time.UnixMilli(1_000_000).UnixMilli()
	trail.Append(dir, trail.Event{Ts: start, Kind: trail.KindJob, Actor: "daemon", Action: "server rotate-activities", Outcome: trail.OutcomeOK, DurMs: 250})
	trail.Append(dir, trail.Event{Ts: start + 1, Kind: trail.KindMCPToolCall, Actor: "a", Action: "query", Outcome: trail.OutcomeOK})
	trail.Append(dir, trail.Event{Ts: start + 2, Kind: trail.KindMCPToolCall, Actor: "a", Action: "explain", Outcome: trail.OutcomeOK})

	s := newTestService(fakeWS{dir: dir}, nil, nil)
	running := map[string]string{argvKey([]string{"server", "rotate-activities"}): "inv-live"}

	j, ok := jobs.Lookup("rotate-activities")
	require.True(t, ok)
	got := s.jobInfo(j, running)

	require.Equal(t, "rotate-activities", got.Name)
	require.True(t, got.Running)
	require.Equal(t, int64(3), got.Target.Count) // three trail events on disk
	require.Positive(t, got.Target.Bytes)

	wantRun := &jobv1.JobRun{
		FinishedAt: timestamppb.New(time.UnixMilli(start + 250)),
		Duration:   durationpb.New(250 * time.Millisecond),
		Ok:         true,
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
	require.Equal(t, []string{"sync-graph", "rotate-activities", "rotate-logs", "clear-cache"}, names)
}
