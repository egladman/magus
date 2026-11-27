package job

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// ErrHeldElsewhere reports a registration against a running job another checkout holds,
// as [job.ErrUnknownJob] reports one against an id no row carries. Sentinels rather than
// bare strings so a door can tell a worker's mistake from a store failure; the wrapped
// messages carry the id and the next step. ErrHeldElsewhere holds until the row changes,
// so a caller may stop retrying on it, unlike a cancelled context or lock contention.
var ErrHeldElsewhere = errors.New("job: another checkout holds the job")

// heldError is ErrHeldElsewhere with the message that names the holder, which reads
// whole without the sentinel's own text.
type heldError struct{ msg string }

func (e heldError) Error() string { return e.msg }

func (heldError) Unwrap() error { return ErrHeldElsewhere }

// Exec takes a job in this store's checkout: it reads the base the checkout is on with
// [Store.CheckoutToken], moves a declared row to running, or an exited one taken again in
// the checkout it was taken in, and returns the row with the
// divergence verdict computed at that moment. Taking it again from the same checkout
// records the new base and nothing else. A checkout that reports no revision is refused,
// since there is no base to record.
//
// The base is read, never taken from the caller: a token a worker types is what it
// believes it is on, and a clean token typed over a dirty tree would record a match the
// tree does not hold.
//
// It refuses a job another checkout holds. Wait grades the checkout the row names, so a
// second taker would silently move that grade off the holder's tree; the job moves only
// once its holder ends it and it is declared again, which clears the checkout. A job that
// owns a checkout (see [job.Job.Worktree]) is taken there and nowhere else.
// A writing job with none, taken in a linked checkout the host isolated its
// worker in, adopts that checkout, and [TakeAdvice] says so.
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
func (s *Store) Exec(ctx context.Context, id string, opts ...ExecOption) (job.Job, error) {
	var o execOptions
	for _, opt := range opts {
		opt(&o)
	}
	// Read before the lock, since it runs the VCS. The row is read again there.
	var row job.Job
	if f, err := s.Read(ctx); err == nil {
		if i := slices.IndexFunc(f.Jobs, func(r job.Job) bool { return r.ID == id }); i >= 0 {
			row = f.Jobs[i]
		}
	}
	facts, err := s.readExecFacts(ctx, id, row)
	if err != nil {
		return job.Job{}, err
	}
	// Relative would be resolved against whichever process sweeps the row later.
	here := ""
	if s.root != "" {
		if abs, err := filepath.Abs(s.root); err == nil {
			here = abs
		}
	}
	if o.placement != nil {
		if facts, err = s.readyAdoption(ctx, row, here, facts, *o.placement); err != nil {
			return job.Job{}, err
		}
	}
	base, full := facts.base, facts.full
	return s.MutateSeeing(ctx, id, job.AsExec, func(cur *job.Job, exists bool, now int64, rows []job.Job) error {
		if !exists {
			return fmt.Errorf("%w: %q, nothing declared it, so there is no checkpoint to exec against,"+
				" check the declared ids with `%s` and exec under the id the"+
				" orchestrator handed you", job.ErrUnknownJob, id, hint.LsJobs)
		}
		if err := execRefusal(cur, here, facts); err != nil {
			return err
		}
		if err := checkoutWriterConflict(*cur, here, rows); err != nil {
			return err
		}
		// An exited row taken again where it was taken is its holder sent back to work: it
		// edits its paths again, so it claims them again. Exited is not terminal, so ADR
		// 0013 lets the holder's own exec reopen it.
		reopened := cur.State == job.StateExited && cur.CheckoutRoot != "" && cur.CheckoutRoot == here
		if cur.State.Terminal() || reopened {
			cur.State = job.StateRunning
		}
		cur.ReportedBase = base
		judged := base
		if rev := checkpointRevision(cur.Checkpoint); rev != "" && rev == facts.ownBase {
			judged = onRevision(base, rev)
		}
		cur.BaseVerdict = compareBase(withFullRevision(cur.Checkpoint, full), withFullRevision(judged, full))
		cur.Registered = now
		cur.CheckoutRoot = here
		if cur.State == job.StateDeclared {
			cur.State = job.StateRunning
		}
		return nil
	})
}

// execFacts is what taking a job reads from the checkout, ahead of any lock.
type execFacts struct {
	base  string
	moved *job.Worktree
	full  map[string]string
	// ownBase is row's checkpoint revision when this checkout is row's own and
	// its head descends from it, "" otherwise.
	ownBase string
}

// readExecFacts reads them for row, the zero row when nothing declared id.
func (s *Store) readExecFacts(ctx context.Context, id string, row job.Job) (execFacts, error) {
	base, err := s.CheckoutToken(ctx)
	if err != nil {
		return execFacts{}, fmt.Errorf("the checkout at %s reports no revision, so there is no base to record for %s,"+
			" `%s` run there says why: %w", s.root, id, hint.VCSCheckpoint.With("-o name"), err)
	}
	return execFacts{
		base:    base,
		moved:   s.untakenElsewhere(ctx, row),
		full:    s.fullRevisions(ctx, row.Checkpoint, base),
		ownBase: s.ownCommitsBase(ctx, row, base),
	}, nil
}

// ownCommitsBase is row's checkpoint revision when this is row's own
// checkout and base descends from it. The commits between are the job's
// work, kept across a re-fork. They are no base its holder strayed to.
func (s *Store) ownCommitsBase(ctx context.Context, row job.Job, base string) string {
	checkpoint, head := checkpointRevision(row.Checkpoint), checkpointRevision(base)
	if s.root == "" || checkpoint == "" || head == "" || SameRevision(checkpoint, head) || !row.Worktree.Standing() {
		return ""
	}
	here, err := filepath.Abs(s.root)
	if err != nil || !samePath(here, row.Worktree.Root) {
		return ""
	}
	driver, _ := resolveDriver(ctx, here)
	if driver == nil {
		return ""
	}
	if contained, err := driver.IsAncestor(ctx, here, checkpoint, head); err != nil || !contained {
		return ""
	}
	return checkpoint
}

// onRevision is token with its revision half replaced by revision, the
// dirty-tree digest kept.
func onRevision(token, revision string) string {
	if _, digest := types.ParseCheckpointToken(token); digest != "" {
		return fmt.Sprintf("%s+%s", revision, digest)
	}
	return revision
}

// Placement readies a checkout [Store.Exec] adopts for a job nobody took.
// Exe, or the ./magus where the job was forked, is copied in when built at
// the checkpoint. Provision readies a checkout that builds its own, as
// [ProvisionCheckout] does, and says whether it reused one. A nil Provision
// leaves such a checkout without one.
type Placement struct {
	Exe       string
	Provision func(ctx context.Context, row job.Job, wt job.Worktree) (reused bool, err error)
}

// ExecOption is an option to [Store.Exec].
type ExecOption func(*execOptions)

type execOptions struct {
	placement *Placement
}

// Placing makes Exec ready the checkout it adopts before it takes the job.
// It moves it to the job's checkpoint, refusing uncommitted work. It places
// a ./magus built there and pins it on the row. Without it Exec adopts the
// checkout as it stands, which is what the guard asks about.
func Placing(p Placement) ExecOption {
	return func(o *execOptions) { o.placement = &p }
}

// readyAdoption readies facts.moved when Exec would move row there, and
// reads the base again from the moved checkout. Any other row's facts come
// back as they were.
func (s *Store) readyAdoption(ctx context.Context, row job.Job, here string, facts execFacts, p Placement) (execFacts, error) {
	if !wouldAdopt(row, here, facts) {
		return facts, nil
	}
	ready, err := s.readyCheckout(ctx, row, *facts.moved, p)
	if err != nil {
		return facts, err
	}
	base, err := s.CheckoutToken(ctx)
	if err != nil {
		return facts, fmt.Errorf("the checkout at %s reports no revision after moving to %s's checkpoint: %w", here, row.ID, err)
	}
	facts.moved, facts.base, facts.full = &ready, base, s.fullRevisions(ctx, row.Checkpoint, base)
	return facts, nil
}

// wouldAdopt reports whether Exec would move row to facts.moved. A refusal
// moves nothing, and the locked write refuses it again.
func wouldAdopt(row job.Job, here string, facts execFacts) bool {
	if facts.moved == nil {
		return false
	}
	return execRefusal(&row, here, facts) == nil && row.Worktree == facts.moved
}

// readyCheckout moves wt to row's checkpoint and pins a ./magus built
// there, saying what it did. The pin is magus placing an exact build, so
// it is written with the row and never through [Store.PinBinary], which
// refuses a bound worker.
func (s *Store) readyCheckout(ctx context.Context, row job.Job, wt job.Worktree, p Placement) (job.Worktree, error) {
	followed, err := s.followCheckpoint(ctx, row.ID, wt, row.Checkpoint, true)
	if err != nil {
		return wt, fmt.Errorf("adopting %s for %s at its checkpoint: %w", wt.Root, row.ID, err)
	}
	if followed.Base != wt.Base {
		execNote(ctx, fmt.Sprintf("moved %s from %.12s to %.12s, the checkpoint of %s", followed.Root, wt.Base, followed.Base, row.ID))
	}
	switch {
	case BuildsItsOwnMagus(followed.Root) && p.Provision != nil:
		reused, err := p.Provision(ctx, row, followed)
		if err != nil {
			return wt, err
		}
		if !reused {
			execNote(ctx, fmt.Sprintf("built ./magus in %s at %.12s: no magus built there stood to reuse", followed.Root, followed.Base))
		}
	default:
		exes := []string{p.Exe}
		if row.ForkedIn != "" {
			exes = append(exes, filepath.Join(row.ForkedIn, binaryName))
		}
		placed, err := s.PlaceBuiltBinary(ctx, followed.Base, followed.Root, exes...)
		if err != nil {
			return wt, err
		}
		if placed == "" {
			execNote(ctx, fmt.Sprintf("placed no ./magus in %s: no magus built at %.12s stands to copy, so %s pins none", followed.Root, followed.Base, row.ID))
			return followed, nil
		}
		execNote(ctx, fmt.Sprintf("placed ./magus in %s from %s, built at %.12s", followed.Root, placed, followed.Base))
	}
	bin := filepath.Join(followed.Root, binaryName)
	digest, err := BinaryDigest(bin)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		execNote(ctx, fmt.Sprintf("%s holds no ./magus after its bootstrap, so %s pins none", followed.Root, row.ID))
		return followed, nil
	case err != nil:
		return wt, fmt.Errorf("pinning %s for %s: %w", bin, row.ID, err)
	}
	followed.Binary, followed.BinaryDigest = bin, digest
	execNote(ctx, fmt.Sprintf("pinned %s for %s: %s", bin, row.ID, digest))
	return followed, nil
}

// execNote is one line of what exec did, on stderr: stdout is its record.
func execNote(ctx context.Context, msg string) {
	slog.InfoContext(ctx, msg, attr.Notice("note"))
}

// execRefusal is why Exec refuses row in the checkout here, nil for none.
// It moves row to facts.moved. A row with no checkout moves at any base,
// since the base verdict is a fact. An adopted checkout nobody took moves
// only when here is at the row's checkpoint.
func execRefusal(row *job.Job, here string, facts execFacts) error {
	if facts.moved != nil && row.CheckoutRoot == "" && (!row.Worktree.Standing() || !row.Worktree.Made) && !row.State.Terminal() {
		switch {
		case !row.Worktree.Standing():
			row.Worktree = facts.moved
		default:
			switch compareBase(withFullRevision(row.Checkpoint, facts.full), withFullRevision(facts.base, facts.full)) {
			case job.BaseMatch, job.BaseRevisionMatch, job.BaseUnknown:
				row.Worktree = facts.moved
			case job.BaseDiverged:
				return heldError{fmt.Sprintf("job: %s is bound to %s, a checkout its fork adopted, and nobody took it there, so it may be taken here in %s instead, once this checkout is at its checkpoint %s: it is at %s",
					row.ID, row.Worktree.Root, here, checkpointRevision(row.Checkpoint), checkpointRevision(facts.base))}
			}
		}
	}
	// A session resuming that work in the same checkout takes it back.
	resumed := row.EndedWithSession() && (row.CheckoutRoot == "" || row.CheckoutRoot == here)
	if row.State.Terminal() && !resumed {
		return fmt.Errorf("job: %s already ended %s, so there is nothing left to take,"+
			" `%s` lists the live ones", row.ID, row.State, hint.LsJobs)
	}
	if err := WrongCheckout(*row, here); err != nil {
		return err
	}
	// Held means running. A declared row may carry a checkout already,
	// and it is still the forker's to hand out, not a holder's to keep.
	if row.State == job.StateRunning && row.CheckoutRoot != "" && row.CheckoutRoot != here {
		return heldError{fmt.Sprintf("job: %s is held in %s (%s, updated %s ago), and wait grades the checkout that holds it,"+
			" its holder gives it up with `%s`, and whoever forked it then hands it out again with `%s`",
			row.ID, row.CheckoutRoot, row.State, updatedAgo(*row), hint.JobExit.With(row.ID), hint.JobApply)}
	}
	return nil
}

// ExecRefusal is why [Store.Exec] in the checkout here would refuse row.
// The guard asks before it binds a caller, so it reads that checkout as
// Exec does.
func (s *Store) ExecRefusal(ctx context.Context, row job.Job, here string) error {
	config := s.storeConfig
	config.root = here
	facts, err := (&Store{storeConfig: config}).open().readExecFacts(ctx, row.ID, row)
	if err != nil {
		return err
	}
	if err := execRefusal(&row, here, facts); err != nil {
		return err
	}
	f, err := s.Read(ctx)
	if err != nil {
		return err
	}
	return checkoutWriterConflict(row, here, f.Jobs)
}

// checkoutWriterConflict refuses a second writer only on a real overlap
// with a live one here. Disjoint paths co-write, as the fork proved.
func checkoutWriterConflict(row job.Job, here string, rows []job.Job) error {
	if here == "" || len(row.HardPaths()) == 0 {
		return nil
	}
	for _, peer := range rows {
		if peer.ID == row.ID || peer.CheckoutRoot != here || len(peer.HardPaths()) == 0 {
			continue
		}
		if !hardPathsOverlap(row, peer) {
			continue
		}
		if peer.State != job.StateRunning && peer.State != job.StateExited {
			continue
		}
		if !peer.Worktree.Standing() && !row.Worktree.Standing() && peer.Parent != "" && row.Parent != "" {
			continue
		}
		return heldError{fmt.Sprintf("job: %s and %s would be graded against the same HEAD in %s and their write paths overlap; give %s its own checkout or finish verifying %s before taking another writer here",
			row.ID, peer.ID, here, row.ID, peer.ID)}
	}
	return nil
}

// hardPathsOverlap matches claims the way [Store.WriteProof] does, so
// the refusal and the recorded proof cannot disagree.
func hardPathsOverlap(row, peer job.Job) bool {
	for _, mine := range row.HardPaths() {
		for _, theirs := range peer.HardPaths() {
			if job.PathsIntersect(mine, theirs) && !job.ReleasedReach(peer, theirs, mine) && !job.ReleasedReach(row, mine, theirs) {
				return true
			}
		}
	}
	return false
}

// untakenElsewhere is this checkout adopted for row, which nobody took: a
// worker the host isolated here binds the job here. Row's fork adopted
// another checkout, or row owns none (see [hostIsolated]).
func (s *Store) untakenElsewhere(ctx context.Context, row job.Job) *job.Worktree {
	if s.root == "" || row.CheckoutRoot != "" || row.Worktree.Standing() && row.Worktree.Made {
		return nil
	}
	here, err := filepath.Abs(s.root)
	if err != nil {
		return nil
	}
	if row.Worktree.Standing() && samePath(here, row.Worktree.Root) || !row.Worktree.Standing() && !hostIsolated(row, here) {
		return nil
	}
	wt, err := s.adoptCheckout(ctx, row.ID, here)
	if err != nil {
		return nil
	}
	return &wt
}

// hostIsolated reports whether writing row is taken at here, a checkout
// other than the one its fork ran in. The forker's own checkout is never
// adopted; a row whose fork recorded none adopts nothing.
func hostIsolated(row job.Job, here string) bool {
	return len(row.Paths.Write) > 0 && row.ForkedIn != "" && !samePath(row.ForkedIn, here)
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
		return fmt.Sprintf("%s+%s", id, digest)
	default:
		return id
	}
}

// compareBase compares the checkpoint a lease was handed with the base its worker
// reported, both as `magus vcs checkpoint -o name` prints them: `<rev>` for a clean tree,
// `<rev>+<digest>` for a dirty one. See job.BaseVerdict for why the answer is not
// a boolean.
func compareBase(checkpoint, reported string) job.BaseVerdict {
	checkpoint, reported = strings.TrimSpace(checkpoint), strings.TrimSpace(reported)
	switch {
	case checkpoint == "" || reported == "":
		return job.BaseUnknown
	case checkpoint == reported:
		return job.BaseMatch
	case checkpointRevision(checkpoint) == checkpointRevision(reported):
		return job.BaseRevisionMatch
	default:
		return job.BaseDiverged
	}
}

// SameRevision reports whether a and b name one revision: equal ignoring case, or hex ids
// one of which abbreviates the other in at least minAbbrev digits, the shortest prefix git
// prints. Two empty revisions name nothing, so they are not the same.
func SameRevision(a, b string) bool {
	a, b = strings.ToLower(strings.TrimSpace(a)), strings.ToLower(strings.TrimSpace(b))
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	short, long := a, b
	if len(short) > len(long) {
		short, long = long, short
	}
	return len(short) >= minAbbrev && isHex(long) && strings.HasPrefix(long, short)
}

// minAbbrev is the fewest digits an abbreviated revision may have: shorter prefixes match
// too many commits to name one.
const minAbbrev = 7

func isHex(s string) bool {
	return strings.Trim(s, "0123456789abcdef") == ""
}

// TakeAdvice reports the base, the checkout row adopted where it was taken,
// and any preparation needed before editing.
func TakeAdvice(ctx context.Context, row job.Job, opts types.VCSOptions) string {
	base := BaseAdvice(row)
	if row.Worktree.Standing() && !row.Worktree.Made && samePath(row.Worktree.Root, row.CheckoutRoot) {
		base = fmt.Sprintf("%s\njob %s adopted the checkout %s: wait grades it there, and it is taken there and nowhere else.",
			base, row.ID, row.Worktree.Root)
	}
	if row.ReadOnly {
		return base
	}
	res, err := vcs.Resolve(ctx, row.CheckoutRoot, "", opts)
	if err != nil {
		return fmt.Sprintf("%s\ncould not inspect checkout preparation: %v", base, err)
	}
	prep := GitCheckoutPreparation(ctx, row, row.CheckoutRoot, res.VCS)
	if prep.Reason == "" {
		return base
	}
	return fmt.Sprintf("%s\n%s", base, prep)
}

// BaseAdvice is what the registering worker is told: what the verdict means in
// terms of the two tokens it compared, and what to do next.
//
// Derived from the row, never stored. The verdict is the fact; this is one rendering of
// it, and a stored sentence would be a second thing to keep true when the wording changes.
func BaseAdvice(row job.Job) string {
	switch row.BaseVerdict {
	case job.BaseMatch:
		return fmt.Sprintf("recorded job %s's base as %s, which is the checkpoint it was handed. Nothing to reconcile; carry on.",
			row.ID, row.ReportedBase)

	case job.BaseRevisionMatch:
		return fmt.Sprintf("recorded job %s's base on revision %s, which IS the revision it was handed,"+
			" but the uncommitted patch is not: the checkpoint digest is %s and yours is %s."+
			" A checkpoint is a revision plus a dirty-patch DIGEST, so you share the commit and not the working tree."+
			" The digest cannot give the patch back, so there is nothing here to restore from:"+
			" have the orchestrator commit the work the job was cut against, or re-cut the checkpoint"+
			" against the tree you are on.",
			row.ID, checkpointRevision(row.ReportedBase), describePatchDigest(row.Checkpoint), describePatchDigest(row.ReportedBase))

	case job.BaseDiverged:
		return fmt.Sprintf("recorded job %s's base, and it DIVERGED: your base %s is not the checkpoint %s the job was handed."+
			" Respawn from %s, or materialize the files you touch from it before you edit them,"+
			" so what you write lands on the tree the plan was cut against.",
			row.ID, checkpointRevision(row.ReportedBase), checkpointRevision(row.Checkpoint), checkpointRevision(row.Checkpoint))

	case job.BaseUnknown:
		return fmt.Sprintf("recorded job %s's base as %s. It carries no checkpoint, so there is nothing to compare"+
			" your base against and the verdict is unknown rather than a match."+
			" Have the orchestrator put one on the job (`magus vcs checkpoint -o name`) before the next job is cut,"+
			" so a later reader can tell whether a worker was on the base it was given.",
			row.ID, row.ReportedBase)
	}
	return fmt.Sprintf("recorded job %s's base as %s, and this magus does not recognize the verdict %q it computed."+
		" Read the row itself rather than this sentence.", row.ID, row.ReportedBase, row.BaseVerdict)
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
