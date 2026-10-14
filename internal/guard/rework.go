package guard

import (
	"context"

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
	if at.cacheDir == "" || addressed == "" || isDryRun(ctx) {
		return
	}
	rec, ok := readSpawnedAgent(facts, resolveAgentID(facts, addressed))
	if !ok || rec.UntrustedJob != "" {
		return
	}
	rows, err := leaseRows(ctx, at)
	if err != nil {
		return
	}
	row, ok := spawnTitleRow(rows, rec.Description)
	if !ok || (row.State != types.StateExited && !row.State.Terminal()) {
		return
	}
	_ = job.NewStore(job.Location{CacheDir: at.cacheDir, Root: at.workspace}).RecordRework(ctx, row.ID)
}
