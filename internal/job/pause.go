package job

import (
	"context"
	"fmt"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// Pause marks each job paused, so `magus job exec` and the guard refuse its holder's next
// magus run. It preempts nothing: a claim the holder already has finishes. Pausing a
// paused job keeps the first time. A bound caller is refused, since a holder that could
// pause or resume jobs could also lift its own pause.
func (s *Store) Pause(ctx context.Context, ids ...string) ([]types.Job, error) {
	return s.setPaused(ctx, ids, true)
}

// Resume lifts the pause on each job. Resuming a job that is not paused changes nothing.
func (s *Store) Resume(ctx context.Context, ids ...string) ([]types.Job, error) {
	return s.setPaused(ctx, ids, false)
}

func (s *Store) setPaused(ctx context.Context, ids []string, paused bool) ([]types.Job, error) {
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
			case !paused:
				cur.Paused = 0
			case cur.Paused == 0:
				cur.Paused = now
			}
			return nil
		})
		if err != nil {
			return rows, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// PausedLine is the one line that refuses a paused job's holder a magus run, naming the
// pause and the command that lifts it. Exec and the guard both say it.
func PausedLine(id string) string {
	return fmt.Sprintf("job %s is paused, so its holder starts no magus run until `%s job resume %s`", id, hint.BinaryName(), id)
}
