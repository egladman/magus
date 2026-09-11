// Package ledger persists the lease ledger: the rows an orchestrating agent
// declares about the plan it is running, kept where a human can read them.
//
// IT GATES NO RUN AND BLOCKS NO WRITE. The AGENT GUARD is what consults these rows to
// grade one, and it is a separate thing that READS this store. Register computes a verdict
// (whether a worker's reported base is the checkpoint its lease was handed), and that is a
// fact recorded on the row and handed back, not a gate: the registration succeeds either
// way and what to do about a divergence is the orchestrator's call. The store's whole job
// is that a plan an agent stated in a prompt stops being trapped in one session's
// transcript.
//
// THE ONE THING IT DOES REFUSE IS A WRITE TO SOMEBODY ELSE'S ROW, and that is not the
// same kind of rule. See [authorizeRow]: a worker acting under a lease may release paths
// and end itself, and it is refused the writes that would widen its own boundary or
// rewrite the plan. The reason it lives here rather than in a guard rule is that three
// doors reach this store and a command pattern can only see one of them. Everything else
// stays outside for the reason types.Lease gives: a store that refused what an
// orchestrator declares would make the ledger something agents route around.
//
// ONE PLAN PER REPOSITORY. Clear wipes the rows so the next plan starts empty, after
// copying them to a timestamped sibling file. Nothing reads those archives back: they
// exist so a plan wiped by somebody else is still legible to a person, not as a history
// this package models. Naming a plan is still outside the vocabulary.
//
// "LEDGER" NAMES THE RECONCILIATION, NOT THE DURABILITY, and the difference is worth
// stating because the word oversells one of them. A financial ledger is append-only and
// historical; this is neither: Put upserts a row in place, Clear wipes the book, and
// nothing is archived. What it does share is the part that earns the name: it is written
// to be checked AGAINST reality later, which is exactly the skill's "compare the ledger
// against the actual diff since each lease's checkpoint" step. Read it as a book of
// declared intent kept for reconciliation, not as a durable record of what happened. The
// vocabulary came from the magus-multi-agent skill, which is also where the row shape is
// defined (see types.Lease).
//
// It is the INTENT layer of three, and naming the other two is what keeps them apart;
// they are flat stores joined by lease id at render time, never a storage hierarchy:
//
//   - intent: this package. What an orchestrating agent SAID it would hand out.
//     Declared up front, mutable, one plan at a time.
//   - actions: internal/trail. What was actually DONE against the daemon, append-only.
//     The closest sibling, and the one to reach for when the question is "did it happen"
//     rather than "was it planned".
//   - effects: the run itself: the pool, the locks, the outputs a target produced.
//
// The two stores that sound related and are NOT: internal/journal is the event stream of
// one magus invocation (what a build executed), and internal/memory and internal/notes
// are prose a human or an agent writes to be read later; neither models leased work.
package ledger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/repoid"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// ErrNoID reports a lease with no id. The id is what Put upserts on, so a row without
// one could never be updated or referred to again: it is unaddressable, not merely
// incomplete.
var ErrNoID = errors.New("ledger: a lease needs an id")

// Store is the repository's lease ledger, a single JSON file in the per-repository state
// directory. Every operation reads the file, acts, and writes it back, so a Store is
// cheap to construct and holds no state between calls beyond the path and its lock.
//
// TWO locks, because there are two kinds of concurrent writer and neither lock sees the
// other's:
//
//   - The mutex serializes writers within one process while they share a Store, which is
//     why the daemon builds exactly one and hands it to both of its doors (the
//     magus_ledger MCP tool and the console's read route).
//   - An OS file lock beside leases.json serializes writers across PROCESSES. The CLI, the
//     daemon, and an MCP client each hold their own Store on the same file, from any
//     worktree or clone of the repository, and workers now register and heartbeat against
//     it, so "one orchestrating agent writes this"
//     (the assumption that made a cross-process race acceptable) stopped being true. Two
//     read-modify-writes that interleave drop whichever row the loser had not read.
//
// Both are taken for the span of ONE call. A caller that merges fields by reading with
// List and writing back with Put takes them twice, and two such merges on one id then
// lose whichever field the second one read before the first wrote. [Store.Update] is that
// merge done under a single acquisition, and it is what a field-at-a-time writer (the
// magus_ledger MCP tool) has to use.
//
// READS take neither. write replaces the file by rename, so a reader either sees the
// whole previous ledger or the whole next one; blocking List behind a writer in another
// process would buy nothing and would let a wedged writer stall the console.
type Store struct {
	mu   sync.Mutex
	path string
	// err is the failure to work out where this ledger lives, kept so NewStore can stay
	// a constructor while every operation still reports it. A Store that cannot name its
	// file must not silently read as an empty ledger: absent rows are how the guard
	// decides nothing is leased.
	err  error
	root string
	// actor pins who this Store writes as; nil resolves the acting party at every write
	// from cacheDir and the environment. The daemon builds ONE Store at startup and serves
	// every magus_ledger caller from it, so an actor frozen at construction grades all of
	// them as whoever started the process.
	actor    *Actor
	cacheDir string
}

// Location is where a Store lives: the repository whose rows these are, and the state
// base and legacy cache directory that place the file.
//
// A struct rather than positional params because the paths are transposable at every
// call site and nothing downstream would notice: a ledger written into the workspace and
// digested against the state dir reads as an ordinary empty ledger.
type Location struct {
	// CacheDir is the workspace's cache directory (magus.CacheDir), which is where the
	// ledger USED to live. It is read only to adopt <CacheDir>/ledger on first use;
	// nothing is written there any more. Empty skips the adoption.
	CacheDir string
	// Root is the checkout this Store was opened from. It answers two questions: which
	// REPOSITORY owns the rows (through repoid, which folds every worktree and clone of
	// one repo onto a single directory), and what a row's paths are relative to when a
	// release is digested (see Update). A Store built with an empty root still records
	// releases; it just cannot say what was in them.
	Root string
	// StateBase overrides the user state directory the ledger resolves under. Empty
	// means config.UserStateDir, which is what every caller outside a test wants.
	StateBase string
	// Actor is who the Store writes as. A pointer because the zero Actor is UNBOUND and
	// is a real value: nil is the only way to say "resolve it at every write, from the
	// environment and CacheDir through [ActingActor]", which is what every door outside a
	// test wants.
	Actor *Actor
}

// NewStore returns the ledger for loc's repository, adopting the legacy cache-dir
// location on the way past. The leases file itself is created by the first Put.
//
// THE CACHE DIRECTORY IS NO LONGER THE HOME, and that is the whole point of this
// resolution: a cache dir belongs to one CHECKOUT, so an orchestrator's rows in one
// worktree were invisible to a worker in another, and a lease-scoped guard rule could
// not bind across the two. The rows describe a repository's plan, so they key on
// repository identity exactly as internal/sessions and internal/memory do.
//
// A resolution failure is held rather than returned: every operation reports it, so a
// caller cannot mistake an unplaceable ledger for an empty one.
func NewStore(loc Location) *Store {
	s := &Store{root: loc.Root, actor: loc.Actor, cacheDir: loc.CacheDir}
	s.path, s.err = leasesPath(loc)
	return s
}

// Actor is who this Store writes as RIGHT NOW, for a door that has to refuse before it
// computes anything: `ledger accept` grades a report and must say a worker cannot grade
// its own row before it reads one. Resolved per call unless Location pinned one, so a
// checkout that binds a lease is graded from its next write.
func (s *Store) Actor() Actor {
	if s.actor != nil {
		return *s.actor
	}
	return ActingActor(s.cacheDir)
}

// leasesPath places the leases file: <XDG state>/magus/ledger/<repo>/leases.json, after
// carrying forward whatever an older magus left at <CacheDir>/ledger.
func leasesPath(loc Location) (string, error) {
	base := loc.StateBase
	if base == "" {
		var err error
		if base, err = config.UserStateDir(); err != nil {
			return "", fmt.Errorf("ledger: resolve state dir: %w (set XDG_STATE_HOME to a writable absolute path)", err)
		}
	}
	dir, err := repoid.StateDir(base, "ledger", loc.Root)
	if err != nil {
		return "", fmt.Errorf("ledger: %w", err)
	}
	// compat(until: no checkout's cache dir still holds a ledger/ directory; observe
	// with `find ~ -path '*/.magus/cache/ledger' -maxdepth 6` returning nothing):
	// carries forward the rows an older magus kept per checkout.
	if loc.CacheDir != "" {
		if err := repoid.Adopt(filepath.Join(loc.CacheDir, "ledger"), dir); err != nil {
			return "", fmt.Errorf("ledger: %w", err)
		}
	}
	return filepath.Join(dir, "leases.json"), nil
}

// Path is the leases file this Store reads and writes, for a reader that has to name it:
// a person asking where their plan is kept, or a test planting one. The file itself may
// not exist yet, which is an empty ledger and not an error.
func (s *Store) Path() (string, error) { return s.path, s.err }

// ledgerFile is the on-disk envelope. An object rather than a bare array so a later
// field (a plan identity, a schema version) can be added without every existing reader
// failing to parse the file.
type ledgerFile struct {
	Leases []types.Lease `json:"leases"`
}

// Put records one lease, replacing any row with the same id IN PLACE. Position is
// preserved on update because the ledger is a table a person reads top to bottom, and
// a row that jumped to the bottom every time its state changed would reorder itself
// exactly when it is being watched.
//
// It stamps Created on the first write and Updated on every write, ignoring whatever
// the caller passed for either. The stored row is returned.
func (s *Store) Put(ctx context.Context, u types.Lease) (types.Lease, error) {
	return s.Update(ctx, u.ID, func(cur *types.Lease) { *cur = u })
}

// Update applies apply to the row with this id and writes the result back, all while
// holding both of the Store's locks ONCE. That is the whole point: a merge spread across
// List and Put releases them in between, so two concurrent writers advancing different
// fields of one row each read it before the other wrote, and the second write reverts the
// first, whether the two are goroutines or separate magus processes.
//
// The row is CREATED when absent, matching Put: apply then sees a zero lease carrying
// only the id, so declaring a lease and advancing one are the same call. Created is
// preserved from the stored row and Updated is stamped on every write, exactly as Put
// does, and the id is the key: whatever apply writes into ID is overwritten with it.
//
// Releases are stamped here too, and for the same reason Created is: they are the
// store's to say, not the caller's. A write that drops a path from OwnedPaths IS the
// release announcement the skill has workers make when they finish editing a contested
// path, so the dropped paths are digested and recorded on the row, under this same
// lock, because reading the previous owned set and writing the next one has to be one
// step or a concurrent put decides which release happened.
//
// apply runs while the lock is held, so it must not touch the store, and it cannot fail:
// anything that could be rejected (an unknown state, a mistyped param) belongs in the
// caller, before the call.
//
// ctx reaches the release digests and nothing else. A cancelled call still WRITES the
// row (the merge is already done and abandoning it would lose the state change), but it
// stops hashing files, so a caller that walked away does not keep the store's lock while
// the disk is read.
func (s *Store) Update(ctx context.Context, id string, apply func(*types.Lease)) (types.Lease, error) {
	return s.mutate(ctx, id, asDeclaration, func(cur *types.Lease, _ bool, _ int64) error {
		apply(cur)
		return nil
	})
}

// MaxUnattributedWrites bounds how many outside-written paths one row keeps.
//
// A cap rather than a growing list because this is written from the guard, which runs on every
// file write a host makes: without one, a plan left declared over a long editing session would
// accumulate a row entry per save until the ledger was mostly this. The newest are kept, since a
// lease asking what moved is asking about the tree it faces now.
const MaxUnattributedWrites = 32

// RecordUnattributedWrite notes that somebody outside lease id wrote one of its owned paths,
// with the content they left behind.
//
// ONE ROW PER PATH, newest wins. A person saves a file a dozen times while an agent works; the
// twelfth save is the only one that describes the tree, and eleven superseded digests would bury
// it. The timestamp moves with the digest, so "when did this last move" stays answerable.
//
// Recording from the guard is a deliberate softening of this package's split (the ledger records,
// the guard enforces), and it survives the rule because what lands here is an OBSERVATION and
// never a verdict. The guard already read these boundaries to grade the write; it simply discarded
// what it saw afterwards, leaving the one party who needed it uninformed.
//
// A missing row is not an error: the lease may have ended between the grading and this call,
// and a write graded against a plan that has since finished is nothing to report to anybody.
func (s *Store) RecordUnattributedWrite(ctx context.Context, id, path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	// An OBSERVATION, and the only ungraded write here: the row it lands on is by
	// definition somebody else's, so grading it would throw the notice away precisely when
	// it matters, which is a worker writing a path another lease holds.
	_, err := s.mutate(ctx, id, asObservation, func(cur *types.Lease, exists bool, now int64) error {
		if !exists {
			return fmt.Errorf("ledger: no such lease %q: %w", id, errNoSuchRow)
		}
		next := make([]types.LeaseUnattributedWrite, 0, len(cur.Unattributed)+1)
		for _, w := range cur.Unattributed {
			if w.Path != path {
				next = append(next, w)
			}
		}
		next = append(next, types.LeaseUnattributedWrite{
			Path: path, Digest: s.digest(ctx, path), At: now,
		})
		if len(next) > MaxUnattributedWrites {
			next = next[len(next)-MaxUnattributedWrites:]
		}
		cur.Unattributed = next
		return nil
	})
	if errors.Is(err, errNoSuchRow) {
		return nil
	}
	return err
}

// errNoSuchRow is internal to RecordUnattributedWrite: mutate creates a row that is not there, and
// this is how the apply func declines that without inventing a lease nobody declared.
var errNoSuchRow = errors.New("ledger: no such lease")

// mutate is the locked read-modify-write [Store.Update] and [Store.Register] share, and
// the only place leases.json is rewritten row-wise.
//
// apply gets two things Update's caller does not need and Register's cannot do without.
// exists says whether a row was already there, which is the difference between the two
// doors: Update creates, Register refuses (a worker registering an id nobody declared was
// handed the wrong id, and inventing a row would bury that). now is the one clock read
// this write is stamped from, so a row cannot claim it registered a second before or
// after it was updated.
//
// apply may fail, which is what lets that refusal be decided where it has to be: under
// the lock, after the row is known present or absent. Nothing is written when it does.
//
// kind says what the write is; see [grading].
func (s *Store) mutate(ctx context.Context, id string, kind grading, apply func(cur *types.Lease, exists bool, now int64) error) (types.Lease, error) {
	if strings.TrimSpace(id) == "" {
		return types.Lease{}, ErrNoID
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	actor := s.Actor()
	var stored types.Lease
	err := s.withFileLock(ctx, func() error {
		f, err := s.read()
		if err != nil {
			return err
		}
		i := slices.IndexFunc(f.Leases, func(e types.Lease) bool { return e.ID == id })
		var prev types.Lease
		if i >= 0 {
			prev = f.Leases[i]
		}
		u := prev.Clone()
		u.ID = id
		now := time.Now().Unix()
		if aerr := apply(&u, i >= 0, now); aerr != nil {
			return aerr
		}
		u = u.Clone()
		u.ID = id
		if kind.graded() {
			if aerr := authorizeRow(actor, id, prev, u, i >= 0, f.Leases); aerr != nil {
				return aerr
			}
		}
		// The store's own record of what HAPPENED is not a caller's to send. On an
		// existing row it is carried forward, so a whole-row write neither forges nor
		// erases a registration; a row a BOUND caller creates carries none, since a child
		// handed a registration it never made writes without ever reporting its base.
		switch {
		case i >= 0:
			if kind != asRegistration {
				u.ReportedBase, u.BaseVerdict, u.Registered = prev.ReportedBase, prev.BaseVerdict, prev.Registered
			}
			if kind != asObservation {
				u.Unattributed = prev.Unattributed
			}
		case actor.Bound():
			u.ReportedBase, u.BaseVerdict, u.Registered, u.Unattributed = "", "", 0, nil
		}
		u.Updated = now
		u.Created = now
		u.SchemaVersion = types.LeaseSchemaVersion
		u.Releases = s.releases(ctx, prev, u, now)
		if i >= 0 {
			u.Created = prev.Created
			u.RegisteredBy = prev.RegisteredBy
			f.Leases[i] = u
		} else {
			u.RegisteredBy = actor.leaseActor()
			f.Leases = append(f.Leases, u)
		}
		if werr := s.write(f); werr != nil {
			return werr
		}
		stored = u.Clone()
		return nil
	})
	if err != nil {
		return types.Lease{}, err
	}
	return stored, nil
}

// List returns every row in the order it was first recorded. The rows are copies, so a
// caller may keep or mutate them without reaching back into the file's next read.
func (s *Store) List() ([]types.Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := s.read()
	if err != nil {
		return nil, err
	}
	out := make([]types.Lease, len(f.Leases))
	for i, u := range f.Leases {
		out[i] = u.Clone()
	}
	return out, nil
}

// Clear drops every row, which is how a fresh plan starts, ARCHIVING what it dropped to a
// timestamped sibling file first, and returns how many rows it dropped. Clearing an empty
// or absent ledger is not an error and archives nothing: the caller asked for an empty
// ledger and got one.
//
// The COUNT comes from inside the lock. Both doors report it, and a preceding List to
// derive it undercounts a row a concurrent registrant appended between the two calls,
// which is the one row its author most needs to hear about.
//
// The archive is what makes this recoverable rather than merely reported: one clear wipes
// rows the caller did not write, and nothing else would let them read the plan again.
// Nothing reads these files back, which is the point: they are for the person who has to
// work out what the plan was.
//
// A bound worker is refused: see [authorizeClear]. ctx bounds the wait for the lock and
// nothing else.
func (s *Store) Clear(ctx context.Context) (int, error) {
	if err := authorizeClear(s.Actor()); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var dropped int
	err := s.withFileLock(ctx, func() error {
		f, err := s.read()
		if err != nil {
			return err
		}
		if len(f.Leases) > 0 {
			if err := s.archive(f); err != nil {
				return err
			}
		}
		dropped = len(f.Leases)
		return s.write(ledgerFile{})
	})
	if err != nil {
		return 0, err
	}
	return dropped, nil
}

// archive writes the rows a clear is about to drop beside the ledger, named for the
// moment they were dropped. A second clear within the same second overwrites the first
// archive rather than growing a suffix scheme: two plans wiped in one second is one
// mistake being repeated, and the rows worth keeping are the ones there now.
func (s *Store) archive(f ledgerFile) error {
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	name := "leases-" + time.Now().UTC().Format("20060102T150405Z") + ".json"
	return file.WriteFileAtomic(filepath.Join(filepath.Dir(s.path), name), append(raw, '\n'), 0o644)
}

// releases carries the row's recorded releases forward and adds the paths this write
// gave up, the ones prev owned and next does not.
//
// A path that is owned again drops OUT of the list: a row saying it both owns and has
// released the same path tells a reader nothing they can act on. A path released twice
// keeps its position and takes the NEWER digest, because the version the next agent
// inherits is the one left behind last.
func (s *Store) releases(ctx context.Context, prev, next types.Lease, now int64) []types.LeaseRelease {
	out := slices.DeleteFunc(slices.Clone(prev.Releases), func(r types.LeaseRelease) bool {
		return slices.Contains(next.OwnedPaths, r.Path)
	})
	for _, p := range prev.OwnedPaths {
		if slices.Contains(next.OwnedPaths, p) {
			continue
		}
		rel := types.LeaseRelease{Path: p, Digest: s.digest(ctx, p), ReleasedAt: now}
		if at := slices.IndexFunc(out, func(r types.LeaseRelease) bool { return r.Path == p }); at >= 0 {
			out[at] = rel
			continue
		}
		out = append(out, rel)
	}
	return out
}

// digest identifies the content at a released path, resolved inside the workspace.
// Computed here rather than passed in: a worker announcing a release should not have to
// hash anything, and a digest the releaser supplied would describe the tree it believed
// it left rather than the one it did.
//
// Everything that is not a readable file inside the root answers with one of the three
// documented markers rather than a hash; types.DigestAbsent, types.DigestDir and
// types.DigestUnreadable say which. The absent/unreadable split matters to the next
// agent: "the releaser deleted it" and "something is there nobody could read" are
// different problems. A path escaping the root is absent by the same rule: the ledger
// describes this workspace, so a row is never handed a digest of something outside it.
//
// Runs under the store's mutex, which is deliberate (reading the previous owned set and
// recording what it gave up has to be one step), and is why every branch below is bounded.
func (s *Store) digest(ctx context.Context, declared string) string {
	if s.root == "" {
		return types.DigestAbsent
	}
	if ctx.Err() != nil {
		return types.DigestUnreadable
	}
	// Symlinks are resolved on BOTH sides before the containment check. A link inside
	// the root pointing outside it was followed and hashed, which is the one thing this
	// function promises not to do; and the root is resolved too, or a workspace reached
	// through a symlinked parent (/tmp on macOS) would read as an escape from itself.
	root, err := filepath.EvalSymlinks(s.root)
	if err != nil {
		return types.DigestAbsent
	}
	full, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(declared)))
	if err != nil {
		// Nothing resolves at the path: a deleted file, a broken link, or a declared
		// glob, which is a pattern rather than a path.
		return types.DigestAbsent
	}
	rel, err := filepath.Rel(root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return types.DigestAbsent
	}
	info, err := os.Stat(full)
	switch {
	case err != nil:
		return types.DigestUnreadable
	case info.IsDir():
		return types.DigestDir
	case !info.Mode().IsRegular():
		// A fifo, socket, or device. os.Open on a released named pipe BLOCKS until
		// somebody writes to it, and it would block holding the store's mutex: one
		// released fifo would wedge every ledger operation in the daemon.
		return types.DigestUnreadable
	case info.Size() > maxDigestBytes:
		return types.DigestUnreadable
	}
	fh, err := os.Open(full)
	if err != nil {
		return types.DigestUnreadable
	}
	defer fh.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, fh); err != nil {
		return types.DigestUnreadable
	}
	return "sha256:" + hex.EncodeToString(sum.Sum(nil))
}

// maxDigestBytes bounds one release digest, because the hash is computed while the store
// holds its mutex. 32 MiB is far above the source files a lease actually releases and far
// below the build artifact that would otherwise stall every other ledger caller for as
// long as it takes to read it; a path over the cap records unreadable, which is what it
// is from the reader's side.
const maxDigestBytes = 32 << 20

// read loads the file. An absent file is an empty ledger, not a failure: nothing has
// been recorded yet for this repository.
func (s *Store) read() (ledgerFile, error) {
	if s.err != nil {
		return ledgerFile{}, s.err
	}
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return ledgerFile{}, nil
	}
	if err != nil {
		return ledgerFile{}, err
	}
	var f ledgerFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return ledgerFile{}, err
	}
	// A row this binary cannot read whole stops every operation, not just the read of that
	// row: mutate rewrites EVERY row in the file, so one unrelated put would silently drop
	// whatever a newer magus recorded across the whole plan.
	for _, row := range f.Leases {
		if row.SchemaVersion > types.LeaseSchemaVersion {
			return ledgerFile{}, fmt.Errorf("ledger: row %s in %s is schema_version %d and this magus accepts version %d only."+
				" A newer ledger is not readable by an older magus; update magus", row.ID, s.path, row.SchemaVersion, types.LeaseSchemaVersion)
		}
	}
	return f, nil
}

// withFileLock runs fn while this process holds the ledger's exclusive OS file lock, so a
// read-modify-write cannot interleave with one from another magus process.
//
// The same idiom the workspace project locks use (magus/lock.go): gofrs/flock, TryLock
// first and TryLockContext to poll while contended. An OS lock rather than a lockfile
// because the kernel drops it when the holder exits, so a killed worker never leaves the
// ledger wedged. Advisory, like that one: it serializes the code that takes it and
// nothing else, so a hand-edit of leases.json ignores it entirely.
//
// The wait is BOUNDED, which is where this parts company with the project locks. Those
// wait forever with a heartbeat because the holder is a build that may legitimately run
// for hours; a ledger write is a small file rewrite, so a wait past lockWait means the
// holder is stuck rather than busy, and blocking a worker's registration on it forever
// hides that. ctx shortens the wait and never lengthens it.
func (s *Store) withFileLock(ctx context.Context, fn func() error) error {
	if s.err != nil {
		return s.err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	fl := flock.New(s.path + lockSuffix)
	got, err := fl.TryLock()
	if err != nil {
		return fmt.Errorf("ledger: lock %s: %w", fl.Path(), err)
	}
	if !got {
		wait, cancel := context.WithTimeout(ctx, lockWait)
		defer cancel()
		if got, err = fl.TryLockContext(wait, lockRetryDelay); err != nil || !got {
			// Whose deadline ended the wait decides which of two different problems the
			// caller has, and blaming a stuck holder for their own cancellation would send
			// them looking for a process that is working fine.
			if ctx.Err() != nil {
				return fmt.Errorf("ledger: the caller was cancelled while waiting for the lease ledger lock at %s,"+
					" so this write was not applied: %w", fl.Path(), ctx.Err())
			}
			return fmt.Errorf("ledger: another process has held the lease ledger lock at %s for more than %s,"+
				" so this write was not applied. Look for a stuck magus process with `magus status`, then retry;"+
				" the lock is an OS file lock and is released the moment its holder exits", fl.Path(), lockWait)
		}
	}
	defer func() { _ = fl.Unlock() }()
	return fn()
}

// The file lock's shape. lockWait bounds one acquisition and lockRetryDelay is how often a
// blocked one re-polls, matching the project locks' cadence in magus/lock.go.
const (
	lockSuffix     = ".lock"
	lockWait       = 10 * time.Second
	lockRetryDelay = 20 * time.Millisecond
)

// write replaces the file atomically, so a reader never sees a half-written ledger.
func (s *Store) write(f ledgerFile) error {
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return file.WriteFileAtomic(s.path, append(raw, '\n'), 0o644)
}

// LeaseMarkerName is the file, in a checkout's cache dir, that binds a lease to THAT
// checkout (`magus session lease <id>` writes it through BindLease). It exists because
// the environment cannot carry a lease into a hook: a host runs its hooks with its own
// environment, so a worker exporting BAGGAGE for its shell is invisible to the guard
// judging its commands. A worker with its own worktree has one checkout, and a file
// in it is the one channel the worker's shell, the host's hook and the sandbox all
// read. BAGGAGE and an explicit --lease still win; the marker is the last resort.
const LeaseMarkerName = "lease"

// ActingLease is the lease the current process acts under, for the checkout whose cache
// dir is cacheDir: the W3C baggage a worker inherits, else the marker BindLease wrote,
// else "". The guard hook and the sandbox both resolve through this one function so the
// two enforcement tiers cannot disagree about who is acting.
func ActingLease(cacheDir string) string {
	if lease := trail.LeaseFromEnv(); lease != "" {
		return lease
	}
	return LeaseFromMarker(cacheDir)
}

// LeaseFromMarker reads the lease bound to the checkout whose cache dir is cacheDir,
// or "" when none is bound or the marker does not hold a lease id.
func LeaseFromMarker(cacheDir string) string {
	if cacheDir == "" {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(cacheDir, LeaseMarkerName))
	if err != nil {
		return ""
	}
	id := strings.TrimSpace(string(raw))
	if !types.ValidLeaseID(id) {
		return ""
	}
	return id
}

// BindLease writes the marker binding lease id to the checkout whose cache dir is
// cacheDir, creating the dir if needed. An id ValidLeaseID rejects is refused, so the
// marker never holds a value LeaseFromMarker would read back as nothing.
//
// BINDING IS ONE-WAY for a worker. A session already acting under a lease cannot bind
// itself to a different one, because that is the whole boundary: a worker that can name
// the orchestrator's row is graded against the orchestrator's paths from its next command.
// Re-binding the lease it already holds is allowed and does nothing, so a worker that
// runs its bootstrap twice is not refused. Only an UNBOUND session, which is the
// orchestrator or the person, binds a checkout.
func BindLease(cacheDir, id string) error {
	if !types.ValidLeaseID(id) {
		return fmt.Errorf("ledger: %q is not a lease id (letters, digits and -_./: only)", id)
	}
	if bound := ActingLease(cacheDir); bound != "" && bound != id {
		return &RefusedError{
			Lease: id, Actor: Actor{Lease: bound},
			Rule: fmt.Sprintf("re-binding it to %s is how a worker would be graded against another lease's paths", id),
		}
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return fmt.Errorf("ledger: bind lease: %w", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, LeaseMarkerName), []byte(id+"\n"), 0o644); err != nil {
		return fmt.Errorf("ledger: bind lease: %w", err)
	}
	return nil
}
