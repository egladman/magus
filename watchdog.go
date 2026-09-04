package magus

import (
	"context"
	"fmt"
	"log/slog"
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

// stallWatch is an armed watchdog over one invocation's heartbeat.
type stallWatch struct {
	stop    func()
	done    chan struct{}
	tripped atomic.Pointer[types.DiagnosticError]
}

// verdict replaces err with the stall diagnostic when the watchdog fired. It takes
// precedence over whatever the cancellation itself surfaced, because that is always
// context.Canceled or a target reporting its subprocess killed, and neither says what
// happened.
func (w *stallWatch) verdict(err error) error {
	if e := w.tripped.Load(); e != nil {
		return e
	}
	return err
}

// close stops the poller and waits for it to exit.
func (w *stallWatch) close() {
	w.stop()
	<-w.done
}

// watchForStall arms the stall watchdog over ctx and returns the context the run should
// use: once prog has been quiet for the configured window, the watchdog cancels it with
// MGS3012 as the cause.
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
func (m *Magus) watchForStall(ctx context.Context, prog *cache.Progress) (context.Context, *stallWatch) {
	window := m.cfg.StallTimeout
	if window == 0 {
		window = defaultStallTimeout
	}
	w := &stallWatch{done: make(chan struct{})}
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
				idle := prog.Idle()
				if idle < window {
					continue
				}
				err := stallDiagnostic(prog.Last(), idle, window)
				w.tripped.Store(err)
				// Logged as well as returned: the abort unwinds through cancellation, and
				// a reader watching the terminal should see WHY the run stopped at the
				// moment it stops, not only in the final error.
				slog.ErrorContext(logCtx, err.Error())
				cancel(err)
				return
			}
		}
	}()
	return ctx, w
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

// stallDiagnostic renders MGS3012: what ran last, how long ago, and where its output
// went, so a reader has somewhere to look without reproducing the stall.
func stallDiagnostic(last cache.Mark, idle, window time.Duration) *types.DiagnosticError {
	what := "none; no target had started"
	if last.Target != "" {
		what = fmt.Sprintf("%s:%s (%s)", last.Project, last.Target, last.What)
	}
	msg := fmt.Sprintf("aborting a stalled run: nothing has started, finished or printed a line for %s, "+
		"and every selected project's lock is still held.\n  last step: %s\n  stall window: %s",
		idle.Round(time.Second), what, window)
	if last.Log != "" {
		msg += "\n  captured log: " + last.Log
	}
	return types.DiagnosticErrorf(types.InvocationStalled,
		"%s\n  override: set stall_timeout (MAGUS_STALL_TIMEOUT, --stall-timeout) higher, or negative to turn the watchdog off", msg)
}
