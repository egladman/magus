package job

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/egladman/magus/types"
)

// ErrUnknownJob reports a registration against an id no row carries, and errNoBase one
// that named no base. Sentinels rather than bare strings so a door can tell a worker's
// mistake from a store failure; the wrapped messages carry the id and the next step.
// ErrUnknownJob is exported because a door branches on it; nothing branches on the
// other, so it stays inside.
var (
	ErrUnknownJob = errors.New("job: unknown lease")
	errNoBase     = errors.New("job: a registration needs the base the worker landed on")
)

// Exec records the base a worker reports it actually landed on and returns the row
// with the divergence verdict computed at that moment.
//
// A FACT, NOT A GATE. Every verdict registers, BaseDiverged included: refusing here would
// leave the orchestrator with no record that a worker went to the wrong base, which is the
// one case the record is for. The caller gets the verdict and [BaseAdvice]'s reading
// of it, and decides.
//
// It is the ONE write here that does not create the row it names. Update declares a lease
// and advances one with the same call, which is right for an orchestrator writing its own
// plan; a WORKER registering an id nothing declared was handed the wrong id, and a row
// invented for it would bury that under a plausible-looking ledger entry.
func (s *Store) Exec(ctx context.Context, id, reportedBase string) (types.Job, error) {
	base := strings.TrimSpace(reportedBase)
	if base == "" {
		return types.Job{}, fmt.Errorf("%w, in the form `magus vcs checkpoint -o name` prints"+
			" (`<rev>`, or `<rev>+<digest>` when the tree is dirty). Run that in the tree you are working in"+
			" and exec what it prints", errNoBase)
	}
	return s.mutate(ctx, id, asExec, func(cur *types.Job, exists bool, now int64) error {
		if !exists {
			return fmt.Errorf("%w %q: nothing declared it, so there is no checkpoint to exec against."+
				" Check the declared ids with `magus_job list` and exec under the id the"+
				" orchestrator handed you", ErrUnknownJob, id)
		}
		cur.ReportedBase = base
		cur.BaseVerdict = compareBase(cur.Checkpoint, base)
		cur.Registered = now
		return nil
	})
}

// compareBase compares the checkpoint a lease was handed with the base its worker
// reported, both as `magus vcs checkpoint -o name` prints them: `<rev>` for a clean tree,
// `<rev>+<digest>` for a dirty one. See types.JobBaseVerdict for why the answer is not
// a boolean.
func compareBase(checkpoint, reported string) types.JobBaseVerdict {
	checkpoint, reported = strings.TrimSpace(checkpoint), strings.TrimSpace(reported)
	switch {
	case checkpoint == "" || reported == "":
		return types.BaseUnknown
	case checkpoint == reported:
		return types.BaseMatch
	case baseRevision(checkpoint) == baseRevision(reported):
		return types.BaseRevisionMatch
	default:
		return types.BaseDiverged
	}
}

// baseRevision is the revision half of a checkpoint token: everything before the "+" that
// separates it from a dirty tree's patch digest.
func baseRevision(token string) string {
	rev, _, _ := strings.Cut(token, "+")
	return rev
}

// BaseAdvice is what the registering worker is told: what the verdict means in
// terms of the two tokens it compared, and what to do next.
//
// Derived from the row, never stored. The verdict is the fact; this is one rendering of
// it, and a stored sentence would be a second thing to keep true when the wording changes.
func BaseAdvice(row types.Job) string {
	switch row.BaseVerdict {
	case types.BaseMatch:
		return fmt.Sprintf("recorded job %s's base as %s, which is the checkpoint it was handed. Nothing to reconcile; carry on.",
			row.ID, row.ReportedBase)

	case types.BaseRevisionMatch:
		return fmt.Sprintf("recorded job %s's base on revision %s, which IS the revision it was handed,"+
			" but the uncommitted patch is not: the checkpoint digest is %s and yours is %s."+
			" A checkpoint is a revision plus a dirty-patch DIGEST, so you share the commit and not the working tree."+
			" The digest cannot give the patch back, so there is nothing here to restore from:"+
			" have the orchestrator commit the work the job was cut against, or re-cut the checkpoint"+
			" against the tree you are on.",
			row.ID, baseRevision(row.ReportedBase), patchDigestOf(row.Checkpoint), patchDigestOf(row.ReportedBase))

	case types.BaseDiverged:
		return fmt.Sprintf("recorded job %s's base, and it DIVERGED: your base %s is not the checkpoint %s the job was handed."+
			" Respawn from %s, or materialize the files you touch from it before you edit them,"+
			" so what you write lands on the tree the plan was cut against.",
			row.ID, baseRevision(row.ReportedBase), baseRevision(row.Checkpoint), baseRevision(row.Checkpoint))

	case types.BaseUnknown:
		return fmt.Sprintf("recorded job %s's base as %s. It carries no checkpoint, so there is nothing to compare"+
			" your base against and the verdict is unknown rather than a match."+
			" Have the orchestrator put one on the job (`magus vcs checkpoint -o name`) before the next job is cut,"+
			" so a later reader can tell whether a worker was on the base it was given.",
			row.ID, row.ReportedBase)

	default:
		return fmt.Sprintf("recorded job %s's base as %s, and this magus does not recognize the verdict %q it computed."+
			" Read the row itself rather than this sentence.", row.ID, row.ReportedBase, row.BaseVerdict)
	}
}

// patchDigestOf is the dirty-patch half of a checkpoint token, or "none (clean tree)" when
// the token carries no "+". Spelled out rather than left empty: the revision-match reading
// names both digests, and an empty one there reads as a rendering bug instead of as the
// clean tree it is.
func patchDigestOf(token string) string {
	_, digest, dirty := strings.Cut(strings.TrimSpace(token), "+")
	if !dirty || digest == "" {
		return "none (clean tree)"
	}
	return digest
}
