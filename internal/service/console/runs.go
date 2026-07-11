package console

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/types"
)

// defaultRunRetention is how long a finished run lingers in the registry after its last
// event, so a dashboard shows the terminal PASSED/FAILED/CACHED states briefly before the
// row disappears rather than blinking out the instant a run ends.
const defaultRunRetention = 10 * time.Second

// maxRunAge is the hard age ceiling for an unfinished run. A run that never emits a
// KindFinished event (a crashed dispatch, or one whose finished event was dropped) would
// otherwise linger forever, since retention only evicts finished runs. Once such a run has
// been silent longer than this bound it is evicted regardless.
const maxRunAge = 5 * time.Minute

// RunRegistry is the daemon's live-run tap: a slog.Handler folded into every adopted run's
// capture logger. It decodes the journal events a run emits (started/scope/exec/result/
// finished) and maintains, per invocation, the per-target execution state a dashboard
// renders - so the SAME status surface that reports the pool also reports what each run's
// targets are doing. Finished runs are pruned after a short retention window.
//
// It is daemon-held (one per daemon process, attached to each adopted dispatch), so it must
// not accumulate unbounded state: it keeps only in-flight and recently-finished runs, never
// a full event backlog. All methods are safe for concurrent use - events arrive on the run
// goroutines while Snapshot is read on the status/SSE handler goroutines.
type RunRegistry struct {
	mu     sync.Mutex
	runs   map[string]*runState // keyed by invocation id
	retain time.Duration
	nowFn  func() time.Time
}

type runState struct {
	inv         string
	trigger     string
	startedAt   time.Time
	finished    bool
	finishedAt  time.Time
	lastEventAt time.Time                         // wall-clock time of the most recent folded event
	order       []string                          // target keys, first-seen order (stable render)
	targets     map[string]*types.StatusTargetRun // keyed by targetKey(project, target)
}

// NewRunRegistry returns an empty registry using the default retention and wall clock.
func NewRunRegistry() *RunRegistry {
	return &RunRegistry{runs: make(map[string]*runState), retain: defaultRunRetention, nowFn: time.Now}
}

// Enabled always accepts records: capture never filters, and the fold is cheap.
func (r *RunRegistry) Enabled(context.Context, slog.Level) bool { return true }

// Handle folds one capture record into the run state. A record that carries no journal event
// (or no invocation id to attribute it to) is ignored. Capture must never slow or fail a run,
// so this never returns an error.
func (r *RunRegistry) Handle(_ context.Context, rec slog.Record) error {
	e, ok := journal.EventFromRecord(rec)
	if !ok || e.Inv == "" {
		return nil
	}
	r.fold(e)
	return nil
}

func (r *RunRegistry) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *RunRegistry) WithGroup(string) slog.Handler      { return r }

// fold advances the registry state for one event.
func (r *RunRegistry) fold(e journal.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()

	rs := r.runs[e.Inv]
	if rs == nil {
		rs = &runState{inv: e.Inv, targets: make(map[string]*types.StatusTargetRun)}
		r.runs[e.Inv] = rs
	}

	switch e.Kind {
	case journal.KindStarted:
		rs.startedAt = time.UnixMilli(e.Ts)
		if e.Command != nil {
			rs.trigger = e.Command.Trigger
		}
	case journal.KindScope:
		// A scope header that names a target seeds it as QUEUED before it runs. Scope
		// headers without a target carry no per-target state, so they are ignored.
		if e.Target != "" {
			r.targetLocked(rs, e).State = types.TargetRunQueued
		}
	case journal.KindExec:
		// First subprocess for a target flips it to RUNNING; later exec events (a target
		// may run several subprocesses) and events after a terminal result do not regress it.
		if e.Target == "" {
			return
		}
		t := r.targetLocked(rs, e)
		if t.State == "" || t.State == types.TargetRunQueued {
			t.State = types.TargetRunRunning
			t.StartedAt = time.UnixMilli(e.Ts)
		}
	case journal.KindResult:
		if e.Target == "" {
			return
		}
		t := r.targetLocked(rs, e)
		t.State = resultState(e.Status)
		t.EndedAt = time.UnixMilli(e.Ts)
		t.OutputRef = e.Ref
		t.DurationMs = e.DurMs
		if t.StartedAt.IsZero() {
			// A cache hit produces a result with no preceding exec; anchor its start so the
			// dashboard has a sensible start time.
			t.StartedAt = time.UnixMilli(e.Ts)
		}
	case journal.KindFinished:
		rs.finished = true
		rs.finishedAt = r.nowFn()
	}

	rs.lastEventAt = r.nowFn()
	r.pruneLocked(r.nowFn())
}

// targetLocked returns the mutable target state for an event's project/target, creating it
// (and recording first-seen order) on first reference. Caller holds r.mu.
func (r *RunRegistry) targetLocked(rs *runState, e journal.Event) *types.StatusTargetRun {
	key := targetKey(e.Project, e.Target)
	t := rs.targets[key]
	if t == nil {
		t = &types.StatusTargetRun{Project: e.Project, Target: e.Target}
		rs.targets[key] = t
		rs.order = append(rs.order, key)
	}
	return t
}

// Snapshot returns the current live runs, newest-started first, each with its targets in
// first-seen order. It prunes runs that finished longer than the retention window ago.
func (r *RunRegistry) Snapshot() []types.StatusRun {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked(r.nowFn())

	out := make([]types.StatusRun, 0, len(r.runs))
	for _, rs := range r.runs {
		run := types.StatusRun{Inv: rs.inv, Trigger: rs.trigger, StartedAt: rs.startedAt}
		for _, key := range rs.order {
			run.Targets = append(run.Targets, *rs.targets[key])
		}
		out = append(out, run)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].StartedAt.After(out[j].StartedAt)
		}
		return out[i].Inv < out[j].Inv
	})
	return out
}

// pruneLocked drops runs that finished more than the retention window before now, plus any
// unfinished run whose last event is older than maxRunAge (a run that never emitted a
// KindFinished must not linger forever). Caller holds r.mu.
func (r *RunRegistry) pruneLocked(now time.Time) {
	for inv, rs := range r.runs {
		switch {
		case rs.finished && now.Sub(rs.finishedAt) > r.retain:
			delete(r.runs, inv)
		case !rs.finished && now.Sub(rs.lastEventAt) > maxRunAge:
			delete(r.runs, inv)
		}
	}
}

// targetKey joins a project and target into a map key that cannot collide across projects.
func targetKey(project, target string) string { return project + "\x00" + target }

// resultState maps a journal result status onto a target run state. An unrecognized status
// (e.g. a future journal status this build does not know) maps to the zero/unspecified state
// rather than being silently shown as PASSED.
func resultState(status string) types.TargetRunState {
	switch status {
	case journal.StatusPass:
		return types.TargetRunPassed
	case journal.StatusCached:
		return types.TargetRunCached
	case journal.StatusFail:
		return types.TargetRunFailed
	default:
		return ""
	}
}

// runSinkKey carries the daemon's live-run handler on ctx so the adopted run/affected
// dispatch can fold it into BeginInvocation without a package-global read.
type runSinkKey struct{}

// WithRunSink threads the daemon's live-run capture handler onto ctx. A nil handler leaves
// ctx unchanged, so callers can pass an unconfigured registry unconditionally.
func WithRunSink(ctx context.Context, h slog.Handler) context.Context {
	if h == nil {
		return ctx
	}
	return context.WithValue(ctx, runSinkKey{}, h)
}

// RunSinkHandlers lifts the handler threaded by WithRunSink into the variadic capture-handler
// list BeginInvocation accepts (empty when none is set - the non-daemon path).
func RunSinkHandlers(ctx context.Context) []slog.Handler {
	if h, ok := ctx.Value(runSinkKey{}).(slog.Handler); ok && h != nil {
		return []slog.Handler{h}
	}
	return nil
}
