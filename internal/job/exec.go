package job

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
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

// Exec takes a job in this store's checkout: it records the base a worker reports it
// actually landed on, moves a declared row to running, and returns the row with the
// divergence verdict computed at that moment. Taking it again from the same checkout
// records the new base and nothing else.
//
// It refuses a job another checkout holds. Wait grades the checkout the row names, so a
// second taker would silently move that grade off the holder's tree; the job moves only
// once its holder ends it and it is declared again, which clears the checkout.
//
// THE BASE VERDICT IS A FACT, NOT A GATE. Every verdict registers, BaseDiverged included: refusing here would
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
		return types.Job{}, fmt.Errorf("%w: in the form `magus vcs checkpoint -o name` prints"+
			" (`<rev>`, or `<rev>+<digest>` when the tree is dirty), run that in the tree you are working in"+
			" and exec what it prints", errNoBase)
	}
	// Resolved before the lock, since resolving runs the VCS; the row is read again under it.
	var checkpoint string
	if f, err := s.read(); err == nil {
		if i := slices.IndexFunc(f.Jobs, func(r types.Job) bool { return r.ID == id }); i >= 0 {
			checkpoint = f.Jobs[i].Checkpoint
		}
	}
	full := s.fullRevisions(ctx, checkpoint, base)
	return s.mutate(ctx, id, asExec, func(cur *types.Job, exists bool, now int64) error {
		if !exists {
			return fmt.Errorf("%w: %q, nothing declared it, so there is no checkpoint to exec against,"+
				" check the declared ids with `%s` and exec under the id the"+
				" orchestrator handed you", ErrUnknownJob, id, hint.LsJobs)
		}
		if cur.State.Terminal() {
			return fmt.Errorf("job: %s already ended %s, so there is nothing left to take,"+
				" `%s` lists the live ones", id, cur.State, hint.LsJobs)
		}
		// Relative would be resolved against whichever process sweeps the row later.
		here := ""
		if s.root != "" {
			if abs, err := filepath.Abs(s.root); err == nil {
				here = abs
			}
		}
		// Held means running: an exec moves a declared row there. A declared row may carry a
		// checkout already, the forker's or one a hook recorded before anyone took the job,
		// and it is still the forker's to hand out, not a holder's to keep.
		if cur.State == types.StateRunning && cur.CheckoutRoot != "" && cur.CheckoutRoot != here {
			return fmt.Errorf("job: %s is held in %s (%s, updated %s ago), and wait grades the checkout that holds it,"+
				" its holder gives it up with `%s`, and whoever forked it then hands it out again with `%s`",
				id, cur.CheckoutRoot, cur.State, updatedAgo(*cur), hint.JobExit.With(id), hint.JobApply)
		}
		cur.ReportedBase = base
		cur.BaseVerdict = compareBase(withFullRevision(cur.Checkpoint, full), withFullRevision(base, full))
		cur.Registered = now
		cur.CheckoutRoot = here
		if cur.State == types.StateDeclared {
			cur.State = types.StateRunning
		}
		return nil
	})
}

// fullRevisions maps the revision half of each token to the full revision this checkout's
// VCS resolves it to, so an abbreviated revision and the full one it abbreviates compare
// equal. Asked only when the two halves differ as strings; a revision the VCS cannot
// place, or a checkout with none, maps to nothing and is compared as written.
func (s *Store) fullRevisions(ctx context.Context, tokens ...string) map[string]string {
	revs := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if rev := checkpointRevision(t); rev != "" && !slices.Contains(revs, rev) {
			revs = append(revs, rev)
		}
	}
	if s.root == "" || len(revs) < 2 {
		return nil
	}
	driver, _ := resolveDriver(ctx, s.root)
	if driver == nil {
		return nil
	}
	full := map[string]string{}
	for _, rev := range revs {
		if c, err := driver.FindCommit(ctx, s.root, rev); err == nil && c.ID != "" {
			full[rev] = c.ID
		}
	}
	return full
}

// withFullRevision is token with its revision half replaced by the full revision full
// names for it, the dirty-tree digest kept.
func withFullRevision(token string, full map[string]string) string {
	rev, digest := types.ParseCheckpointToken(token)
	id, ok := full[rev]
	switch {
	case !ok:
		return token
	case digest != "":
		return id + "+" + digest
	default:
		return id
	}
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
	case checkpointRevision(checkpoint) == checkpointRevision(reported):
		return types.BaseRevisionMatch
	default:
		return types.BaseDiverged
	}
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
			row.ID, checkpointRevision(row.ReportedBase), describePatchDigest(row.Checkpoint), describePatchDigest(row.ReportedBase))

	case types.BaseDiverged:
		return fmt.Sprintf("recorded job %s's base, and it DIVERGED: your base %s is not the checkpoint %s the job was handed."+
			" Respawn from %s, or materialize the files you touch from it before you edit them,"+
			" so what you write lands on the tree the plan was cut against.",
			row.ID, checkpointRevision(row.ReportedBase), checkpointRevision(row.Checkpoint), checkpointRevision(row.Checkpoint))

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

// describePatchDigest is the dirty-patch half of a checkpoint token, or "none (clean tree)" when
// the token carries no "+". Spelled out rather than left empty: the revision-match reading
// names both digests, and an empty one there reads as a rendering bug instead of as the
// clean tree it is.
func describePatchDigest(token string) string {
	_, digest := types.ParseCheckpointToken(token)
	if digest == "" {
		return "none (clean tree)"
	}
	return digest
}
