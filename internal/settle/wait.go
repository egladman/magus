package settle

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

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
