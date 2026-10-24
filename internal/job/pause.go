package job

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// Pause marks each job paused for reason until the unix second until, zero for no bound,
// so `magus job exec` and the guard refuse its holder's next magus run and the sweep and
// `magus job prune` leave the row alone. Past until the pause lapses. It preempts nothing:
// a claim the holder already has finishes. The pause records the caller as its owner.
// Pausing a paused job keeps the first time and takes the new owner, reason and bound. A
// bound caller is refused, since a holder that could pause or resume jobs could also lift
// its own pause.
func (s *Store) Pause(ctx context.Context, reason string, until int64, ids ...string) ([]types.Job, error) {
	if strings.TrimSpace(reason) == "" {
		return nil, errors.New("job: a pause names its reason, which the holder and the person resuming it read")
	}
	by := trail.StampOrigin(ctx, types.Origin{})
	return s.setPaused(ctx, ids, func(cur *types.Job, now int64) error {
		if until != 0 && until <= now {
			return fmt.Errorf("job: %s: the pause's bound is already past, so it would lapse as it was written", cur.ID)
		}
		if cur.Paused == 0 {
			cur.Paused = now
		}
		cur.PausedBy, cur.PauseReason, cur.PausedUntil = by, reason, until
		return nil
	})
}

// Resume lifts the pause on each job. Resuming a job that is not paused changes nothing.
func (s *Store) Resume(ctx context.Context, ids ...string) ([]types.Job, error) {
	return s.setPaused(ctx, ids, func(cur *types.Job, _ int64) error {
		cur.Paused, cur.PausedBy, cur.PauseReason, cur.PausedUntil = 0, types.Origin{}, "", 0
		return nil
	})
}

// setPaused applies change to each of ids, a live job.
func (s *Store) setPaused(ctx context.Context, ids []string, change func(cur *types.Job, now int64) error) ([]types.Job, error) {
	if actor := s.Actor(); actor.Bound() && len(ids) > 0 {
		return nil, refuse(actor, ids[0], "a holder neither pauses nor resumes a job; that is a person's or the orchestrator's")
	}
	rows := make([]types.Job, 0, len(ids))
	for _, id := range ids {
		row, err := s.mutate(ctx, id, asDeclaration, func(cur *types.Job, exists bool, now int64) error {
			switch {
			case !exists:
				return fmt.Errorf("%w: %q, `%s` lists the declared ids", ErrUnknownJob, id, hint.LsJobs)
			case cur.State.Terminal():
				return fmt.Errorf("job: %s already ended %s, so there is nothing to pause or resume", id, cur.State)
			}
			return change(cur, now)
		})
		if err != nil {
			return rows, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// PausedLine is the one line that refuses a paused job's holder a magus run, naming who
// paused it and why, and the command that lifts it. Exec and the guard both say it.
func PausedLine(row types.Job) string {
	line := fmt.Sprintf("job %s is paused", row.ID)
	if row.PauseReason != "" {
		line = fmt.Sprintf("%s by %s: %s", line, row.PausedBy.Label(), row.PauseReason)
	}
	if row.PausedUntil != 0 {
		line = fmt.Sprintf("%s (until %s)", line, time.Unix(row.PausedUntil, 0).UTC().Format(time.RFC3339))
	}
	return fmt.Sprintf("%s, so its holder starts no magus run until `%s job resume %s`", line, hint.BinaryName(), row.ID)
}
