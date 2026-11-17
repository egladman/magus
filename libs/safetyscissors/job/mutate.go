package job

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// WriteStage is where in a row write [WithWriteHook]'s hook runs.
type WriteStage int

const (
	// StageAuthorize runs after the store graded the write, before anything else: the
	// hook adds the rules only its caller knows, and an [AsClaim] write is graded here
	// alone. Refusing refuses the write.
	StageAuthorize WriteStage = iota
	// StageRefork runs when an ended row is declared again, the fork of its next attempt.
	// Attempts holds every stored try and Denials the guard's denials of the last holder.
	// The hook may refuse the fork, and sets Fold to the attempt the row ends, which the
	// store appends once the write lands.
	StageRefork
	// StageEnd runs once the store set the row's end reason: the previous one, cleared on a
	// live row. The hook sets the reason a write that ends a live row records.
	StageEnd
	// StageTier runs once the store stamped the row's times: the hook assigns the row's
	// model tier and may refuse a row its parent may not spawn.
	StageTier
	// StagePlaced runs once the row is placed among Rows. The hook may queue work in
	// After, such as recording the outcome of a row that just ended.
	StagePlaced
	// StageDelete runs before [Store.Delete] drops the row, Rows being the rows that stay.
	// Refusing keeps the row; the hook may change it, as a checkout it removed.
	StageDelete
)

// WriteHook is a caller's part of a row write; see [WithWriteHook]. It runs while the
// store's locks are held, so it must not touch the store.
type WriteHook func(ctx context.Context, e *WriteEvent) error

// WriteEvent is one row write as a [WriteHook] sees it at one stage.
type WriteEvent struct {
	Stage WriteStage
	Kind  WriteKind
	ID    string
	Actor Actor
	// Prev is the stored row, the zero Job when Exists is false, and Row the row being
	// written, which the hook may change.
	Prev   Job
	Row    *Job
	Exists bool
	// Refork is an ended row declared again.
	Refork bool
	// Rows are every stored row, which the hook must not change.
	Rows []Job
	// Now is the write's time, in unix seconds.
	Now int64
	// Attempts are the stored tries, set for StageRefork and StageEnd.
	Attempts Attempts
	// Denials and DenialsKnown are the denials of the ended row's last holder, set for
	// StageRefork.
	Denials      []Denial
	DenialsKnown bool
	// Fold is the attempt a StageRefork hook folds the ended row into.
	Fold *Attempt
	// After runs once the write has landed and the store's locks are released. An error
	// from one is the write's, though the row stays written.
	After []func(context.Context) error
}

// Mutate is the locked read-modify-write every row write shares, and the only place a row
// is written row-wise. kind says what the write is; see [WriteKind]. apply may fail, and
// nothing is written then.
func (s *Store) Mutate(ctx context.Context, id string, kind WriteKind, apply func(cur *Job, exists bool, now int64) error) (Job, error) {
	return s.MutateSeeing(ctx, id, kind, func(cur *Job, exists bool, now int64, _ []Job) error {
		return apply(cur, exists, now)
	})
}

// MutateSeeing is Mutate whose apply also sees every stored row.
// The rows are read under the same lock; apply must not change them.
func (s *Store) MutateSeeing(ctx context.Context, id string, kind WriteKind, apply func(cur *Job, exists bool, now int64, rows []Job) error) (Job, error) {
	if strings.TrimSpace(id) == "" {
		return Job{}, ErrNoID
	}
	w := &rowWrite{s: s, id: id, kind: kind}
	// Read before the lock: the record may be another checkout's, and only a declaration
	// of an ended row folds it.
	if kind == AsDeclaration {
		w.denials, w.denialsKnown = s.endedAttemptDenials(ctx, id)
	}
	s.mu.Lock()
	w.actor = s.Actor()
	w.head = sync.OnceValue(func() string {
		if s.head == nil {
			return ""
		}
		return s.head(ctx)
	})
	err := s.rewrite(ctx, func(f *Rows) error {
		w.f = f
		return w.run(ctx, apply)
	})
	s.mu.Unlock()
	if err != nil {
		return Job{}, err
	}
	// After the row, so a refused write folds nothing.
	if w.folded != nil {
		if err := s.AppendAttempts(ctx, *w.folded); err != nil {
			return w.stored, fmt.Errorf("%s was forked again, and its last attempt was not folded: %w", id, err)
		}
	}
	for _, after := range w.after {
		if err := after(ctx); err != nil {
			return w.stored, err
		}
	}
	return w.stored, nil
}

// rowWrite is one locked write of a row: what the steps of [Store.MutateSeeing] pass
// between them, in the order run calls them.
type rowWrite struct {
	s            *Store
	id           string
	kind         WriteKind
	actor        Actor
	denials      []Denial
	denialsKnown bool
	head         func() string

	f    *Rows
	i    int // the row's place in f.Jobs, -1 when it is new
	prev Job
	row  Job
	now  int64
	// entering is a declaration whose only change is one entry. refork is an
	// ended row declared again.
	entering, refork bool

	folded *Attempt
	after  []func(context.Context) error
	stored Job
}

func (w *rowWrite) exists() bool { return w.i >= 0 }

// event is the write at stage, as a hook sees it.
func (w *rowWrite) event(stage WriteStage) WriteEvent {
	return WriteEvent{
		Stage: stage, Kind: w.kind, ID: w.id, Actor: w.actor, Prev: w.prev, Row: &w.row,
		Exists: w.exists(), Refork: w.refork, Rows: w.f.Jobs, Now: w.now,
	}
}

// hook runs the store's hook over e, keeping what it queued.
func (w *rowWrite) hook(ctx context.Context, e *WriteEvent) error {
	if w.s.hook == nil {
		return nil
	}
	if err := w.s.hook(ctx, e); err != nil {
		return err
	}
	w.after = append(w.after, e.After...)
	return nil
}

// run is the write over w.f, under both locks.
func (w *rowWrite) run(ctx context.Context, apply func(cur *Job, exists bool, now int64, rows []Job) error) error {
	w.folded, w.after = nil, nil
	w.i = slices.IndexFunc(w.f.Jobs, func(e Job) bool { return e.ID == w.id })
	w.prev = Job{}
	if w.exists() {
		w.prev = w.f.Jobs[w.i]
		if err := Unwritable(w.prev); err != nil {
			return w.s.wrap(err)
		}
	}
	w.row = w.prev.Clone()
	w.row.ID = w.id
	w.now = w.s.Now().Unix()
	if err := apply(&w.row, w.exists(), w.now, w.f.Jobs); err != nil {
		return err
	}
	w.row = w.row.Clone()
	w.row.ID = w.id
	w.declare()
	if err := w.authorize(ctx); err != nil {
		return err
	}
	if err := w.carryRecord(ctx); err != nil {
		return err
	}
	w.settlePaths()
	if err := w.endReason(ctx); err != nil {
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
	// is stored as declared: an empty state reads as ended.
	if w.kind == AsDeclaration && w.row.State == "" {
		w.row.State = StateDeclared
	}
	if w.kind == AsDeclaration && w.exists() && widensInPlace(w.prev, w.row) {
		w.row.State = w.prev.State
	}
}

// authorize grades the write and stamps the entry an entry declaration adds.
func (w *rowWrite) authorize(ctx context.Context) error {
	w.entering = w.kind == AsDeclaration && requestsEntry(w.prev, w.row)
	switch {
	case w.entering:
		if err := authorizeEntry(w.actor, w.id, w.prev, w.row, w.exists(), w.f.Jobs); err != nil {
			return w.s.wrap(err)
		}
		e := &w.row.Entries[len(w.row.Entries)-1]
		e.By, e.At, e.Consumed = w.s.originOf(ctx), w.now, 0
	case w.kind == AsClaim && w.s.hook != nil:
		// Only the caller's claim rule grades a claim; see [AsClaim].
	case w.kind.Graded():
		if err := AuthorizeRow(w.actor, w.id, w.kind, w.prev, w.row, w.exists(), w.f.Jobs); err != nil {
			return w.s.wrap(err)
		}
	}
	// An ended row declared again is a fork of the job's next attempt.
	w.refork = w.kind == AsDeclaration && w.exists() && w.prev.State.Terminal() && w.row.State.Live()
	if !w.entering && w.kind.Graded() {
		e := w.event(StageAuthorize)
		if err := w.hook(ctx, &e); err != nil {
			return w.s.wrap(err)
		}
	}
	return nil
}

// carryRecord keeps the store's own record of what HAPPENED, which is not a
// caller's to send.
func (w *rowWrite) carryRecord(ctx context.Context) error {
	row, prev := &w.row, w.prev
	// An existing row carries it forward, so a whole-row write neither forges
	// nor erases a registration. A row a bound caller creates carries none.
	switch {
	case w.exists():
		if w.kind != AsExec {
			row.ReportedBase, row.BaseVerdict, row.Registered = prev.ReportedBase, prev.BaseVerdict, prev.Registered
			row.CheckoutRoot = prev.CheckoutRoot
		}
		// An ended row declared again is handed out afresh: a checkout left
		// on it would refuse every new taker.
		if w.refork {
			if err := w.fold(ctx); err != nil {
				return err
			}
			row.ReportedBase, row.BaseVerdict, row.Registered, row.CheckoutRoot = "", "", 0, ""
		}
		if w.kind != AsObservation {
			row.Unattributed, row.ReleaseRequests = prev.Unattributed, prev.ReleaseRequests
		}
		if w.refork {
			row.ReleaseRequests = nil
		}
		if w.kind != AsObservation && !w.entering {
			row.Entries = prev.Entries
		}
	case w.actor.Bound():
		row.ReportedBase, row.BaseVerdict, row.Registered, row.Unattributed = "", "", 0, nil
		row.CheckoutRoot = ""
	}
	return nil
}

// fold puts the ended row being forked again to the [StageRefork] hook, which folds its
// live attempt and may refuse a fork past the cap. With no hook the attempt is not folded,
// and a fork past max_attempts is refused.
func (w *rowWrite) fold(ctx context.Context) error {
	all, err := w.s.Attempts(ctx)
	if err != nil {
		return err
	}
	tries := NewAttempts(all)
	if w.s.hook == nil {
		if used := tries.LiveN(w.id); w.row.MaxAttempts > 0 && used >= w.row.MaxAttempts {
			return fmt.Errorf("%w: %s used %d of its %d attempt(s)", ErrMaxAttempts, w.id, used, w.row.MaxAttempts)
		}
		return nil
	}
	e := w.event(StageRefork)
	e.Attempts, e.Denials, e.DenialsKnown = tries, w.denials, w.denialsKnown
	if err := w.hook(ctx, &e); err != nil {
		return err
	}
	w.folded = e.Fold
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
	if w.kind == AsDeclaration && w.exists() {
		keepDerived(row, prev)
	}
	// Hardening is one-way: a re-declaration cannot soften a path the holder
	// wrote, and a soft path that is no write path claims nothing.
	row.Paths.Soft = slices.DeleteFunc(row.Paths.Soft, func(p string) bool {
		return !slices.Contains(row.Paths.Write, p) ||
			w.exists() && slices.Contains(prev.Paths.Write, p) && !slices.Contains(prev.Paths.Soft, p)
	})
}

// endReason sets the reason the store writes for an ended row: the stored one, none on a
// live row, and whatever the [StageEnd] hook writes for a write that ends a live row.
func (w *rowWrite) endReason(ctx context.Context) error {
	row, prev := &w.row, w.prev
	row.EndReason = prev.EndReason
	row.Landed = prev.Landed && !row.State.Live()
	if row.State.Live() {
		row.EndReason = ""
		return nil
	}
	if !w.exists() || !prev.State.Live() || w.s.hook == nil {
		return nil
	}
	all, err := w.s.Attempts(ctx)
	if err != nil {
		return err
	}
	e := w.event(StageEnd)
	e.Attempts = NewAttempts(all)
	return w.hook(ctx, &e)
}

// stamp sets the times and carries what only the store writes.
func (w *rowWrite) stamp() {
	row, prev := &w.row, w.prev
	row.Updated = w.now
	// A wait that refuses records why and leaves the row where it was,
	// heartbeat included: the waiter is not the holder.
	if (w.kind == AsObservation || w.kind == AsAdvance || w.entering || (w.kind == AsVerdict && row.State == prev.State)) && w.exists() {
		row.Updated = prev.Updated
	}
	row.Created = w.now
	// The record keeps what a newer writer stored; see [Rows].
	row.Unknown = prev.Unknown
	row.ModelTier = prev.ModelTier
	// Only an exec rebinding an untaken row writes the worktree record. The next attempt
	// keeps a checkout that stands and forgets one that was removed.
	if w.kind != AsExec || !rebindsAdopted(prev, *row) {
		row.Worktree = prev.Clone().Worktree
	}
	if w.refork && row.Worktree != nil {
		row.Worktree.Kept = ""
		if row.Worktree.Removed != 0 {
			row.Worktree = nil
		}
	}
}

// assignTier puts the row to the [StageTier] hook, which assigns its model tier.
func (w *rowWrite) assignTier(ctx context.Context) error {
	e := w.event(StageTier)
	return w.hook(ctx, &e)
}

// count carries the store's counters. A whole-row write cannot set them, and
// only a write that adds write paths to a row its holder already took counts
// a widening.
func (w *rowWrite) count() {
	row, prev := &w.row, w.prev
	row.Widenings = prev.Widenings
	if w.kind != AsClaim {
		row.Claims = prev.Claims
	}
	if w.kind != AsObservation {
		row.ReworkRounds = prev.ReworkRounds
	}
	if w.kind != AsNarrowing && w.exists() && (prev.State == StateRunning || prev.State == StateExited) &&
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
	if w.kind != AsVerdict {
		row.Rejection = prev.Rejection
	}
	if w.refork {
		row.Attempt, row.GateAttempts, row.Rejection = nil, nil, nil
		row.Integration, row.WaiterEvidence = nil, nil
		row.ReworkRounds, row.Widenings, row.Claims = 0, 0, nil
	}
	row.Requires = requiredFeatures(*row)
	if w.kind != AsNarrowing {
		row.Releases = w.s.Releases(ctx, prev, *row, w.now, prev.Registered != 0 && w.actor.Lease != w.id)
	}
	switch {
	case w.kind == AsAdvance:
	case w.kind == AsDeclaration && (!w.exists() || w.refork):
		row.Authority = forkAuthority(w.actor, w.f.Jobs, w.head)
	default:
		row.Authority = prev.Authority
	}
}

// place stores the row in the ledger.
func (w *rowWrite) place(ctx context.Context) error {
	row, prev := &w.row, w.prev
	if w.exists() {
		row.Created = prev.Created
		row.RegisteredBy = prev.RegisteredBy
		w.f.Jobs[w.i] = *row
	} else {
		row.RegisteredBy = w.s.originOf(ctx)
		w.f.Jobs = append(w.f.Jobs, *row)
	}
	e := w.event(StagePlaced)
	if err := w.hook(ctx, &e); err != nil {
		return err
	}
	w.stored = row.Clone()
	return nil
}

// nextGeneration is row's Generation once written over prev: 1 for a new row, and one more
// than prev's for a write that changes the declared spec ([SpecChanges]). An observation
// declares nothing, and neither does a write that only widens the write paths: evidence
// taken under the narrower set still asks what the wider one asks.
func nextGeneration(prev, row Job, exists bool, kind WriteKind) int {
	if !exists {
		return 1
	}
	generation := max(prev.Generation, 1)
	if kind != AsObservation && changesSpec(prev, row) {
		generation++
	}
	return generation
}

// changesSpec reports whether row declares anything prev did not, other than write paths
// that each hold every path prev held.
func changesSpec(prev, row Job) bool {
	changes := SpecChanges(prev, row)
	if len(changes) == 1 && changes[0] == "paths.write" {
		return !widens(prev.Paths.Write, row.Paths.Write)
	}
	return len(changes) > 0
}

// widens reports whether every path in held lies within some path of next.
func widens(held, next []string) bool {
	return !slices.ContainsFunc(held, func(h string) bool {
		return !slices.ContainsFunc(next, func(n string) bool { return PathWithin(h, n) })
	})
}

// rebindsAdopted reports whether row moves prev, which nobody took, from one checkout its
// fork adopted to another adopted one.
func rebindsAdopted(prev, row Job) bool {
	return prev.CheckoutRoot == "" && prev.Worktree.Standing() && !prev.Worktree.Made &&
		row.Worktree.Standing() && !row.Worktree.Made && !samePath(prev.Worktree.Root, row.Worktree.Root)
}

// samePath compares two absolute paths by where they land, symlinks resolved. A copy of
// the checkout layer's, which this package does not import.
func samePath(a, b string) bool {
	return a != "" && b != "" && resolvedPath(a) == resolvedPath(b)
}

func resolvedPath(p string) string {
	return cmp.Or(evalSymlinks(p), filepath.Clean(p))
}

func evalSymlinks(p string) string {
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return ""
	}
	return r
}
