package job

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// mutate is the locked read-modify-write [Store.Update], [Store.Exec] and
// [Wait] share, and the only place a row is written row-wise. kind says what
// the write is; see [grading]. apply may fail, and nothing is written then.
func (s *Store) mutate(ctx context.Context, id string, kind grading, apply func(cur *types.Job, exists bool, now int64) error) (types.Job, error) {
	return s.mutateSeeing(ctx, id, kind, func(cur *types.Job, exists bool, now int64, _ []types.Job) error {
		return apply(cur, exists, now)
	})
}

// mutateSeeing is mutate whose apply also sees every stored row.
// The rows are read under the same lock; apply must not change them.
func (s *Store) mutateSeeing(ctx context.Context, id string, kind grading, apply func(cur *types.Job, exists bool, now int64, rows []types.Job) error) (types.Job, error) {
	if strings.TrimSpace(id) == "" {
		return types.Job{}, ErrNoID
	}
	w := &rowWrite{s: s, id: id, kind: kind}
	// Read before the lock, like Exec's checkpoint: the trail may be another
	// checkout's, and only a declaration of an ended row folds it.
	if kind == asDeclaration {
		w.denials, w.denialsKnown = s.endedAttemptDenials(id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	w.actor = s.Actor()
	w.rootHead = sync.OnceValue(func() string { return s.committedRevision(ctx) })
	err := s.update(ctx, func(f *jobsFile) error {
		w.f = f
		return w.run(ctx, apply)
	})
	if err != nil {
		return types.Job{}, err
	}
	// After the row, so a refused write folds nothing.
	if w.folded != nil {
		if err := s.appendAttempts(ctx, *w.folded); err != nil {
			return w.stored, fmt.Errorf("%s was forked again, and its last attempt was not folded: %w", id, err)
		}
	}
	if err := w.ended.record(ctx, time.Now().UnixMilli()); err != nil {
		return w.stored, err
	}
	return w.stored, nil
}

// rowWrite is one locked write of a row: what the steps of
// [Store.mutateSeeing] pass between them, in the order run calls them.
type rowWrite struct {
	s            *Store
	id           string
	kind         grading
	actor        Actor
	denials      []types.JobDenial
	denialsKnown bool
	rootHead     func() string

	f    *jobsFile
	i    int // the row's place in f.Jobs, -1 when it is new
	prev types.Job
	row  types.Job
	now  int64
	// entering is a declaration whose only change is one entry. refork is an
	// ended row declared again.
	entering, refork bool

	ended  endings
	folded *types.Attempt
	stored types.Job
}

func (w *rowWrite) exists() bool { return w.i >= 0 }

// run is the write over w.f, under both locks.
func (w *rowWrite) run(ctx context.Context, apply func(cur *types.Job, exists bool, now int64, rows []types.Job) error) error {
	w.ended, w.folded = endings{}, nil
	w.i = slices.IndexFunc(w.f.Jobs, func(e types.Job) bool { return e.ID == w.id })
	w.prev = types.Job{}
	if w.exists() {
		w.prev = w.f.Jobs[w.i]
		if err := readOnly(w.prev); err != nil {
			return err
		}
	}
	w.row = w.prev.Clone()
	w.row.ID = w.id
	w.now = time.Now().Unix()
	if err := apply(&w.row, w.exists(), w.now, w.f.Jobs); err != nil {
		return err
	}
	w.row = w.row.Clone()
	w.row.ID = w.id
	w.declare()
	if err := w.authorize(ctx); err != nil {
		return err
	}
	if err := w.carryRecord(); err != nil {
		return err
	}
	w.settlePaths()
	if err := w.endReason(); err != nil {
		return err
	}
	w.stamp()
	if err := w.assignTier(ctx); err != nil {
		return err
	}
	w.count()
	w.conclude(ctx)
	return w.place(ctx)
}

// declare gives a declaration the state it implies.
func (w *rowWrite) declare() {
	// Every door into the store declares here, so a record naming no state
	// is stored as declared: the guard reads an empty state as ended.
	if w.kind == asDeclaration && w.row.State == "" {
		w.row.State = types.StateDeclared
	}
	if w.kind == asDeclaration && w.exists() && widensInPlace(w.prev, w.row) {
		w.row.State = w.prev.State
	}
}

// authorize grades the write and stamps the entry an entry declaration adds.
func (w *rowWrite) authorize(ctx context.Context) error {
	w.entering = w.kind == asDeclaration && requestsEntry(w.prev, w.row)
	switch {
	case w.entering:
		if err := authorizeEntry(w.actor, w.id, w.prev, w.row, w.exists(), w.f.Jobs); err != nil {
			return err
		}
		e := &w.row.Entries[len(w.row.Entries)-1]
		e.By, e.At, e.Consumed = trail.StampOrigin(ctx, types.Origin{}), w.now, 0
	case w.kind.graded():
		if err := authorizeRow(w.actor, w.id, w.kind, w.prev, w.row, w.exists(), w.f.Jobs); err != nil {
			return err
		}
	}
	// An ended row declared again is a fork of the job's next attempt.
	w.refork = w.kind == asDeclaration && w.exists() && w.prev.State.Terminal() && w.row.State.Live()
	return nil
}

// carryRecord keeps the store's own record of what HAPPENED, which is not a
// caller's to send.
func (w *rowWrite) carryRecord() error {
	row, prev := &w.row, w.prev
	// An existing row carries it forward, so a whole-row write neither forges
	// nor erases a registration. A row a bound caller creates carries none.
	switch {
	case w.exists():
		if w.kind != asExec {
			row.ReportedBase, row.BaseVerdict, row.Registered = prev.ReportedBase, prev.BaseVerdict, prev.Registered
			row.CheckoutRoot = prev.CheckoutRoot
		}
		// An ended row declared again is handed out afresh: a checkout left
		// on it would refuse every new taker.
		if w.refork {
			if err := w.foldAttempt(); err != nil {
				return err
			}
			row.ReportedBase, row.BaseVerdict, row.Registered, row.CheckoutRoot = "", "", 0, ""
		}
		if w.kind != asObservation {
			row.Unattributed, row.ReleaseRequests = prev.Unattributed, prev.ReleaseRequests
		}
		if w.refork {
			row.ReleaseRequests = nil
		}
		if w.kind != asObservation && !w.entering {
			row.Entries = prev.Entries
		}
	case w.actor.Bound():
		row.ReportedBase, row.BaseVerdict, row.Registered, row.Unattributed = "", "", 0, nil
		row.CheckoutRoot = ""
	}
	return nil
}

// foldAttempt folds the live attempt of the ended row being forked again,
// refusing a fork past the cap.
func (w *rowWrite) foldAttempt() error {
	all, err := w.s.Attempts()
	if err != nil {
		return err
	}
	tries := types.NewJobAttempts(all)
	if err := refuseAttempt(w.id, tries.LiveN(w.id), w.row); err != nil {
		return err
	}
	fold := foldAttempt(w.prev, tries.Live(w.id), tries.Prior(w.id), w.denials, w.denialsKnown, w.now)
	w.folded = &fold
	return nil
}

// settlePaths fixes what a write may not decide of the row's paths and
// requests.
func (w *rowWrite) settlePaths() {
	row, prev := &w.row, w.prev
	if !w.exists() {
		row.Entries, row.ReleaseRequests = nil, nil
	}
	row.ReleaseRequests = heldRequests(*row)
	if w.kind == asDeclaration && w.exists() {
		keepDerived(row, prev)
	}
	// Hardening is one-way: a re-declaration cannot soften a path the holder
	// wrote, and a soft path that is no write path claims nothing.
	row.Paths.Soft = slices.DeleteFunc(row.Paths.Soft, func(p string) bool {
		return !slices.Contains(row.Paths.Write, p) ||
			w.exists() && slices.Contains(prev.Paths.Write, p) && !slices.Contains(prev.Paths.Soft, p)
	})
}

// endReason sets the reason the store writes for an ended row.
func (w *rowWrite) endReason() error {
	row, prev := &w.row, w.prev
	// The store writes every reason: the sweep's, the one a write ending a
	// live row as no_return gets, and a pass its holder's result made. A row
	// brought back to life has none.
	row.EndReason = prev.EndReason
	row.Landed = prev.Landed && !row.State.Live()
	switch {
	case row.State.Live():
		row.EndReason = ""
	case w.exists() && prev.State.Live() && row.State == types.StateNoReturn:
		all, err := w.s.Attempts()
		if err != nil {
			return err
		}
		row.EndReason = endedReason(prev, types.NewJobAttempts(all).Result(w.id) != nil, w.s.endsOwnRow(prev))
	case w.exists() && prev.State.Live() && row.State == types.StatePass && gradedByHolder(prev):
		row.EndReason = returnedReason
	}
	return nil
}

// stamp sets the times and carries what only the store writes.
func (w *rowWrite) stamp() {
	row, prev := &w.row, w.prev
	row.Updated = w.now
	// A wait that refuses records why and leaves the row where it was,
	// heartbeat included: the waiter is not the holder.
	if (w.kind == asObservation || w.kind == asAdvance || w.entering || (w.kind == asVerdict && row.State == prev.State)) && w.exists() {
		row.Updated = prev.Updated
	}
	row.Created = w.now
	// The record keeps what a newer magus stored; see [jobsFile].
	row.Unknown = prev.Unknown
	row.ModelTier = prev.ModelTier
	// Only RecordWorktree, EndWorktrees and Exec rebinding an untaken row
	// write the worktree record. The next attempt keeps a checkout that
	// stands and forgets one magus removed.
	if w.kind != asExec || !rebindsAdopted(prev, *row) {
		row.Worktree = prev.Clone().Worktree
	}
	if w.refork && row.Worktree != nil {
		row.Worktree.Kept = ""
		if row.Worktree.Removed != 0 {
			row.Worktree = nil
		}
	}
}

// assignTier gives a row whose requested model changed its tier, and refuses
// one above its parent's.
func (w *rowWrite) assignTier(ctx context.Context) error {
	row, prev := &w.row, w.prev
	if w.s.modelTiers != nil && (!w.exists() || prev.Agent.RequestedModel() != row.Agent.RequestedModel()) {
		assigned, err := w.s.assignModelTierWithHistory(ctx, *row, w.f.Jobs)
		if err != nil {
			return err
		}
		row.ModelTier = &assigned
	}
	if !w.exists() || prev.Agent.RequestedModel() != row.Agent.RequestedModel() || prev.Parent != row.Parent {
		return refuseAboveParent(w.s.modelTiers, w.s.spawnRule, *row, w.f.Jobs)
	}
	return nil
}

// count carries the store's counters. A whole-row write cannot set them, and
// only a write that adds write paths to a row its holder already took counts
// a widening.
func (w *rowWrite) count() {
	row, prev := &w.row, w.prev
	row.Widenings = prev.Widenings
	if w.kind != asClaim {
		row.Claims = prev.Claims
	}
	if w.kind != asObservation {
		row.ReworkRounds = prev.ReworkRounds
	}
	if w.kind != asNarrowing && w.exists() && (prev.State == types.StateRunning || prev.State == types.StateExited) &&
		slices.ContainsFunc(row.Paths.Write, func(p string) bool { return !slices.Contains(prev.Paths.Write, p) }) {
		row.Widenings++
	}
}

// conclude sets the generation, the evidence, and what the row requires and
// releases. A fork of the next attempt starts the evidence and counters over.
func (w *rowWrite) conclude(ctx context.Context) {
	row, prev := &w.row, w.prev
	// The generation and the evidence are the store's too. Only a wait
	// records a rejection.
	row.Generation = nextGeneration(prev, *row, w.exists(), w.kind)
	if w.kind != asVerdict {
		row.Rejection = prev.Rejection
	}
	if w.refork {
		row.Attempt, row.GateAttempts, row.Rejection = nil, nil, nil
		row.Integration, row.WaiterEvidence = nil, nil
		row.ReworkRounds, row.Widenings, row.Claims = 0, 0, nil
	}
	row.Requires = requiredFeatures(*row)
	if w.kind != asNarrowing {
		row.Releases = w.s.releases(ctx, prev, *row, w.now, prev.Registered != 0 && w.actor.Lease != w.id)
	}
	switch {
	case w.kind == asAdvance:
	case w.kind == asDeclaration && (!w.exists() || w.refork):
		row.Authority = forkAuthority(w.actor, w.f.Jobs, w.rootHead)
	default:
		row.Authority = prev.Authority
	}
}

// place stores the row in the ledger, holding the checkouts of a row that
// just ended.
func (w *rowWrite) place(ctx context.Context) error {
	row, prev := &w.row, w.prev
	if w.exists() {
		row.Created = prev.Created
		row.RegisteredBy = prev.RegisteredBy
		w.f.Jobs[w.i] = *row
	} else {
		row.RegisteredBy = trail.StampOrigin(ctx, types.Origin{})
		w.f.Jobs = append(w.f.Jobs, *row)
	}
	if w.exists() && !prev.State.Terminal() && row.State.Terminal() {
		if err := w.ended.hold(w.s, w.f.Jobs, *row); err != nil {
			return err
		}
	}
	w.stored = row.Clone()
	return nil
}
