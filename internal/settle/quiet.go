package settle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/journal"
)

// RunFunc runs one `<target>:rw <projects...>` invocation, the way Hook's run does.
type RunFunc func(ctx context.Context, inv []string) error

// Quietly wraps run so a hook's regeneration prints nothing of its own: every step's
// log, a generator's chatter and the run's pass blocks only ever repeated what the
// hook's one line says. observe threads a handler onto ctx that the run's invocation
// log feeds, and the first failed step it sees becomes the error, naming that step's
// cause and output ref. A run that failed with no failed step on record (the workspace
// did not resolve the target, say) gets what it printed back on stderr, since nothing
// else would say why.
func Quietly(run RunFunc, observe func(context.Context, slog.Handler) context.Context) RunFunc {
	return func(ctx context.Context, inv []string) error {
		var steps failedSteps
		var err error
		captured, captureErr := silenced(func() error {
			err = run(observe(ctx, &steps), inv)
			return err
		})
		if err == nil {
			return captureErr
		}
		if failure, ok := steps.first(); ok {
			return failure
		}
		if Busy(err) {
			// Nothing ran, the error names the holder, and Waiting may retry: a dump of
			// the refused run's header lines per attempt would only repeat it.
			return err
		}
		_, _ = os.Stderr.Write(captured)
		return err
	}
}

// exitTempFail is EX_TEMPFAIL, the status magus gives a run it refused to start
// because another magus holds a project lock or the machine could not seat it.
const exitTempFail = 75

// Busy reports whether err is a run that never started for one of those reasons, so
// the same invocation succeeds once the holder finishes.
func Busy(err error) bool {
	var coded interface{ ExitCode() int }
	return errors.As(err, &coded) && coded.ExitCode() == exitTempFail
}

// Waiting wraps run so an invocation refused as Busy is retried every poll until bound
// has passed, with say told once what it waits on. A settle that failed at once would
// leave its regeneration owed over a lock that is often released seconds later. Once
// the bound passes the last refusal is returned, still Busy; a cancelled ctx returns
// ctx's error.
func Waiting(run RunFunc, bound, poll time.Duration, say func(ctx context.Context, line string)) RunFunc {
	return func(ctx context.Context, inv []string) error {
		deadline := time.Now().Add(bound)
		told := false
		for {
			err := run(ctx, inv)
			if !Busy(err) {
				return err
			}
			if !time.Now().Add(poll).Before(deadline) {
				return fmt.Errorf("waited %s: %w", bound, err)
			}
			if !told {
				say(ctx, fmt.Sprintf("%s; waiting up to %s for it", strings.TrimSuffix(err.Error(), ", not waiting"), bound))
				told = true
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(poll):
			}
		}
	}
}

// StepFailure is the first step of a quiet regeneration that failed.
type StepFailure struct {
	Project string
	Target  string
	// Cause is the first line of the step's error; the output ref holds the rest.
	Cause string
	Ref   string
}

func (f *StepFailure) Error() string {
	msg := fmt.Sprintf("%s failed in %s: %s", f.Target, f.Project, f.Cause)
	if f.Ref == "" {
		return msg
	}
	return fmt.Sprintf("%s, read it with `%s`", msg, hint.QueryOutput.With(f.Ref))
}

// failedSteps is a slog.Handler over a run's invocation log that keeps the first failed
// step result. Steps run concurrently, so Handle locks.
type failedSteps struct {
	mu     sync.Mutex
	failed *StepFailure
}

func (h *failedSteps) Enabled(context.Context, slog.Level) bool { return true }

func (h *failedSteps) Handle(_ context.Context, r slog.Record) error {
	e, ok := journal.EventFromRecord(r)
	if !ok || e.Kind != journal.KindResult || e.Status != journal.StatusFail {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failed == nil {
		cause, _, _ := strings.Cut(strings.TrimSpace(e.Text), "\n")
		h.failed = &StepFailure{Project: e.Project, Target: e.Target, Cause: cause, Ref: e.Ref}
	}
	return nil
}

func (h *failedSteps) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *failedSteps) WithGroup(string) slog.Handler { return h }

func (h *failedSteps) first() (*StepFailure, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.failed, h.failed != nil
}
