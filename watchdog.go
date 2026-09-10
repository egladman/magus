package magus

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/types"
)

// defaultStallTimeout is how long an invocation may hold its project locks with nothing
// moving before the watchdog aborts it (MGS3012).
//
// Chosen against the longest legitimately SILENT stretch a run produces, not against its
// longest target: every line of subprocess output beats the heartbeat, so a slow target
// that is still talking never approaches the window. What has to fit under it is quiet
// work, and the quietest things magus waits on are a cold module download, a link step,
// and a container image pull, each single-digit minutes. Fifteen clears that several
// times over and still catches the shape this exists for: the gate that wedged for 65
// minutes on 2026-09-04 would have failed inside the first quarter of it.
const defaultStallTimeout = 15 * time.Minute

// abortWatch is an armed supervisor over one invocation: a poller that cancels the run
// and names why. One run arms two of them, for the two conditions it cannot diagnose from
// inside itself: it stopped making progress (MGS3012), and a later gate wants the locks
// it holds (MGS3014).
type abortWatch struct {
	stop    func()
	done    chan struct{}
	tripped atomic.Pointer[types.DiagnosticError]
	// exit is the process status a tripped verdict carries. A stall is a failure and
	// exits like one; a supersede is not, and says so with 75.
	exit int
}

// stallExit is the process status a stalled run carries: a plain failure.
const stallExit = 1

// verdict replaces err with the watchdog's diagnostic when it fired. It takes
// precedence over whatever the cancellation itself surfaced, because that is always
// context.Canceled or a target reporting its subprocess killed, and neither says what
// happened.
func (w *abortWatch) verdict(err error) error {
	if e := w.tripped.Load(); e != nil {
		return types.ExitError{Err: e, Code: w.exit}
	}
	return err
}

// close stops the poller and waits for it to exit.
func (w *abortWatch) close() {
	w.stop()
	<-w.done
}

// watchForStall arms the stall watchdog over ctx and returns the context the run should
// use: once prog has been quiet for the configured window, the watchdog cancels it with
// MGS3012 as the cause and calls release.
//
// release is what makes the abort true rather than hopeful. Cancelling only asks the run
// to unwind, and the locks come back when executeStages returns, so a stall that does not
// answer its context holds every project lock forever while the terminal says the run was
// aborted. watchWorkspaceRoot answers the same problem the same way, and the release it
// shares is already idempotent for exactly this second caller (see acquireProjectLocks).
//
// It runs entirely in this process. The daemon is an accelerant and never a capability
// gate, and an invocation stalling with no daemon up is exactly the case where nobody
// else is watching, so the net cannot depend on one.
//
// Distinct from a target ceiling (config.TargetTimeout, a target's own declared timeout,
// MGS3011): a ceiling bounds a target that runs LONG, and only one whose author declared
// a bound. This catches an invocation making NO progress at all, in work no target
// declared. The case it was built for is the post-batch settle pass, which holds every
// project lock while belonging to no target.
func (m *Magus) watchForStall(ctx context.Context, prog *cache.Progress, release func()) (context.Context, *abortWatch) {
	window := m.cfg.StallTimeout
	if window == 0 {
		window = defaultStallTimeout
	}
	if release == nil {
		release = func() {}
	}
	w := &abortWatch{done: make(chan struct{}), exit: stallExit}
	if window < 0 {
		w.stop = func() {}
		close(w.done)
		return ctx, w
	}

	ctx, cancel := context.WithCancelCause(ctx)
	pollCtx, stopPoll := context.WithCancel(ctx)
	w.stop = func() {
		stopPoll()
		cancel(context.Canceled)
	}
	// Captured, not closed over: executeStages reassigns ctx many times after this
	// returns, and the poller logs for the whole run.
	logCtx := ctx
	go func() {
		defer close(w.done)
		tick := time.NewTicker(stallPollInterval(window))
		defer tick.Stop()
		for {
			select {
			case <-pollCtx.Done():
				return
			case <-tick.C:
				// A pending tick and a stop can be ready together, and select picks
				// among ready cases at random. Re-check, or a run that already
				// returned is reported stalled and has its successor's locks freed.
				select {
				case <-pollCtx.Done():
					return
				default:
				}
				idle := prog.Idle()
				if idle < window {
					continue
				}
				err := stallDiagnostic(prog.Last(), idle, window, m.cache.RunningTargets())
				w.tripped.Store(err)
				// Logged as well as returned: the abort unwinds through cancellation, and
				// a reader watching the terminal should see WHY the run stopped at the
				// moment it stops, not only in the final error.
				slog.ErrorContext(logCtx, err.Error())
				cancel(err)
				// After the cancel, so the run is already unwinding when its
				// exclusivity goes: a peer that takes a lock here finds a run on its
				// way out rather than one still working.
				release()
				return
			}
		}
	}()
	return ctx, w
}

// supersedePollInterval is how often a gate re-reads its own lock paths for a yield
// request. One stat per held project per second, against a decision whose whole value is
// that the later gate starts in seconds rather than after the earlier one finishes.
//
// A var, not a const, so a test can shorten it.
var supersedePollInterval = time.Second

// supersedeExit is the process status a superseded gate carries.
//
// 75 (EX_TEMPFAIL) like the contended lock and MGS3010, and for the same reason: nothing
// here failed, and the same command is valid the moment the later gate is done. 1 would
// leave a caller unable to tell a gate that yielded from a gate that found a bug.
const supersedeExit = 75

// watchForSupersede aborts the run when a LATER gate on this same tree asks for the
// project locks this one holds, and returns the context the run should use.
//
// Inert for anything that is not a gate: only a gate supersedes and only a gate is
// superseded, so a `run build` and a `run test` queue on each other exactly as before.
//
// The abort is a cancellation and nothing more: the locks come back when the run has
// unwound and its deferred release runs, never before, because cancellation is not
// completion and a successor taking the locks while this run's subprocesses are still
// being torn down would have two gates mutating one tree. The successor's bounded wait
// covers the unwind (see takeBySuperseding), and past the bound it queues like any run.
//
// It runs for the whole invocation, which is what covers the case a batch-shaped check
// would miss: a run that has finished its targets and is in the settle tail is running as
// far as the locks are concerned, and is superseded like any other gate.
func (m *Magus) watchForSupersede(ctx context.Context, hold *projectHold) (context.Context, *abortWatch) {
	w := &abortWatch{done: make(chan struct{}), exit: supersedeExit}
	if hold == nil || hold.locker == nil || !hold.locker.gate || len(hold.paths) == 0 {
		w.stop = func() {}
		close(w.done)
		return ctx, w
	}
	ctx, cancel := context.WithCancelCause(ctx)
	pollCtx, stopPoll := context.WithCancel(ctx)
	w.stop = func() {
		stopPoll()
		cancel(context.Canceled)
	}
	// Captured, not closed over: executeStages reassigns ctx many times after this
	// returns, and the poller logs for the whole run.
	logCtx := ctx
	go func() {
		defer close(w.done)
		tick := time.NewTicker(supersedePollInterval)
		defer tick.Stop()
		for {
			select {
			case <-pollCtx.Done():
				return
			case <-tick.C:
				// A pending tick and a stop can be ready together, and select picks
				// among ready cases at random. Re-check, or a run that already
				// returned reports itself superseded and frees its successor's locks.
				select {
				case <-pollCtx.Done():
					return
				default:
				}
				project, by, ok := hold.yieldRequested()
				if !ok {
					continue
				}
				err := supersededDiagnostic(project, by)
				w.tripped.Store(err)
				// Logged as well as returned, so a reader watching the terminal learns why
				// the run stopped at the moment it stops rather than only at the end.
				slog.WarnContext(logCtx, err.Error())
				cancel(err)
				return
			}
		}
	}()
	return ctx, w
}

// supersededDiagnostic renders MGS3014: who took over, when they started and what they
// ran, so a reader can tell this from a failure without opening anything.
func supersededDiagnostic(projectPath string, by processRecord) *types.DiagnosticError {
	p := projectPath
	if p == "" {
		p = "."
	}
	return types.DiagnosticErrorf(types.GateSuperseded,
		"this gate was superseded by a later gate on the same tree (pid %d, started %s, %s);"+
			" its verdict would have described a tree that has since changed; nothing here was wrong.\n"+
			"  yielded: project %s, along with every other project this run had locked\n"+
			"  the later gate is running now, so there is nothing to rerun here",
		by.PID, by.Started.UTC().Format(time.RFC3339), by.Command, p)
}

// stallPollInterval samples often enough that the reported quiet time is close to the
// window, without waking a goroutine every second through a long build.
func stallPollInterval(window time.Duration) time.Duration {
	d := window / 4
	if d > time.Minute {
		d = time.Minute
	}
	if d < 10*time.Millisecond {
		d = 10 * time.Millisecond
	}
	return d
}

// stallDiagnostic renders MGS3012: what ran last, how long ago, what else was still
// admitted alongside it, and where its output went, so a reader has somewhere to look
// without reproducing the stall.
//
// running is the whole answer to "waiting on what?" that this process can give. A step
// blocked on a dependency is blocked on something that is either still in flight (named
// here) or already finished (absent here, which says the wait itself is the bug). The
// 2026-09-10 stall reported only "generate (executing)", and the list would have said
// whether the codegen chain beneath it was moving.
func stallDiagnostic(last cache.Mark, idle, window time.Duration, running []string) *types.DiagnosticError {
	what := "none; no target had started"
	if last.Target != "" {
		what = fmt.Sprintf("%s:%s (%s)", last.Project, last.Target, last.What)
	}
	inFlight := "nothing; every admitted step had already handed its seat back"
	if len(running) > 0 {
		inFlight = strings.Join(running, ", ")
	}
	msg := fmt.Sprintf("aborting a stalled run: nothing has started, finished or printed a line for %s, "+
		"and every selected project's lock is still held.\n  last step: %s\n  still admitted: %s\n  stall window: %s",
		idle.Round(time.Second), what, inFlight, window)
	if last.Log != "" {
		msg += "\n  captured log: " + last.Log
	}
	return types.DiagnosticErrorf(types.InvocationStalled,
		"%s\n  override: set stall_timeout (MAGUS_STALL_TIMEOUT, --stall-timeout) higher, or negative to turn the watchdog off", msg)
}
