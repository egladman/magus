package settle

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/libs/safetyscissors/journal"
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
func Quietly(run RunFunc, observe func(context.Context, recorder.Sink) context.Context) RunFunc {
	return func(ctx context.Context, inv []string) error {
		var steps failedSteps
		var err error
		captured, captureErr := silenced(func() error {
			err = run(observe(ctx, steps.Handle), inv)
			return err
		})
		if err == nil {
			return captureErr
		}
		if failure, ok := steps.first(); ok {
			return failure
		}
		if busy(err) {
			// Nothing ran, the error names the holder, and Waiting may retry: a dump of
			// the refused run's header lines per attempt would only repeat it.
			return err
		}
		_, _ = os.Stderr.Write(captured)
		return err
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

// failedSteps is a sink over a run's journal that keeps the first failed
// step result. Steps run concurrently, so Handle locks.
type failedSteps struct {
	mu     sync.Mutex
	failed *StepFailure
}

func (h *failedSteps) Handle(_ context.Context, r journal.Record) {
	run, ok := r.Run()
	if !ok || run.Status != journal.StatusFail {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failed == nil {
		cause, _, _ := strings.Cut(strings.TrimSpace(run.Error), "\n")
		h.failed = &StepFailure{Project: run.Project, Target: run.Target, Cause: cause, Ref: run.Ref}
	}
}

func (h *failedSteps) first() (*StepFailure, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.failed, h.failed != nil
}
