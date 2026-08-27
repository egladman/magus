// Package ledger persists the delegation ledger: the rows an orchestrating agent
// declares about the plan it is running, kept where a human can read them.
//
// It RECORDS AND REFUSES NOTHING. Nothing here gates a run or blocks a write; the AGENT
// GUARD is what consults these rows to grade one, and it is a separate thing that READS
// this store. Register does compute a verdict - whether a worker's reported base is the
// checkpoint its delegation was handed - and that is a fact recorded on the row and handed back
// to the caller, not a gate: the registration succeeds either way and what to do about a
// divergence is the caller's and the orchestrator's call. Enforcement stays outside for
// the reason types.Delegation gives: a store that refused would make the ledger
// something agents route around, and a ledger nobody keeps honestly grades nothing. The
// store's whole job is that a plan an agent stated in a prompt stops being trapped in one
// session's transcript.
//
// ONE PLAN PER WORKSPACE, and no history of past plans: Clear wipes the rows so the
// next plan starts empty. A plan that ended is not archived anywhere, which is the v1
// scope on purpose - keeping every past plan means deciding what identifies one, and
// nothing in the vocabulary names a plan yet.
//
// "LEDGER" NAMES THE RECONCILIATION, NOT THE DURABILITY, and the difference is worth
// stating because the word oversells one of them. A financial ledger is append-only and
// historical; this is neither - Put upserts a row in place, Clear wipes the book, and
// nothing is archived. What it does share is the part that earns the name: it is written
// to be checked AGAINST reality later, which is exactly the skill's "compare the ledger
// against the actual diff since each delegation's checkpoint" step. Read it as a book of
// declared intent kept for reconciliation, not as a durable record of what happened. The
// vocabulary came from the delegation skill, which is also where the row shape is defined
// (see types.Delegation).
//
// It is the INTENT layer of three, and naming the other two is what keeps them apart -
// they are flat stores joined by delegation id at render time, never a storage hierarchy:
//
//   - intent - this package. What an orchestrating agent SAID it would hand out.
//     Declared up front, mutable, one plan at a time.
//   - actions - internal/trail. What was actually DONE against the daemon, append-only.
//     The closest sibling, and the one to reach for when the question is "did it happen"
//     rather than "was it planned".
//   - effects - the run itself: the pool, the locks, the outputs a target produced.
//
// The two stores that sound related and are NOT: internal/journal is the event stream of
// one magus invocation (what a build executed), and internal/memory and internal/notes
// are prose a human or an agent writes to be read later - neither models delegated work.
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

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// ErrNoID reports a delegation with no id. The id is what Put upserts on, so a row without
// one could never be updated or referred to again - it is unaddressable, not merely
// incomplete.
var ErrNoID = errors.New("ledger: a delegation needs an id")

// Store is the workspace's delegation ledger, a single JSON file under the cache
// directory. Every operation reads the file, acts, and writes it back, so a Store is
// cheap to construct and holds no state between calls beyond the path and its lock.
//
// TWO locks, because there are two kinds of concurrent writer and neither lock sees the
// other's:
//
//   - The mutex serializes writers within one process while they share a Store, which is
//     why the daemon builds exactly one and hands it to both of its doors (the
//     magus_ledger MCP tool and the console's read route).
//   - An OS file lock beside units.json serializes writers across PROCESSES. The CLI, the
//     daemon, and an MCP client each hold their own Store on the same file, and workers
//     now register and heartbeat against it, so "one orchestrating agent writes this" -
//     the assumption that made a cross-process race acceptable - stopped being true. Two
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
	root string
}

// Location is where a Store lives: which cache directory holds the file, and which
// workspace its rows describe. A struct rather than two string params because the two
// are transposable at every call site and nothing downstream would notice - a ledger
// written into the workspace and digested against the cache dir reads as an ordinary
// empty ledger.
type Location struct {
	// CacheDir is the workspace's cache directory (magus.CacheDir); the ledger is
	// written to <CacheDir>/ledger/units.json.
	CacheDir string
	// Root is the workspace a row's paths are relative to, read for one purpose:
	// digesting a path at the moment a delegation releases it (see Update). A Store built
	// with an empty root still records releases - it just cannot say what was in them.
	Root string
}

// NewStore returns the ledger at loc. It touches no disk: the file is created by the
// first Put.
func NewStore(loc Location) *Store {
	return &Store{path: filepath.Join(loc.CacheDir, "ledger", "units.json"), root: loc.Root}
}

// ledgerFile is the on-disk envelope. An object rather than a bare array so a later
// field (a plan identity, a schema version) can be added without every existing reader
// failing to parse the file.
type ledgerFile struct {
	// compat(until: no cache directory still holds a ledger written before the
	// delegation rename): the on-disk key stays "units", as does the file name. A
	// running plan lives in this file, and a renamed key would read every declared row
	// back as an empty ledger - which the guard cannot tell from "nobody delegated",
	// so it would silently stop grading writes. Observe it is safe to drop when no
	// units.json predating the rename is left to load.
	Delegations []types.Delegation `json:"units"`
}

// Put records one delegation, replacing any row with the same id IN PLACE. Position is
// preserved on update because the ledger is a table a person reads top to bottom, and
// a row that jumped to the bottom every time its state changed would reorder itself
// exactly when it is being watched.
//
// It stamps Created on the first write and Updated on every write, ignoring whatever
// the caller passed for either. The stored row is returned.
func (s *Store) Put(ctx context.Context, u types.Delegation) (types.Delegation, error) {
	return s.Update(ctx, u.ID, func(cur *types.Delegation) { *cur = u })
}

// Update applies apply to the row with this id and writes the result back, all while
// holding both of the Store's locks ONCE. That is the whole point: a merge spread across
// List and Put releases them in between, so two concurrent writers advancing different
// fields of one row each read it before the other wrote, and the second write reverts the
// first - whether the two are goroutines or separate magus processes.
//
// The row is CREATED when absent, matching Put: apply then sees a zero delegation carrying
// only the id, so declaring a delegation and advancing one are the same call. Created is
// preserved from the stored row and Updated is stamped on every write, exactly as Put
// does, and the id is the key - whatever apply writes into ID is overwritten with it.
//
// Releases are stamped here too, and for the same reason Created is: they are the
// store's to say, not the caller's. A write that drops a path from OwnedPaths IS the
// release announcement the skill has workers make when they finish editing a contested
// path, so the dropped paths are digested and recorded on the row - under this same
// lock, because reading the previous owned set and writing the next one has to be one
// step or a concurrent put decides which release happened.
//
// apply runs while the lock is held, so it must not touch the store, and it cannot fail:
// anything that could be rejected (an unknown state, a mistyped param) belongs in the
// caller, before the call.
//
// ctx reaches the release digests and nothing else. A cancelled call still WRITES the
// row - the merge is already done and abandoning it would lose the state change - but it
// stops hashing files, so a caller that walked away does not keep the store's lock while
// the disk is read.
func (s *Store) Update(ctx context.Context, id string, apply func(*types.Delegation)) (types.Delegation, error) {
	return s.mutate(ctx, id, func(cur *types.Delegation, _ bool, _ int64) error {
		apply(cur)
		return nil
	})
}

// MaxUnattributedWrites bounds how many outside-written paths one row keeps.
//
// A cap rather than a growing list because this is written from the guard, which runs on every
// file write a host makes: without one, a plan left declared over a long editing session would
// accumulate a row entry per save until the ledger was mostly this. The newest are kept, since a
// delegation asking what moved is asking about the tree it faces now.
const MaxUnattributedWrites = 32

// RecordUnattributedWrite notes that somebody outside delegation id wrote one of its owned paths,
// with the content they left behind.
//
// ONE ROW PER PATH, newest wins. A person saves a file a dozen times while an agent works; the
// twelfth save is the only one that describes the tree, and eleven superseded digests would bury
// it. The timestamp moves with the digest, so "when did this last move" stays answerable.
//
// Recording from the guard is a deliberate softening of this package's split - the ledger records,
// the guard enforces - and it survives the rule because what lands here is an OBSERVATION and
// never a verdict. The guard already read these boundaries to grade the write; it simply discarded
// what it saw afterwards, leaving the one party who needed it uninformed.
//
// A missing row is not an error: the delegation may have ended between the grading and this call,
// and a write graded against a plan that has since finished is nothing to report to anybody.
func (s *Store) RecordUnattributedWrite(ctx context.Context, id, path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	_, err := s.mutate(ctx, id, func(cur *types.Delegation, exists bool, now int64) error {
		if !exists {
			return errNoSuchRow
		}
		next := make([]types.DelegationUnattributedWrite, 0, len(cur.Unattributed)+1)
		for _, w := range cur.Unattributed {
			if w.Path != path {
				next = append(next, w)
			}
		}
		next = append(next, types.DelegationUnattributedWrite{
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
// this is how the apply func declines that without inventing a delegation nobody declared.
var errNoSuchRow = errors.New("ledger: no such delegation")

// mutate is the locked read-modify-write [Store.Update] and [Store.Register] share, and
// the only place units.json is rewritten row-wise.
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
func (s *Store) mutate(ctx context.Context, id string, apply func(cur *types.Delegation, exists bool, now int64) error) (types.Delegation, error) {
	if strings.TrimSpace(id) == "" {
		return types.Delegation{}, ErrNoID
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var stored types.Delegation
	err := s.withFileLock(ctx, func() error {
		f, err := s.read()
		if err != nil {
			return err
		}
		i := slices.IndexFunc(f.Delegations, func(e types.Delegation) bool { return e.ID == id })
		var prev types.Delegation
		if i >= 0 {
			prev = f.Delegations[i]
		}
		u := prev.Clone()
		u.ID = id
		now := time.Now().Unix()
		if aerr := apply(&u, i >= 0, now); aerr != nil {
			return aerr
		}
		u = u.Clone()
		u.ID = id
		u.Updated = now
		u.Created = now
		u.Releases = s.releases(ctx, prev, u, now)
		if i >= 0 {
			u.Created = prev.Created
			f.Delegations[i] = u
		} else {
			f.Delegations = append(f.Delegations, u)
		}
		if werr := s.write(f); werr != nil {
			return werr
		}
		stored = u.Clone()
		return nil
	})
	if err != nil {
		return types.Delegation{}, err
	}
	return stored, nil
}

// List returns every row in the order it was first recorded. The rows are copies, so a
// caller may keep or mutate them without reaching back into the file's next read.
func (s *Store) List() ([]types.Delegation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := s.read()
	if err != nil {
		return nil, err
	}
	out := make([]types.Delegation, len(f.Delegations))
	for i, u := range f.Delegations {
		out[i] = u.Clone()
	}
	return out, nil
}

// Clear drops every row, which is how a fresh plan starts. Clearing an empty or absent
// ledger is not an error - the caller asked for an empty ledger and got one.
//
// It reads nothing, so no row can be lost to an interleaving; it takes the file lock
// anyway, so that every rewrite of units.json is under it and a reader of this package
// never has to work out which writes are the exempt ones. ctx bounds the wait for the
// lock and nothing else.
func (s *Store) Clear(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.withFileLock(ctx, func() error { return s.write(ledgerFile{}) })
}

// releases carries the row's recorded releases forward and adds the paths this write
// gave up - the ones prev owned and next does not.
//
// A path that is owned again drops OUT of the list: a row saying it both owns and has
// released the same path tells a reader nothing they can act on. A path released twice
// keeps its position and takes the NEWER digest, because the version the next agent
// inherits is the one left behind last.
func (s *Store) releases(ctx context.Context, prev, next types.Delegation, now int64) []types.DelegationRelease {
	out := slices.DeleteFunc(slices.Clone(prev.Releases), func(r types.DelegationRelease) bool {
		return slices.Contains(next.OwnedPaths, r.Path)
	})
	for _, p := range prev.OwnedPaths {
		if slices.Contains(next.OwnedPaths, p) {
			continue
		}
		rel := types.DelegationRelease{Path: p, Digest: s.digest(ctx, p), ReleasedAt: now}
		if at := slices.IndexFunc(out, func(r types.DelegationRelease) bool { return r.Path == p }); at >= 0 {
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
// documented markers rather than a hash - types.DigestAbsent, types.DigestDir and
// types.DigestUnreadable say which. The absent/unreadable split matters to the next
// agent: "the releaser deleted it" and "something is there nobody could read" are
// different problems. A path escaping the root is absent by the same rule: the ledger
// describes this workspace, so a row is never handed a digest of something outside it.
//
// Runs under the store's mutex, which is deliberate - reading the previous owned set and
// recording what it gave up has to be one step - and is why every branch below is bounded.
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
		// somebody writes to it, and it would block holding the store's mutex - one
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
// holds its mutex. 32 MiB is far above the source files a delegation actually releases and far
// below the build artifact that would otherwise stall every other ledger caller for as
// long as it takes to read it; a path over the cap records unreadable, which is what it
// is from the reader's side.
const maxDigestBytes = 32 << 20

// read loads the file. An absent file is an empty ledger, not a failure: nothing has
// been recorded yet in this workspace.
func (s *Store) read() (ledgerFile, error) {
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
	return f, nil
}

// withFileLock runs fn while this process holds the ledger's exclusive OS file lock, so a
// read-modify-write cannot interleave with one from another magus process.
//
// The same idiom the workspace project locks use (magus/lock.go): gofrs/flock, TryLock
// first and TryLockContext to poll while contended. An OS lock rather than a lockfile
// because the kernel drops it when the holder exits, so a killed worker never leaves the
// ledger wedged. Advisory, like that one - it serializes the code that takes it and
// nothing else, so a hand-edit of units.json ignores it entirely.
//
// The wait is BOUNDED, which is where this parts company with the project locks. Those
// wait forever with a heartbeat because the holder is a build that may legitimately run
// for hours; a ledger write is a small file rewrite, so a wait past lockWait means the
// holder is stuck rather than busy, and blocking a worker's registration on it forever
// hides that. ctx shortens the wait and never lengthens it.
func (s *Store) withFileLock(ctx context.Context, fn func() error) error {
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
				return fmt.Errorf("ledger: the caller was cancelled while waiting for the delegation ledger lock at %s,"+
					" so this write was not applied: %w", fl.Path(), ctx.Err())
			}
			return fmt.Errorf("ledger: another process has held the delegation ledger lock at %s for more than %s,"+
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
