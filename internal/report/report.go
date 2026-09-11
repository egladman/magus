// Package report writes per-task JSONL events for post-processing.
// Each line has stable "schema" and "type" fields; bump Schema when a field change breaks parsers.
// Use [RunOptions] to wire into cache.RunAll and [GraphObserver] for graph events.
// Writes are async (drop+count under load); use [WithBlockOnFull] for lossless capture.
package report

import (
	"context"
	"reflect"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
)

// Schema is the on-disk schema version; bump on field renames or removals.
// v3 unified the per-target cache.hit/cache.miss/cache.error events into a single
// target-result event (see TargetResult).
// v4 prefixed that event and the diagnostic one with "run.": both collided by name
// with types.StreamEvent, which stamps its own schema number on a line of nearly the
// same shape.
const Schema = 4

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
	TypeMissingDependency     = "race.missing_dependency"
	TypeDiagnosticEmitted     = "run.diagnostic"
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
	reflect.TypeOf(MissingDependency{}):     TypeMissingDependency,
}

func typeOf(e any) string { return registry[reflect.TypeOf(e)] }

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
		role, lane := hint.RoleUnbound, []string(nil)
		if id := job.ActingLease(cacheDir); id != "" {
			role = hint.RoleWorker
			if rows, err := job.NewStore(job.Location{CacheDir: cacheDir, Root: root}).List(); err == nil {
				role, lane = hint.RoleFor(rows, id)
			}
		}
		served := hint.ServableTo(role, lane, next)
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
