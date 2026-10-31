package guard

import (
	"context"
	"fmt"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
)

// recordRework counts one rework round on the job whose worker addressed was sent a
// continuation after handing back: the worker's spawn title names a job that has exited or
// ended. The model-tier lens reads the count, as evidence that a tier needed another round.
//
// It fails open and records nothing it cannot attribute: no cache, an unreadable store, a
// spawn the spawner could not hand a job out in (see [spawnedAgent.UntrustedJob]), a job
// still taken, and a dry run. It never changes the verdict.
func recordRework(ctx context.Context, at location, facts hint.Gate, addressed string) {
	if isDryRun(ctx) {
		return
	}
	if row, ok := handedBackRow(ctx, at, facts, addressed); ok {
		_ = job.NewStore(at.jobs()).RecordRework(ctx, row.ID)
	}
}

// handedBackRow is the row addressed's spawn title names when it has exited or
// ended; false when nothing attributes one.
func handedBackRow(ctx context.Context, at location, facts hint.Gate, addressed string) (types.Job, bool) {
	if at.cacheDir == "" || addressed == "" {
		return types.Job{}, false
	}
	rec, ok := readSpawnedAgent(facts, resolveAgentID(facts, addressed))
	if !ok || rec.UntrustedJob != "" {
		return types.Job{}, false
	}
	rows, err := leaseRows(ctx, at)
	if err != nil {
		return types.Job{}, false
	}
	row, ok := spawnTitleRow(rows, rec.Description)
	if !ok || (row.State != types.StateExited && !row.State.Terminal()) {
		return types.Job{}, false
	}
	return row, true
}

// adviseContinueHandedBack tells the root the worker it continues handed its
// job back. It never denies, so the root keeps its own call.
func adviseContinueHandedBack(ctx context.Context, at location, facts hint.Gate, addressed string) advice {
	row, ok := handedBackRow(ctx, at, facts, addressed)
	if !ok {
		return advice{}
	}
	if row.EndedWithSession() {
		return advice{
			Say: fmt.Sprintf("magus workspace: job %s ended with its worker's session; the resumed worker runs `%s` to take it back. No re-fork and no new row.",
				row.ID, hint.JobExec.With(row.ID)),
			Why: "The row's write boundary is the one the worker held, and job exec rebinds it.",
		}
	}
	if row.State.Terminal() {
		return advice{
			Say: fmt.Sprintf("magus workspace: job %s is %s; fork a new job for more work.", row.ID, row.State),
			Why: "An ended job grades nothing and the worker holds no write paths, so its writes in its checkout are denied.",
		}
	}
	return advice{
		Say: fmt.Sprintf("magus workspace: job %s is exited; widen it yourself with `%s`, or the worker re-takes it with `%s`.",
			row.ID, hint.JobApply, hint.JobExec.With(row.ID)),
		Why: "The worker stays bound to the exited job's write paths, and a write outside them is denied.",
	}
}
