package console

import (
	"context"
	"fmt"
	"github.com/egladman/magus/types/daemon"
	"sort"
	"sync"
	"time"

	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/libs/safetyscissors/journal"
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

// RunRegistry is the server's live-run tap, a sink on every adopted run. It
// keeps each invocation's per-target state from the records a run emits, so
// the status service that reports the pool also reports each run's targets.
// It is one per server process, so it keeps only in-flight and recently
// finished runs. All methods are safe for concurrent use.
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
	lastEventAt time.Time                    // wall-clock time of the most recent folded event
	order       []string                     // target keys, first-seen order (stable render)
	targets     map[string]*daemon.TargetRun // keyed by targetKey(project, target)
}

// NewRunRegistry returns an empty registry using the default retention and wall clock.
func NewRunRegistry() *RunRegistry {
	return &RunRegistry{runs: make(map[string]*runState), retain: defaultRunRetention, nowFn: time.Now}
}

// Handle folds one record into the run state; it is a recorder.Sink. A record
// with no invocation id to attribute it to is ignored.
func (r *RunRegistry) Handle(_ context.Context, rec journal.Record) {
	if rec.Invocation == "" {
		return
	}
	r.fold(rec)
}

// fold advances the registry state for one record.
func (r *RunRegistry) fold(rec journal.Record) {
	r.mu.Lock()
	defer r.mu.Unlock()

	rs := r.runs[rec.Invocation]
	if rs == nil {
		rs = &runState{inv: rec.Invocation, targets: make(map[string]*daemon.TargetRun)}
		r.runs[rec.Invocation] = rs
	}

	if started, ok := rec.Started(); ok {
		rs.startedAt = rec.Time
		rs.trigger = string(started.Command.Trigger)
	}
	if exec, ok := rec.Exec(); ok {
		// First subprocess for a target flips it to RUNNING; later exec events (a target
		// may run several subprocesses) and events after a terminal result do not regress it.
		if exec.Target == "" {
			return
		}
		t := r.targetLocked(rs, exec.Project, exec.Target)
		if t.State == "" || t.State == daemon.TargetRunQueued {
			t.State = daemon.TargetRunRunning
			t.StartedAt = rec.Time
		}
	}
	if run, ok := rec.Run(); ok {
		if run.Target == "" {
			return
		}
		t := r.targetLocked(rs, run.Project, run.Target)
		t.State = resultState(run.Status)
		t.EndedAt = rec.Time
		t.OutputRef = run.Ref
		t.DurationMs = run.Duration.Milliseconds()
		if t.StartedAt.IsZero() {
			// A cache hit produces a result with no preceding exec; anchor its start so the
			// dashboard has a sensible start time.
			t.StartedAt = rec.Time
		}
	}
	if _, ok := rec.Finished(); ok {
		rs.finished = true
		rs.finishedAt = r.nowFn()
	}

	rs.lastEventAt = r.nowFn()
	r.pruneLocked(r.nowFn())
}

// targetLocked returns the mutable state for project/target, creating it (and
// recording first-seen order) on first reference. Caller holds r.mu.
func (r *RunRegistry) targetLocked(rs *runState, project, target string) *daemon.TargetRun {
	key := targetKey(project, target)
	t := rs.targets[key]
	if t == nil {
		t = &daemon.TargetRun{Project: project, Target: target}
		rs.targets[key] = t
		rs.order = append(rs.order, key)
	}
	return t
}

// Snapshot returns the current live runs, newest-started first, each with its targets in
// first-seen order. It prunes runs that finished longer than the retention window ago.
func (r *RunRegistry) Snapshot() []daemon.Run {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked(r.nowFn())

	out := make([]daemon.Run, 0, len(r.runs))
	for _, rs := range r.runs {
		run := daemon.Run{Inv: rs.inv, Trigger: rs.trigger, StartedAt: rs.startedAt}
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
func targetKey(project, target string) string { return fmt.Sprintf("%s\x00%s", project, target) }

// resultState maps a journal result status onto a target run state. An unrecognized status
// (e.g. a future journal status this build does not know) maps to the zero/unspecified state
// rather than being silently shown as PASSED.
func resultState(status journal.Status) daemon.TargetRunState {
	switch status {
	case journal.StatusPass:
		return daemon.TargetRunPassed
	case journal.StatusCached:
		return daemon.TargetRunCached
	case journal.StatusPermitted:
		return daemon.TargetRunPermitted
	case journal.StatusFail:
		return daemon.TargetRunFailed
	default:
		return ""
	}
}

// runSinkKey carries the server's live-run handler on ctx so the adopted run/affected
// dispatch can fold it into BeginInvocation without a package-global read.
type runSinkKey struct{}

// WithRunSink threads the server's live-run sink onto ctx. A nil sink leaves
// ctx unchanged, so callers can pass an unconfigured registry unconditionally.
func WithRunSink(ctx context.Context, sink recorder.Sink) context.Context {
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, runSinkKey{}, sink)
}

// RunSinkHandlers lifts the sink threaded by WithRunSink into the sinks
// BeginInvocation accepts, empty when none is set: the non-server path.
func RunSinkHandlers(ctx context.Context) []recorder.Sink {
	if sink, ok := ctx.Value(runSinkKey{}).(recorder.Sink); ok && sink != nil {
		return []recorder.Sink{sink}
	}
	return nil
}
