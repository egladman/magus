// Package job is the console-facing JobService handler: the daemon's CONTROL surface, the
// mutating sibling of the read-only activity/status/viewer handlers. Its RPCs submit background
// maintenance jobs (graph sync, activity-trail rotate, cache clear) through the same
// fire-and-forget, coalescing proc mechanism the CLI's `server job` uses, so a double-click never
// starts a second copy. Each response carries a metadata snapshot - the job's running state, its
// last completed run (from the activity trail), and the current size of what it maintains - so a
// caller renders a job's state in one round trip. The daemon mounts it behind the bearer guard;
// it is never served unauthenticated.
package job

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/jobs"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/trail"
	jobv1 "github.com/egladman/magus/proto/gen/go/magus/job/v1"
	"github.com/egladman/magus/proto/gen/go/magus/job/v1/jobv1connect"
)

// workspace is the narrow slice of *magus.Magus the handler needs: where the trail lives and how
// big the build cache is. Satisfied structurally by *magus.Magus.
type workspace interface {
	CacheDir() string
	CacheDiskBytes() int64
}

// Service implements jobv1connect.JobServiceHandler. It submits jobs to the daemon's own proc
// socket (self-dial, so it rides the exact coalescing/journal path an external submit would) and
// reads the workspace trail + cache for the metadata it returns.
type Service struct {
	ws      workspace
	version string
	// socket returns the daemon's proc socket address to submit to. The daemon sets
	// MAGUS_DAEMON_SOCKET on itself before serving, so the default reads that.
	socket func() string
	// submitFn and statusFn are the proc entry points, injectable so the submit/coalesce mapping
	// is unit-testable without a live daemon socket. They default to the real proc calls.
	submitFn func(ctx context.Context, addr string, argv []string, version string) (string, error)
	statusFn func(ctx context.Context, addr string) (*proc.StatusReply, error)
}

// NewService builds a JobService handler over the workspace ws, submitting jobs as version.
func NewService(ws workspace, version string) *Service {
	return &Service{
		ws:       ws,
		version:  version,
		socket:   func() string { return os.Getenv("MAGUS_DAEMON_SOCKET") },
		submitFn: proc.SubmitJob,
		statusFn: proc.QueryStatus,
	}
}

var _ jobv1connect.JobServiceHandler = (*Service)(nil)

func (s *Service) SyncGraph(ctx context.Context, _ *connect.Request[jobv1.SyncGraphRequest]) (*connect.Response[jobv1.SubmitJobResponse], error) {
	return s.submit(ctx, jobs.NameSyncGraph)
}

func (s *Service) RotateActivities(ctx context.Context, _ *connect.Request[jobv1.RotateActivitiesRequest]) (*connect.Response[jobv1.SubmitJobResponse], error) {
	return s.submit(ctx, jobs.NameRotateActivities)
}

func (s *Service) ClearCache(ctx context.Context, _ *connect.Request[jobv1.ClearCacheRequest]) (*connect.Response[jobv1.SubmitJobResponse], error) {
	return s.submit(ctx, jobs.NameClearCache)
}

func (s *Service) RotateLogs(ctx context.Context, _ *connect.Request[jobv1.RotateLogsRequest]) (*connect.Response[jobv1.SubmitJobResponse], error) {
	return s.submit(ctx, jobs.NameRotateLogs)
}

// ListJobs returns every registered job with its running state, last run, and target size.
func (s *Service) ListJobs(ctx context.Context, _ *connect.Request[jobv1.ListJobsRequest]) (*connect.Response[jobv1.ListJobsResponse], error) {
	running := s.runningByArgv(ctx)
	all := jobs.All()
	out := make([]*jobv1.JobInfo, 0, len(all))
	for _, j := range all {
		out = append(out, s.jobInfo(j, running))
	}
	return connect.NewResponse(&jobv1.ListJobsResponse{Jobs: out}), nil
}

// submit resolves name to its worker argv, submits it to the daemon, and builds the response.
// A coalesced submit (empty invocation id back) is ALREADY_RUNNING, not an error: the response
// still carries the running job's id and its metadata. Only real failures use error codes.
func (s *Service) submit(ctx context.Context, name string) (*connect.Response[jobv1.SubmitJobResponse], error) {
	j, ok := jobs.Lookup(name)
	if !ok { // registry and handler are in the same binary, so this is a programmer error
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("job: unknown job %q", name))
	}
	addr := s.socket()
	if addr == "" {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("job: no daemon socket to submit to"))
	}
	// Snapshot the running set BEFORE submitting: on a coalesced submit the already-running job
	// predates our call, so it is reliably in this snapshot; a query taken AFTER the submit could
	// miss it if it finishes in the race window, leaving an ALREADY_RUNNING reply with an empty
	// invocation id. It also feeds the response metadata (Running reflects state at submit time).
	running := s.runningByArgv(ctx)
	inv, err := s.submitFn(ctx, addr, j.Argv, s.version)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	info := s.jobInfo(j, running)

	state := jobv1.SubmitState_SUBMIT_STATE_SUBMITTED
	if inv == "" { // the daemon coalesced this into an identical in-flight job
		state = jobv1.SubmitState_SUBMIT_STATE_ALREADY_RUNNING
		inv = running[argvKey(j.Argv)] // report the already-running invocation
	}
	return connect.NewResponse(&jobv1.SubmitJobResponse{
		State:        state,
		InvocationId: inv,
		ConsoleUrl:   "", // TODO: deep-link once the /logs page accepts an invocation fragment
		Job:          info,
	}), nil
}

// jobInfo assembles a job's descriptor plus its running state, last completed run (from the
// trail), and the current size of what it maintains. running maps a worker-argv key to the live
// invocation id, so ListJobs and submit share one status query.
func (s *Service) jobInfo(j jobs.Job, running map[string]string) *jobv1.JobInfo {
	info := &jobv1.JobInfo{
		Name:        j.Name,
		Description: j.Desc,
		Target:      s.targetSize(j),
	}
	if _, ok := running[argvKey(j.Argv)]; ok {
		info.Running = true
	}
	if ev, ok := trail.LastRun(s.ws.CacheDir(), jobs.ActionString(j.Argv)); ok {
		info.LastRun = lastRun(ev)
	}
	return info
}

// lastRun maps a trail job Event to the wire JobRun. The trail records the run's start (Ts) and
// duration, so finished_at is Ts+duration. The trail carries no invocation id or per-run delta,
// so those fields stay zero - additive to fill in later.
func lastRun(e trail.Event) *jobv1.JobRun {
	run := &jobv1.JobRun{
		FinishedAt: timestamppb.New(time.UnixMilli(e.Ts + e.DurMs)),
		Ok:         e.Outcome == trail.OutcomeOK,
		Error:      e.Error,
	}
	if e.DurMs > 0 {
		run.Duration = durationpb.New(time.Duration(e.DurMs) * time.Millisecond)
	}
	return run
}

// targetSize is the current magnitude of what a job maintains. Not every job shrinks a resource
// (sync-graph reconciles rather than trims), so an unmapped job reports a zero size.
func (s *Service) targetSize(j jobs.Job) *jobv1.ResourceSize {
	switch j.Name {
	case jobs.NameRotateActivities:
		bytes, count := trail.Stat(s.ws.CacheDir())
		return &jobv1.ResourceSize{Bytes: bytes, Count: count}
	case jobs.NameRotateLogs:
		bytes, count := cache.NewOutputStore(s.ws.CacheDir()).RunsStat()
		return &jobv1.ResourceSize{Bytes: bytes, Count: count}
	case jobs.NameClearCache:
		return &jobv1.ResourceSize{Bytes: s.ws.CacheDiskBytes()}
	default:
		return &jobv1.ResourceSize{}
	}
}

// runningByArgv snapshots the daemon's live calls into a map from worker-argv key to invocation
// id, so callers can tell whether a given job is in flight (and which invocation). A failed
// status query yields an empty map: nothing shows as running, never an error.
func (s *Service) runningByArgv(ctx context.Context) map[string]string {
	out := map[string]string{}
	st, err := s.statusFn(ctx, s.socket())
	if err != nil || st == nil {
		return out
	}
	for _, c := range st.Calls {
		out[argvKey(c.Args)] = c.Inv
	}
	return out
}

// argvKey is a stable map key for a worker argv. \x00 cannot appear in a shell token, so joining
// on it is collision-free where a space join would conflate ["a","b"] with ["a b"].
func argvKey(argv []string) string { return strings.Join(argv, "\x00") }
