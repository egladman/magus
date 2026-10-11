// Package job is the console-facing JobService handler: the server's CONTROL service, the
// mutating sibling of the read-only activity/status/viewer handlers. Its RPCs submit background
// maintenance jobs (graph sync, activity-trail rotate, cache clear) through the same
// fire-and-forget, coalescing proc mechanism the CLI's `magus job run` uses, so a double-click never
// starts a second copy. Each response carries a metadata snapshot: the job's running state, its
// last completed run (from the activity trail), and the current size of what it maintains, so a
// caller renders a job's state in one round trip. The server mounts it behind the bearer guard;
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
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/internal/trail"
	activityv1 "github.com/egladman/magus/proto/gen/go/magus/activity/v1alpha1"
	jobv1 "github.com/egladman/magus/proto/gen/go/magus/job/v1alpha1"
	"github.com/egladman/magus/proto/gen/go/magus/job/v1alpha1/jobv1alpha1connect"
	"github.com/egladman/magus/types"
)

// workspace is the narrow slice of *magus.Magus the handler needs: where the trail lives and how
// big the build cache is. Satisfied structurally by *magus.Magus.
type workspace interface {
	CacheDir() string
	CacheDiskBytes() int64
}

// Service implements jobv1alpha1connect.JobServiceHandler. It submits jobs to the server's own proc
// socket (self-dial, so it rides the exact coalescing/journal path an external submit would) and
// reads the workspace trail + cache for the metadata it returns.
type Service struct {
	ws      workspace
	version string
	// store is where a catalog job's row lives, beside the delegated jobstore. Nil leaves the
	// rows unwritten, which is what a server with no store does rather than failing a
	// submit: the job still runs, and only its row is missing.
	store *jobstore.Store
	// socket returns the server's proc socket address to submit to. The server sets
	// MAGUS_PROC_SOCKET on itself before serving, so the default reads that.
	socket func() string
	// submitFn and statusFn are the proc entry points, injectable so the submit/coalesce mapping
	// is unit-testable without a live server socket. They default to the real proc calls.
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
		socket:   func() string { return os.Getenv("MAGUS_PROC_SOCKET") },
		submitFn: proc.SubmitJob,
		statusFn: proc.QueryStatus,
	}
}

var _ jobv1alpha1connect.JobServiceHandler = (*Service)(nil)

// jobsPrefix is the resource-name collection segment. A wire name is "jobs/{id}"; the registry
// and the CLI both speak the bare id, so this is the only place the two spellings meet.
const jobsPrefix = "jobs/"

// ListJobs returns every job, the server's own catalog beside the delegated ones, with each
// one's holder, state, last run and target size.
//
// The catalog leads and the stored rows follow, so the fixed set a reader can act on stays
// in one place while the plan grows under it. A catalog job nobody has run yet still lists,
// which is why the catalog is iterated rather than the store: the set is declared by the
// binary, and a job with no row has not run rather than not existing.
func (s *Service) ListJobs(ctx context.Context, _ *connect.Request[jobv1.ListJobsRequest]) (*connect.Response[jobv1.ListJobsResponse], error) {
	running := s.runningByArgv(ctx)
	list := s.report(ctx)
	byID := make(map[string]types.Job, len(list.Jobs))
	for _, row := range list.Jobs {
		byID[row.ID] = row
	}
	all := jobstore.All()
	out := make([]*jobv1.Job, 0, len(all)+len(list.Jobs))
	catalog := make(map[string]bool, len(all))
	for _, j := range all {
		catalog[j.Name] = true
		out = append(out, s.job(j, running, byID[j.Name]))
	}
	for _, row := range list.Jobs {
		if !catalog[row.ID] {
			out = append(out, delegatedJob(row))
		}
	}
	resp := &jobv1.ListJobsResponse{
		Jobs:     out,
		Overlaps: wireOverlaps(list.Overlaps),
		Overdue:  list.Overdue,
		Orphans:  list.Orphans,
		Stale:    list.Stale,
	}
	for _, b := range list.Blocked {
		resp.Blocked = append(resp.Blocked, &jobv1.JobBlock{Job: b.Job, On: b.On, State: string(b.State)})
	}
	for _, r := range list.ReadOnly {
		resp.ReadOnly = append(resp.ReadOnly, &jobv1.JobReadOnly{Job: r.Job, Lacks: r.Lacks})
	}
	return connect.NewResponse(resp), nil
}

// report is the store's [jobstore.Store.Report], the list `magus ls jobs` prints, empty
// when there is no store or it will not read. A listing that drops the delegated jobs
// beats one that fails: the catalog beside it is still true, and the server's maintenance
// service must not go dark because a plan file is unreadable. An unreadable stale window
// flags no row stale and is logged, for the same reason.
func (s *Service) report(ctx context.Context) types.JobList {
	if s.store == nil {
		return types.NewJobList(nil)
	}
	staleAfter, err := s.store.StaleAfter()
	if err != nil {
		slog.WarnContext(ctx, "reading jobs.stale_after failed; no row is flagged stale",
			attr.Error(err))
	}
	list, err := s.store.Report(ctx, time.Now().Unix(), staleAfter)
	if err != nil {
		return types.NewJobList(nil)
	}
	return list
}

// rows reads the job store, empty when there is none or it will not read; see [Service.report].
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

// wireOverlaps maps the overlaps [jobstore.Store.Report] derived and measured.
func wireOverlaps(derived []types.JobOverlap) []*jobv1.JobOverlap {
	out := make([]*jobv1.JobOverlap, 0, len(derived))
	for _, o := range derived {
		w := &jobv1.JobOverlap{JobA: o.JobA, JobB: o.JobB, PathsA: o.PathsA, PathsB: o.PathsB, Claims: o.Claims}
		if f := o.Footprint; f != nil {
			w.Footprint = &jobv1.JobOverlapFootprint{Verdict: f.Verdict, Shared: f.Shared, Reason: f.Reason}
		}
		out = append(out, w)
	}
	return out
}

// RunJob submits the named job. An unregistered name is NotFound rather than a SubmitState:
// the enum reports how a valid submission resolved, and a name nobody registered never became
// one. That is also why this is the only RPC that can reject its input: the four verbs it
// replaced took no argument, so they had nothing to get wrong.
func (s *Service) RunJob(ctx context.Context, req *connect.Request[jobv1.RunJobRequest]) (*connect.Response[jobv1.RunJobResponse], error) {
	id := strings.TrimPrefix(req.Msg.GetName(), jobsPrefix)
	if _, ok := jobstore.Lookup(id); !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("job: unknown job %q", req.Msg.GetName()))
	}
	return s.submit(ctx, id)
}

// submit resolves name to its worker argv, submits it to the server, and builds the response.
// A coalesced submit (empty invocation id back) is ALREADY_RUNNING, not an error: the response
// still carries the running job's id and its metadata. Only real failures use error codes.
func (s *Service) submit(ctx context.Context, name string) (*connect.Response[jobv1.RunJobResponse], error) {
	j, ok := jobstore.Lookup(name)
	if !ok { // RunJob already rejected an unknown name, so reaching here is a programmer error
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("job: unknown job %q", name))
	}
	addr := s.socket()
	if addr == "" {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("job: no server socket to submit to, run `magus server start`"))
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
	if inv == "" { // the server coalesced this into an identical in-flight job
		state = jobv1.SubmitState_SUBMIT_STATE_ALREADY_RUNNING
		inv = running[argvKey(j.Argv)] // report the already-running invocation
	}
	s.recordSubmit(ctx, j, inv)
	return connect.NewResponse(&jobv1.RunJobResponse{
		State:        state,
		InvocationId: inv,
		ConsoleUrl:   consoleURL(inv),
		Job:          info,
	}), nil
}

// recordSubmit upserts the catalog job's row as running and records the invocation id.
// This is the only place that CAN record it: the completion callback is handed argv,
// duration and error and no invocation, so a run first written when it ends could never
// name the log it produced.
//
// Best-effort, like the trail the server writes beside it. The store refuses a write from
// a checkout bound to a lease, so a server serving a worker's worktree leaves the row
// alone rather than failing a submit that otherwise succeeded.
func (s *Service) recordSubmit(ctx context.Context, j jobstore.CatalogEntry, inv string) {
	if s.store == nil {
		return
	}
	if _, err := s.store.Update(ctx, j.Name, func(row *types.Job) {
		row.Holder = types.HolderServer
		row.Criteria = j.Desc
		row.State = types.StateRunning
		if row.LastRun == nil {
			row.LastRun = &types.JobRun{}
		}
		row.LastRun.Invocation = inv
	}); err != nil {
		slog.With(attr.Component("job")).DebugContext(ctx, "recording the submitted job's row failed",
			slog.String("job", j.Name), attr.Error(err))
	}
}

// job assembles a job's descriptor plus its running state, last completed run (from the
// trail), and the current size of what it maintains. running maps a worker-argv key to the live
// invocation id, so ListJobs and submit share one status query.
func (s *Service) job(j jobstore.CatalogEntry, running map[string]string, row types.Job) *jobv1.Job {
	info := &jobv1.Job{
		Name:        jobsPrefix + j.Name,
		Id:          j.Name,
		Holder:      jobv1.JobHolder_JOB_HOLDER_SERVER,
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
	} else if ev, ok := trail.LastRun(s.ws.CacheDir(), jobstore.ActionString(j.Argv)); ok {
		info.LastRun = lastRun(ev)
	}
	info.Goals = wireGoals(row.Goals)
	info.Result = wireJobResult(row.Result)
	return info
}

// delegatedJob maps a stored row to the wire Job: what an orchestrator DECLARED about work
// it handed out. It carries no description or target size, which are a catalog job's; a
// delegated job's equivalents are its criteria and the paths it was given.
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
		Criteria:   row.Criteria,
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
		Goals:      wireGoals(row.Goals),
		Result:     wireJobResult(row.Result),
		Deadline:   row.Deadline,

		EndReason:     row.EndReason,
		WriteProof:    string(row.WriteProof),
		ReportedBase:  row.ReportedBase,
		BaseVerdict:   string(row.BaseVerdict),
		CheckoutRoot:  row.CheckoutRoot,
		Registered:    row.Registered,
		RegisteredBy:  wireOrigin(row.RegisteredBy),
		Attempt:       wireAttempt(row.Attempt),
		Integration:   wireIntegration(row.Integration),
		SchemaVersion: int32(row.Version),
		Requires:      row.Requires,
	}
	if row.Holder.OrSession() == types.HolderServer {
		j.Holder = jobv1.JobHolder_JOB_HOLDER_SERVER
	}
	for _, r := range row.Releases {
		j.Releases = append(j.Releases, &jobv1.JobRelease{Path: r.Path, Digest: r.Digest, ReleasedAt: r.ReleasedAt})
	}
	for _, g := range row.GateAttempts {
		j.GateAttempts = append(j.GateAttempts, &jobv1.JobGateAttempt{GateId: g.GateID, Attempt: wireAttempt(&g.Attempt)})
	}
	for _, u := range row.Unattributed {
		j.Unattributed = append(j.Unattributed, &jobv1.JobUnattributedWrite{Path: u.Path, Digest: u.Digest, At: u.At})
	}
	for _, e := range row.Entries {
		j.Entries = append(j.Entries, &jobv1.JobEntry{Path: e.Path, By: wireOrigin(e.By), At: e.At, Consumed: e.Consumed})
	}
	if row.LastRun != nil {
		j.LastRun = storedRun(row.LastRun)
	}
	return j
}

// wireOrigin maps who did something to a row, nil for the zero Origin a row written before
// origins were recorded carries.
func wireOrigin(o types.Origin) *jobv1.JobOrigin {
	if o == (types.Origin{}) {
		return nil
	}
	w := &jobv1.JobOrigin{
		User: o.User, Uid: o.UID, EntryPoint: string(o.EntryPoint),
		Host: o.Host, Session: o.Session, Agent: o.Agent,
	}
	if c := o.Credential; c != (types.Credential{}) {
		w.Credential = &activityv1.Credential{Class: string(c.Kind), Id: c.ID, Name: c.Name, Grant: c.Grant.String()}
	}
	return w
}

func wireAttempt(a *types.JobAttempt) *jobv1.JobAttempt {
	if a == nil {
		return nil
	}
	return &jobv1.JobAttempt{
		Found: a.Found, Ref: a.Ref, TimestampMs: a.TimestampMs,
		Project: a.Project, Target: a.Target, Spell: a.Spell, Failed: a.Failed,
	}
}

func wireIntegration(in *types.JobIntegration) *jobv1.JobIntegration {
	if in == nil {
		return nil
	}
	w := &jobv1.JobIntegration{Checkout: in.Checkout, At: in.At, Verified: in.Verified}
	for _, g := range in.Gates {
		w.Gates = append(w.Gates, &jobv1.JobGateStatus{Id: g.ID, Verified: g.Verified, OutputRef: g.OutputRef, Violations: g.Violations})
	}
	return w
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
		EndTime: timestamppb.New(time.UnixMilli(e.Ts + e.DurationMs)),
		Ok:      e.Outcome == trail.OutcomeOK,
		Error:   e.Error,
	}
	if e.DurationMs > 0 {
		run.Duration = durationpb.New(time.Duration(e.DurationMs) * time.Millisecond)
	}
	return run
}

// wireGoals maps the stored goals to the wire shape. check is rendered as the command that
// runs it, the same way the row's Validation already is for the primary Check field: the
// wire never carries the unrendered LeaseCheck, so a client needs no second parser for it.
func wireGoals(gates []types.Goal) []*jobv1.CompletionGate {
	if len(gates) == 0 {
		return nil
	}
	out := make([]*jobv1.CompletionGate, 0, len(gates))
	for _, g := range gates {
		wire := &jobv1.CompletionGate{
			Id:          g.ID,
			Description: g.Description,
			Kind:        string(g.Kind),
			Expect:      string(g.Expect),
			Paths:       g.Paths,
			Symbols:     g.Symbols,
			DependsOn:   g.DependsOn,
		}
		if g.Kind == types.GoalKindCheck && g.Check.Target != "" {
			wire.Check = g.Check.String()
		}
		out = append(out, wire)
	}
	return out
}

// wireJobResult maps the stored result to its console-facing wire projection. nil until a
// holder has filed one, which is a fact the drawer needs to tell apart from a result that filed
// nothing.
func wireJobResult(r *types.JobResult) *jobv1.JobResult {
	if r == nil {
		return nil
	}
	w := &jobv1.JobResult{
		ChangedPaths:        r.ChangedPaths,
		UnresolvedRisks:     r.UnresolvedRisks,
		Descendants:         r.Descendants,
		ValidationCommand:   r.Validation.Command,
		ValidationOutputRef: r.Validation.OutputRef,
	}
	for _, g := range r.GateEvidence {
		w.GateEvidence = append(w.GateEvidence, &jobv1.JobGateEvidence{GateId: g.GateID, OutputRef: g.OutputRef})
	}
	return w
}

// targetSize is the current magnitude of what a job maintains. Not every job shrinks a resource
// (sync-graph reconciles rather than trims), so an unmapped job reports a zero size.
func (s *Service) targetSize(j jobstore.CatalogEntry) *jobv1.ResourceSize {
	switch j.Name {
	case jobstore.NameRotateActivities:
		bytes, count := trail.Stat(s.ws.CacheDir())
		return &jobv1.ResourceSize{SizeBytes: bytes, ItemCount: count}
	case jobstore.NameRotateLogs:
		bytes, count := cache.NewOutputStore(s.ws.CacheDir()).RunsStat()
		return &jobv1.ResourceSize{SizeBytes: bytes, ItemCount: count}
	case jobstore.NameClearCache:
		return &jobv1.ResourceSize{SizeBytes: s.ws.CacheDiskBytes()}
	default:
		return &jobv1.ResourceSize{}
	}
}

// runningByArgv snapshots the server's live calls into a map from worker-argv key to invocation
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

// consoleURL is where a caller watches the job it just submitted: the console's Runs
// app, scoped to this invocation.
//
// Empty when there is no invocation, which is the one case the proto's "empty when no
// console is mounted" covers: a submit the server could not name cannot be linked to. It
// is a PATH rather than an absolute URL because the reader is the console, served from the
// server it just called, so it resolves this against its own origin; see
// console.AppLink.
func consoleURL(inv string) string {
	if inv == "" {
		return ""
	}
	return console.AppLink("runs", console.FragmentParam{Key: "inv", Value: inv})
}
