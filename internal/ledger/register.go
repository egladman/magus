package ledger

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/egladman/magus/types"
)

// ErrUnknownLease reports a registration against an id no row carries, and ErrNoBase one
// that named no base. Sentinels rather than bare strings so a door can tell a worker's
// mistake from a store failure; the wrapped messages carry the id and the next step.
var (
	ErrUnknownLease = errors.New("ledger: unknown lease")
	ErrNoBase       = errors.New("ledger: a registration needs the base the worker landed on")
)

// Register records the base a worker reports it actually landed on and returns the row
// with the divergence verdict computed at that moment.
//
// A FACT, NOT A GATE. Every verdict registers, BaseDiverged included: refusing here would
// leave the orchestrator with no record that a worker went to the wrong base, which is the
// one case the record is for. The caller gets the verdict and [RegistrationAdvice]'s reading
// of it, and decides.
//
// It is the ONE write here that does not create the row it names. Put and Update declare a
// lease and advance one with the same call, which is right for an orchestrator writing its
// own plan; a WORKER registering an id nothing declared was handed the wrong id, and a row
// invented for it would bury that under a plausible-looking ledger entry.
func (s *Store) Register(ctx context.Context, id, reportedBase string) (types.Lease, error) {
	base := strings.TrimSpace(reportedBase)
	if base == "" {
		return types.Lease{}, fmt.Errorf("%w, in the form `magus vcs checkpoint -o name` prints"+
			" (`<rev>`, or `<rev>+<digest>` when the tree is dirty). Run that in the tree you are working in"+
			" and register what it prints", ErrNoBase)
	}
	return s.mutate(ctx, id, func(cur *types.Lease, exists bool, now int64) error {
		if !exists {
			return fmt.Errorf("%w %q: nothing declared it, so there is no checkpoint to register against."+
				" Check the declared ids with `magus_ledger list` and register under the id the"+
				" orchestrator handed you", ErrUnknownLease, id)
		}
		cur.ReportedBase = base
		cur.BaseVerdict = CompareBase(cur.Checkpoint, base)
		cur.Registered = now
		return nil
	})
}

// CompareBase compares the checkpoint a lease was handed with the base its worker
// reported, both as `magus vcs checkpoint -o name` prints them: `<rev>` for a clean tree,
// `<rev>+<digest>` for a dirty one.
//
// Exported to keep "is this worker on its base" spelled once. Register is the only caller
// today; a later reader that needs the same question answered - the agent guard, the
// console - should call this rather than grow a second comparison that can drift from it.
// See types.LeaseBaseVerdict for why the answer is not a boolean.
func CompareBase(checkpoint, reported string) types.LeaseBaseVerdict {
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

// RegistrationAdvice is what the registering worker is told: what the verdict means in
// terms of the two tokens it compared, and what to do next.
//
// Derived from the row, never stored. The verdict is the fact; this is one rendering of
// it, and a stored sentence would be a second thing to keep true when the wording changes.
func RegistrationAdvice(u types.Lease) string {
	switch u.BaseVerdict {
	case types.BaseMatch:
		return fmt.Sprintf("registered lease %s on %s, which is the checkpoint it was handed. Nothing to reconcile; carry on.",
			u.ID, u.ReportedBase)

	case types.BaseRevisionMatch:
		return fmt.Sprintf("registered lease %s on revision %s, which IS the revision it was handed,"+
			" but the uncommitted patch is not: the checkpoint digest is %s and yours is %s."+
			" A checkpoint is a revision plus a dirty-patch digest, so you share the commit and not the working tree."+
			" Materialize the files you touch from the checkpoint, or have the orchestrator re-cut the checkpoint"+
			" against the tree you are on.",
			u.ID, baseRevision(u.ReportedBase), patchDigestOf(u.Checkpoint), patchDigestOf(u.ReportedBase))

	case types.BaseDiverged:
		return fmt.Sprintf("registered lease %s, and it DIVERGED: your base %s is not the checkpoint %s the lease was handed."+
			" Respawn from %s, or materialize the files you touch from it before you edit them,"+
			" so what you write lands on the tree the plan was cut against.",
			u.ID, baseRevision(u.ReportedBase), baseRevision(u.Checkpoint), baseRevision(u.Checkpoint))

	// BaseUnknown, the only verdict a registered row can carry that is not above.
	default:
		return fmt.Sprintf("registered lease %s on %s. It carries no checkpoint, so there is nothing to compare"+
			" your base against and the verdict is unknown rather than a match."+
			" Have the orchestrator put one on the lease (`magus vcs checkpoint -o name`) before the next handoff,"+
			" so a later reader can tell whether a worker was on the base it was given.",
			u.ID, u.ReportedBase)
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
