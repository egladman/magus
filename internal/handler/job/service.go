// Package job is the console-facing JobService handler: the daemon's CONTROL surface, the
// mutating sibling of the read-only activity/status/viewer handlers. Its RPCs submit background
// maintenance jobs (graph sync, activity-trail rotate, cache clear) through the same
// fire-and-forget, coalescing proc mechanism the CLI's `server job` uses, so a double-click never
// starts a second copy. Each response carries a metadata snapshot: the job's running state, its
// last completed run (from the activity trail), and the current size of what it maintains, so a
// caller renders a job's state in one round trip. The daemon mounts it behind the bearer guard;
// it is never served unauthenticated.
package job

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/egladman/magus/internal/cache"
	jobstore "github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/jobs"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
	jobv1 "github.com/egladman/magus/proto/gen/go/magus/job/v1alpha1"
	"github.com/egladman/magus/proto/gen/go/magus/job/v1alpha1/jobv1alpha1connect"
)

// workspace is the narrow slice of *magus.Magus the handler needs: where the trail lives and how
// big the build cache is. Satisfied structurally by *magus.Magus.
type workspace interface {
	CacheDir() string
	CacheDiskBytes() int64
}

// Service implements jobv1alpha1connect.JobServiceHandler. It submits jobs to the daemon's own proc
// socket (self-dial, so it rides the exact coalescing/journal path an external submit would) and
// reads the workspace trail + cache for the metadata it returns.
type Service struct {
	ws      workspace
	version string
	// store is where a catalog job's row lives, beside the delegated jobs. Nil leaves the
	// rows unwritten, which is what a server with no store does rather than failing a
	// submit: the job still runs, and only its row is missing.
	store *jobstore.Store
	// socket returns the daemon's proc socket address to submit to. The daemon sets
	// MAGUS_DAEMON_SOCKET on itself before serving, so the default reads that.
	socket func() string
	// submitFn and statusFn are the proc entry points, injectable so the submit/coalesce mapping
	// is unit-testable without a live daemon socket. They default to the real proc calls.
	submitFn func(ctx context.Context, addr string, argv []string, version string) (string, error)
	statusFn func(ctx context.Context, addr string) (*proc.StatusReply, error)
}

// NewService builds a JobService handler over the workspace ws, submitting jobs as version
// and recording each submitted job's row in store. store may be nil; see [Service.store].
func NewService(ws workspace, version string, store *jobstore.Store) *Service {
	return &Service{
		ws:       ws,
		version:  version,
		store:    store,
		socket:   func() string { return os.Getenv("MAGUS_DAEMON_SOCKET") },
		submitFn: proc.SubmitJob,
		statusFn: proc.QueryStatus,
	}
}

var _ jobv1alpha1connect.JobServiceHandler = (*Service)(nil)

// jobsPrefix is the resource-name collection segment. A wire name is "jobs/{id}"; the registry
// and the CLI both speak the bare id, so this is the only place the two spellings meet.
const jobsPrefix = "jobs/"

// ListJobs returns every job, the daemon's own catalog beside the delegated ones, with each
// one's holder, state, last run and target size.
//
// The catalog leads and the stored rows follow, so the fixed set a reader can act on stays
// in one place while the plan grows under it. A catalog job nobody has run yet still lists,
// which is why the catalog is iterated rather than the store: the set is declared by the
// binary, and a job with no row has not run rather than not existing.
func (s *Service) ListJobs(ctx context.Context, _ *connect.Request[jobv1.ListJobsRequest]) (*connect.Response[jobv1.ListJobsResponse], error) {
	running := s.runningByArgv(ctx)
	rows := s.rows()
	byID := make(map[string]types.Job, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	all := jobs.All()
	out := make([]*jobv1.Job, 0, len(all)+len(rows))
	catalog := make(map[string]bool, len(all))
	for _, j := range all {
		catalog[j.Name] = true
		out = append(out, s.job(j, running, byID[j.Name]))
	}
	for _, row := range rows {
		if !catalog[row.ID] {
			out = append(out, delegatedJob(row))
		}
	}
	return connect.NewResponse(&jobv1.ListJobsResponse{Jobs: out, Overlaps: overlaps(rows)}), nil
}

// rows reads the job store, empty when there is none or it will not read. A listing that
// drops the delegated jobs beats one that fails: the catalog beside it is still true, and
// the daemon's maintenance surface must not go dark because a plan file is unreadable.
func (s *Service) rows() []types.Job {
	if s.store == nil {
		return nil
	}
	rows, err := s.store.List()
	if err != nil {
		return nil
	}
	return rows
}

// row is the stored row for the job named name, zero when nothing has recorded one.
func (s *Service) row(name string) types.Job {
	for _, r := range s.rows() {
		if r.ID == name {
			return r
		}
	}
	return types.Job{}
}

// overlaps derives the pairs claiming common ground through the same constructor every
// other read door uses, so two doors cannot disagree about whether an overlap exists.
func overlaps(rows []types.Job) []*jobv1.JobOverlap {
	derived := types.NewJobList(rows).Overlaps
	out := make([]*jobv1.JobOverlap, 0, len(derived))
	for _, o := range derived {
		out = append(out, &jobv1.JobOverlap{JobA: o.JobA, JobB: o.JobB, PathsA: o.PathsA, PathsB: o.PathsB})
	}
	return out
}

// RunJob submits the named job. An unregistered name is NotFound rather than a SubmitState:
// the enum reports how a valid submission resolved, and a name nobody registered never became
// one. That is also why this is the only RPC that can reject its input: the four verbs it
// replaced took no argument, so they had nothing to get wrong.
func (s *Service) RunJob(ctx context.Context, req *connect.Request[jobv1.RunJobRequest]) (*connect.Response[jobv1.RunJobResponse], error) {
	id := strings.TrimPrefix(req.Msg.GetName(), jobsPrefix)
	if _, ok := jobs.Lookup(id); !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("job: unknown job %q", req.Msg.GetName()))
	}
	return s.submit(ctx, id)
}

// submit resolves name to its worker argv, submits it to the daemon, and builds the response.
// A coalesced submit (empty invocation id back) is ALREADY_RUNNING, not an error: the response
// still carries the running job's id and its metadata. Only real failures use error codes.
func (s *Service) submit(ctx context.Context, name string) (*connect.Response[jobv1.RunJobResponse], error) {
	j, ok := jobs.Lookup(name)
	if !ok { // RunJob already rejected an unknown name, so reaching here is a programmer error
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("job: unknown job %q", name))
	}
	addr := s.socket()
	if addr == "" {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("job: no daemon socket to submit to; run `magus server start`"))
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
	info := s.job(j, running, s.row(j.Name))

	state := jobv1.SubmitState_SUBMIT_STATE_SUBMITTED
	if inv == "" { // the daemon coalesced this into an identical in-flight job
		state = jobv1.SubmitState_SUBMIT_STATE_ALREADY_RUNNING
		inv = running[argvKey(j.Argv)] // report the already-running invocation
	}
	s.recordSubmit(ctx, j, inv)
	return connect.NewResponse(&jobv1.RunJobResponse{
		State:        state,
		InvocationId: inv,
		ConsoleUrl:   "", // TODO: deep-link once the /logs page accepts an invocation fragment
		Job:          info,
	}), nil
}

// recordSubmit upserts the catalog job's row as running and records the invocation id.
// This is the only place that CAN record it: the completion callback is handed argv,
// duration and error and no invocation, so a run first written when it ends could never
// name the log it produced.
//
// Best-effort, like the trail the daemon writes beside it. The store refuses a write from
// a checkout bound to a lease, so a daemon serving a worker's worktree leaves the row
// alone rather than failing a submit that otherwise succeeded.
func (s *Service) recordSubmit(ctx context.Context, j jobs.Job, inv string) {
	if s.store == nil {
		return
	}
	if _, err := s.store.Update(ctx, j.Name, func(row *types.Job) {
		row.Holder = types.HolderDaemon
		row.Goal = j.Desc
		row.State = types.StateRunning
		if row.LastRun == nil {
			row.LastRun = &types.JobRun{}
		}
		row.LastRun.Invocation = inv
	}); err != nil {
		slog.DebugContext(ctx, "job: recording the submitted job's row failed",
			slog.String("job", j.Name), slog.String("error", err.Error()))
	}
}

// job assembles a job's descriptor plus its running state, last completed run (from the
// trail), and the current size of what it maintains. running maps a worker-argv key to the live
// invocation id, so ListJobs and submit share one status query.
func (s *Service) job(j jobs.Job, running map[string]string, row types.Job) *jobv1.Job {
	info := &jobv1.Job{
		Name:        jobsPrefix + j.Name,
		Id:          j.Name,
		Holder:      jobv1.JobHolder_JOB_HOLDER_DAEMON,
		Description: j.Desc,
		State:       string(types.StateDeclared),
		Target:      s.targetSize(j),
	}
	if row.State != "" {
		info.State = string(row.State)
	}
	if _, ok := running[argvKey(j.Argv)]; ok {
		info.Running = true
	}
	// The row first, because only it carries the invocation id. The trail is the fallback
	// for a job that ran before anything wrote rows, where a run with no id still beats none.
	if row.LastRun != nil {
		info.LastRun = storedRun(row.LastRun)
	} else if ev, ok := trail.LastRun(s.ws.CacheDir(), jobs.ActionString(j.Argv)); ok {
		info.LastRun = lastRun(ev)
	}
	return info
}

// delegatedJob maps a stored row to the wire Job: what an orchestrator DECLARED about work
// it handed out. It carries no description or target size, which are a catalog job's; a
// delegated job's equivalents are its goal and the lanes it was given.
//
// Running is left unset rather than derived from the state. Nothing here watched the
// worker, and a row still reading `running` after its holder died would be the stored
// running flag this service refuses to keep for the catalog.
func delegatedJob(row types.Job) *jobv1.Job {
	j := &jobv1.Job{
		Name:       jobsPrefix + row.ID,
		Id:         row.ID,
		Holder:     jobv1.JobHolder_JOB_HOLDER_SESSION,
		State:      string(row.State),
		Goal:       row.Goal,
		Parent:     row.Parent,
		Model:      row.Model,
		Check:      row.Validation,
		WritePaths: row.WritePaths,
		DenyPaths:  row.DenyPaths,
		ReadPaths:  row.ReadPaths,
		DependsOn:  row.DependsOn,
		ReadOnly:   row.ReadOnly,
		Checkpoint: row.Checkpoint,
		Created:    row.Created,
		Updated:    row.Updated,
	}
	if row.Holder.OrSession() == types.HolderDaemon {
		j.Holder = jobv1.JobHolder_JOB_HOLDER_DAEMON
	}
	for _, r := range row.Releases {
		j.Releases = append(j.Releases, &jobv1.JobRelease{Path: r.Path, Digest: r.Digest, ReleasedAt: r.ReleasedAt})
	}
	if row.LastRun != nil {
		j.LastRun = storedRun(row.LastRun)
	}
	return j
}

// storedRun maps the row's own run record to the wire JobRun.
func storedRun(r *types.JobRun) *jobv1.JobRun {
	run := &jobv1.JobRun{
		InvocationId:   r.Invocation,
		Ok:             r.OK,
		Error:          r.Error,
		ItemsRemoved:   r.ItemsRemoved,
		BytesReclaimed: r.BytesReclaimed,
	}
	if r.Ended > 0 {
		run.EndTime = timestamppb.New(time.UnixMilli(r.Ended))
	}
	if r.DurationMs > 0 {
		run.Duration = durationpb.New(time.Duration(r.DurationMs) * time.Millisecond)
	}
	return run
}

// lastRun maps a trail job Event to the wire JobRun. The trail records the run's start (Ts) and
// duration, so end_time is Ts+duration. The trail carries no invocation id or per-run delta,
// so those fields stay zero, additive to fill in later.
func lastRun(e trail.Event) *jobv1.JobRun {
	run := &jobv1.JobRun{
		EndTime: timestamppb.New(time.UnixMilli(e.Ts + e.DurMs)),
		Ok:      e.Outcome == trail.OutcomeOK,
		Error:   e.Error,
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
		return &jobv1.ResourceSize{SizeBytes: bytes, ItemCount: count}
	case jobs.NameRotateLogs:
		bytes, count := cache.NewOutputStore(s.ws.CacheDir()).RunsStat()
		return &jobv1.ResourceSize{SizeBytes: bytes, ItemCount: count}
	case jobs.NameClearCache:
		return &jobv1.ResourceSize{SizeBytes: s.ws.CacheDiskBytes()}
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
