// Package report writes per-task JSONL events for post-processing.
// Each line has stable "schema" and "type" fields; bump Schema when a field change breaks parsers.
// Use [RunOptions] to wire into cache.RunAll and [GraphObserver] for graph events.
// Writes are async (drop+count under load); use [WithBlockOnFull] for lossless capture.
package report

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// Schema is the on-disk schema version; bump on field renames or removals.
// v3 unified the per-target cache.hit/cache.miss/cache.error events into a single
// target-result event (see TargetResult).
// v4 prefixed that event and the diagnostic one with "run.": both collided by name
// with types.StreamEvent, which stamps its own schema number on a line of nearly the
// same shape.
// v5 dropped run.base's vcs field and the lock.wait and lock.released events, and a
// -o jsonl run's stderr moved from slog's {time,level,msg} lines to run.notice records.
const Schema = 5

// Type values stamped on every event line; stable across versions.
const (
	TypeTargetResult          = "run.target.result"
	TypeGraphBuild            = "graph.build"
	TypeGraphQuery            = "graph.query"
	TypeGraphError            = "graph.error"
	TypeVolatility            = "volatile"
	TypeShardTotal            = "shard.total"
	TypeRaceDetected          = "race.detected"
	TypeOutputOverlapDetected = "race.output_overlap"
	TypeDeterminismMismatch   = "race.determinism_mismatch"
	TypeDeterminismUnchecked  = "race.determinism_unchecked"
	TypeMissingDependency     = "race.missing_dependency"
	TypeDiagnosticEmitted     = "run.diagnostic"
	TypeRunScope              = "run.scope"
	TypeRunCharms             = "run.charms"
	TypeRunCache              = "run.cache"
	TypeRunBase               = "run.base"
	TypeRunStep               = "run.step"
	TypeRunSummary            = "run.summary"
	TypeRunRemote             = "run.remote"
	TypeRunDry                = "run.dry"
	TypeRunDetach             = "run.detach"
	TypeLockSuperseded        = "lock.superseded"
	TypeLockSupersedeRefused  = "lock.supersede_refused"
	TypeLockPipeWait          = "lock.pipe_wait"
	TypeNotice                = "run.notice"
)

// TargetResult reports the outcome of one target run — the single per-target event
// (replacing the former cache.hit / cache.miss / cache.error). Status is "ok" (ran
// or replayed successfully) or "failed"; CacheHit distinguishes a cache replay from
// a fresh run. It is emitted once per target by the dispatcher (cache OnResult), so
// it fires for cached targets too.
type TargetResult struct {
	Project    string `json:"project"`
	Target     string `json:"target"`
	Status     string `json:"status"`
	CacheHit   bool   `json:"cache_hit"`
	Hash       string `json:"hash,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	Error      string `json:"error,omitempty"`
	Ref        string `json:"ref,omitempty"`     // per-execution output reference id, so a consumer can fetch this target's captured output by ref
	HintID     string `json:"hint_id,omitempty"` // stable id of the advisory line this target printed, so a hint's uptake is countable without matching its wording
	// Next carries the breadcrumbs for a FAILED result: the ref that holds the whole
	// captured output, and the target's own graph node. Absent on a pass, and absent
	// rather than empty when a failure minted neither, so a consumer counting uptake
	// can tell "nothing to suggest" from "suggested and ignored".
	//
	// The text a failing run prints is unchanged; this is the same suggestion as a
	// field, because the printed line was measured converting at the rate of no hint
	// at all.
	Next []hint.Next `json:"next,omitempty"`
}

// GraphBuild is one graph construction event, emitted once per Build.
type GraphBuild struct {
	Nodes      int   `json:"nodes"`
	DurationMs int64 `json:"duration_ms"`
}

// GraphQuery is one graph query observation -- affected detection,
// closure walks, etc.
type GraphQuery struct {
	Op          string `json:"op"`
	Nodes       int    `json:"nodes"`
	Seeds       int    `json:"seeds,omitempty"`
	Strategy    string `json:"strategy,omitempty"`
	ResultCount int    `json:"result_count,omitempty"`
	DurationMs  int64  `json:"duration_ms"`
}

// GraphError is one graph error event. The error message is logged via
// slog separately; the line only records that one occurred.
type GraphError struct {
	Op      string `json:"op,omitempty"`
	Message string `json:"error"`
}

// VolatilityCall records a volatility outcome; emitted when a retry was triggered or regression suspected.
type VolatilityCall struct {
	Project         string  `json:"project"`
	Target          string  `json:"target"`
	Status          string  `json:"status"` // "retried_volatile" | "retry_failed" | "suspected_regression"
	Attempts        int     `json:"attempts"`
	RetryReason     string  `json:"retry_reason,omitempty"` // "bootstrap" | "unaffected_failure" | "predicted_volatile"
	VolatilityScore float64 `json:"volatility_score,omitempty"`
}

// ShardTotal is one observation of total per-shard wall clock (job start → last project end); fits α.
type ShardTotal struct {
	Shard      string `json:"shard"`
	NShards    int    `json:"n_shards"`
	DurationMs int64  `json:"duration_ms"`
}

// RaceDetected records one filesystem race (--race=watch); both projects are confirmed writers of Path.
type RaceDetected struct {
	Path         string `json:"path"`
	ProjectA     string `json:"project_a"`
	ProjectB     string `json:"project_b"`
	Target       string `json:"target"`
	OverlapStart int64  `json:"overlap_start_ns"`
	OverlapEnd   int64  `json:"overlap_end_ns"`
}

// OutputOverlapDetected records a declared-output overlap between two projects in the same run.
type OutputOverlapDetected struct {
	ProjectA    string   `json:"project_a"`
	ProjectB    string   `json:"project_b"`
	Target      string   `json:"target"`
	Overlapping []string `json:"overlapping"`
}

// DeterminismMismatch records a project whose outputs differed between two consecutive runs (--race=replay).
type DeterminismMismatch struct {
	Project        string   `json:"project"`
	Target         string   `json:"target"`
	DifferingPaths []string `json:"differing_paths"`
}

// DeterminismUnchecked records a project whose byte-stability --race=replay could not
// check: Globs is set when its declared outputs matched nothing, Error when they could
// not be hashed. It fails the gate, like a mismatch does.
type DeterminismUnchecked struct {
	Project string `json:"project"`
	Target  string `json:"target"`
	Globs   string `json:"globs,omitempty"`
	Error   string `json:"error,omitempty"`
}

// MissingDependency records a likely missing graph edge: Consumer sources Path but didn't run; Producer wrote it.
type MissingDependency struct {
	Consumer string `json:"consumer"`
	Producer string `json:"producer"`
	Path     string `json:"path"`
	Target   string `json:"target"`
}

// DiagnosticEmitted reports one diagnostic (MGS code) fired during a run, captured
// through the shared diagnostic sink: the same events that enrich the knowledge
// graph's runtime shard, so the report stream and the graph read one capture.
type DiagnosticEmitted struct {
	Unit    string `json:"unit"`              // "<project>:<target>" or a project path
	Code    string `json:"code"`              // MGS####
	Message string `json:"message,omitempty"` // human message
}

// RunScope reports the run's project selection -- the "projects: ..." header a text
// run prints once at the start. Separate from RunCharms/RunCache/RunBase because the
// three headers are independent slog calls (some callers, e.g. the interactive picker,
// emit RunScope alone), so a single combined event would have to buffer for headers
// that may never arrive.
type RunScope struct {
	Label  string `json:"label"`
	Source string `json:"source,omitempty"`
}

// RunCharms reports the charms mixed into a run (e.g. magus.yaml default_charms),
// the "charms: ..." header.
type RunCharms struct {
	Charms string `json:"charms"`
}

// RunCache reports which cache tiers a run can reach and whether it may write to
// them, the "cache: ..." header.
type RunCache struct {
	Tier string `json:"tier"`
	Mode string `json:"mode"`
}

// RunBase reports what an affected run's change set was compared against, the
// "base: ..." header. Base already names the VCS ("git diff vs origin/main").
type RunBase struct {
	Base string `json:"base"`
}

// RunStep reports one sub-target progress line ("[pass] name (695ms)") as it
// completes -- a magus.needs stage, or a dry-run target that never actually ran.
// Status is "pass", "fail", "advisory", or "dry". Project is the workspace-relative
// path, set on a dry step so its repro command can name it.
type RunStep struct {
	Label      string `json:"label"`
	Project    string `json:"project,omitempty"`
	Target     string `json:"target,omitempty"`
	Status     string `json:"status"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	Error      string `json:"error,omitempty"`
}

// RunRemote accounts for what the remote cache did this run, once, beside the summary.
// It is emitted whenever a remote is configured, so all-zero counts mean the run never
// reached it rather than that nothing was reported.
type RunRemote = cache.RemoteTally

// RunDry opens a dry run: what follows are the steps it would take, none executed. The
// run.summary with dry set closes it.
type RunDry struct{}

// RunDetach is where an invocation handed to the daemon with --detach stands. State is
// "coalesced" (an identical one was already running, so none was queued), "queued"
// (handed over, not waited on), "running" (handed over and waited on), "unwatched"
// (the wait stopped; the run continues), "passed" or "failed".
type RunDetach struct {
	Invocation string `json:"invocation,omitempty"`
	State      string `json:"state"`
	DurationMs int64  `json:"duration_ms,omitempty"` // passed and failed only
}

// RunSummary is the end-of-run footer: hit/miss/error counts (or, for a dry run,
// the planned count) and elapsed wall time.
type RunSummary struct {
	Dry        bool  `json:"dry,omitempty"`
	Planned    int   `json:"planned,omitempty"`
	Hits       int   `json:"hits,omitempty"`
	Misses     int   `json:"misses,omitempty"`
	Errors     int   `json:"errors,omitempty"`
	DurationMs int64 `json:"duration_ms"`
}

// LockSuperseded reports that this gate stopped an earlier gate on the same tree and
// took its project lock (MGS3014).
type LockSuperseded struct {
	Project   string `json:"project"`
	HolderPID int    `json:"holder_pid,omitempty"`
	Command   string `json:"command,omitempty"`
}

// LockSupersedeRefused reports that this gate asked an earlier gate on the same tree to
// stop, it did not within BoundMs, and so this run is refused (exit 75) rather than
// waiting for it. Nothing was superseded.
type LockSupersedeRefused struct {
	Project   string `json:"project"`
	HolderPID int    `json:"holder_pid,omitempty"`
	Command   string `json:"command,omitempty"`
	BoundMs   int64  `json:"bound_ms"`
}

// LockPipeWait reports that this run is waiting, before taking any project lock, for
// the magus upstream of it in a shell pipe to release or settle the projects it needs.
type LockPipeWait struct {
	UpstreamPID int    `json:"upstream_pid"`
	Command     string `json:"command,omitempty"`
}

// Notice is a free-form advisory line -- a hint, warning, or one-time banner --
// that has no dedicated event type of its own. Code is the diagnostic code (e.g.
// an MGS####) when the notice carries one, and Message does not repeat it. Attrs
// carries the fields of a log record no typed event converts (see [NewNoticeHandler]).
//
// Level is written as "debug", "info", "warn" or "error" (see [LevelName]).
type Notice struct {
	Level   slog.Level
	Code    string
	Message string
	Attrs   map[string]any
}

// noticeWire is Notice as a line carries it.
type noticeWire struct {
	Level   string         `json:"level"`
	Code    string         `json:"code,omitempty"`
	Message string         `json:"msg"`
	Attrs   map[string]any `json:"attrs,omitempty"`
}

// MarshalJSON writes Level by name.
func (n Notice) MarshalJSON() ([]byte, error) {
	return json.Marshal(noticeWire{Level: LevelName(n.Level), Code: n.Code, Message: n.Message, Attrs: n.Attrs})
}

// UnmarshalJSON reads a line [Notice.MarshalJSON] wrote.
func (n *Notice) UnmarshalJSON(b []byte) error {
	var w noticeWire
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	var l slog.Level
	if err := l.UnmarshalText([]byte(w.Level)); err != nil {
		return fmt.Errorf("report: notice level: %w", err)
	}
	*n = Notice{Level: l, Code: w.Code, Message: w.Message, Attrs: w.Attrs}
	return nil
}

// LevelName is the name a record carries for l: one of debug, info, warn and error, with
// anything below debug (magus's trace) reading as debug and anything past error as error.
func LevelName(l slog.Level) string {
	switch {
	case l < slog.LevelInfo:
		return "debug"
	case l < slog.LevelWarn:
		return "info"
	case l < slog.LevelError:
		return "warn"
	}
	return "error"
}

var registry = map[reflect.Type]string{ // populated at init; read-only in the hot path
	reflect.TypeOf(DiagnosticEmitted{}):     TypeDiagnosticEmitted,
	reflect.TypeOf(TargetResult{}):          TypeTargetResult,
	reflect.TypeOf(GraphBuild{}):            TypeGraphBuild,
	reflect.TypeOf(GraphQuery{}):            TypeGraphQuery,
	reflect.TypeOf(GraphError{}):            TypeGraphError,
	reflect.TypeOf(VolatilityCall{}):        TypeVolatility,
	reflect.TypeOf(ShardTotal{}):            TypeShardTotal,
	reflect.TypeOf(RaceDetected{}):          TypeRaceDetected,
	reflect.TypeOf(OutputOverlapDetected{}): TypeOutputOverlapDetected,
	reflect.TypeOf(DeterminismMismatch{}):   TypeDeterminismMismatch,
	reflect.TypeOf(DeterminismUnchecked{}):  TypeDeterminismUnchecked,
	reflect.TypeOf(MissingDependency{}):     TypeMissingDependency,
	reflect.TypeOf(RunScope{}):              TypeRunScope,
	reflect.TypeOf(RunCharms{}):             TypeRunCharms,
	reflect.TypeOf(RunCache{}):              TypeRunCache,
	reflect.TypeOf(RunBase{}):               TypeRunBase,
	reflect.TypeOf(RunStep{}):               TypeRunStep,
	reflect.TypeOf(RunSummary{}):            TypeRunSummary,
	reflect.TypeOf(RunDry{}):                TypeRunDry,
	reflect.TypeOf(RunDetach{}):             TypeRunDetach,
	reflect.TypeOf(LockSuperseded{}):        TypeLockSuperseded,
	reflect.TypeOf(LockSupersedeRefused{}):  TypeLockSupersedeRefused,
	reflect.TypeOf(LockPipeWait{}):          TypeLockPipeWait,
	reflect.TypeOf(RunRemote{}):             TypeRunRemote,
	reflect.TypeOf(Notice{}):                TypeNotice,
}

// TypeOf returns the record type e is written as, or "" for an unregistered event.
func TypeOf(e any) string { return registry[reflect.TypeOf(e)] }

// RegisteredTypes returns every event type [Record] accepts, in no fixed order.
func RegisteredTypes() []reflect.Type {
	out := make([]reflect.Type, 0, len(registry))
	for t := range registry {
		out = append(out, t)
	}
	return out
}

// Record appends one event to w; no-op when w is nil. Unknown event types return an error.
// Under default (non-blocking) policy a full queue drops the event; use [WithBlockOnFull] for lossless capture.
func Record(w *Writer, e any) error {
	if w == nil {
		return nil
	}
	return w.record(e)
}

type writerKey struct{}

// WithWriter returns a copy of ctx carrying w. Retrieve with [WriterFromContext].
func WithWriter(ctx context.Context, w *Writer) context.Context {
	return context.WithValue(ctx, writerKey{}, w)
}

// WriterFromContext returns the Writer stored by [WithWriter], or nil.
func WriterFromContext(ctx context.Context) *Writer {
	w, _ := ctx.Value(writerKey{}).(*Writer)
	return w
}

// NextServer grades a failing target's breadcrumbs for whoever is reading the report
// and records what it handed over. [ServedIn] builds the one production caller;
// a nil server serves everything and records nothing.
//
// A parameter rather than a default, because a report written for a reader magus
// cannot identify is not the same thing as one written for a bound worker, and the
// serving side is where that has to be decided.
type NextServer func(next []hint.Next) []hint.Next

// ServedIn serves a failing run's breadcrumbs the way every other door serves them:
// filtered for the acting lease's role and journaled in this checkout's cache dir.
//
// The failure family is the one this whole mechanism was measured against, so leaving
// it unjournaled would give the most-served breadcrumb in the tree a denominator of
// zero in `magus session hints`, and leave it the one suggestion the guard cannot
// pre-authorize.
func ServedIn(cacheDir, root string) NextServer {
	return func(next []hint.Next) []hint.Next {
		role, writePaths := hint.RoleUnbound, []string(nil)
		// A binding that cannot be read serves the worker's narrower set.
		id, _, err := job.ActingLease(cacheDir, trail.LeaseFromEnv())
		if err != nil {
			role = hint.RoleWorker
		}
		if id != "" {
			role = hint.RoleWorker
			if rows, err := job.NewStore(job.Location{CacheDir: cacheDir, Root: root}).List(); err == nil {
				role, writePaths = hint.LeaseRole(rows, id)
			}
		}
		served := hint.ServableTo(role, writePaths, next)
		hint.AppendServedNext(cacheDir, served)
		return served
	}
}

// RunOptions returns a cache.RunOption that records hit/miss/error events into w per spec.
func RunOptions(w *Writer, served NextServer) []cache.RunOption {
	return []cache.RunOption{
		cache.OnResult(func(s *cache.Step, r *cache.Result, err error) {
			tr := TargetResult{
				Project:    s.ProjectPath,
				Target:     s.Target,
				Status:     "ok",
				CacheHit:   r.Hit,
				Hash:       r.Hash,
				DurationMs: r.Duration.Milliseconds(),
				Ref:        r.Ref,
				HintID:     r.HintID,
			}
			if err != nil {
				tr.Status = "failed"
				tr.Error = err.Error()
				tr.Next = hint.NextForFailure(s.ProjectPath, s.Target, r.Ref)
				if served != nil {
					tr.Next = served(tr.Next)
				}
			}
			_ = Record(w, tr)
		}),
	}
}

// GraphObserver returns a types.Observer that appends graph events to w; nil-safe.
func GraphObserver(w *Writer) types.Observer {
	if w == nil {
		return types.NoopObserver{}
	}
	return &graphObserver{w: w}
}

type graphObserver struct{ w *Writer }

func (o *graphObserver) OnBuild(s types.BuildStats) {
	_ = Record(o.w, GraphBuild{
		Nodes:      s.Nodes,
		DurationMs: s.Duration.Milliseconds(),
	})
}

func (o *graphObserver) OnQuery(e types.QueryEvent) {
	_ = Record(o.w, GraphQuery{
		Op:          e.Op,
		Nodes:       e.Nodes,
		Seeds:       e.Seeds,
		Strategy:    e.Strategy,
		ResultCount: e.ResultCount,
		DurationMs:  e.Duration.Milliseconds(),
	})
}

func (o *graphObserver) OnError(err error) {
	_ = Record(o.w, GraphError{Message: err.Error()})
}
