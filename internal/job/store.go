// Package job persists the job store: the jobs an orchestrating agent declares
// about the plan it is running, kept where a human can read them.
//
// It gates no run and blocks no write to the tree; the one write it refuses is a write to
// a job the caller does not own (see [authorizeRow]). One plan per REPOSITORY, so every
// worktree and clone reads the same jobs.
//
// The INTENT layer of three, flat stores joined by job id at render time rather than a
// hierarchy: intent is this package, actions are internal/trail, and effects are the run
// itself. internal/journal (one invocation's events) and internal/memory and
// internal/notes (prose for a later reader) model no leased work and are not siblings.
package job

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

// ErrNoID reports a lease with no id. The id is what Update upserts on, so a row without
// one could never be updated or referred to again: it is unaddressable, not merely
// incomplete.
var ErrNoID = errors.New("job: a lease needs an id")

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
//   - An OS file lock beside jobs.json serializes writers across PROCESSES. The CLI, the
//     daemon, and an MCP client each hold their own Store on the same file, from any
//     worktree or clone of the repository, and workers now register and heartbeat against
//     it, so "one orchestrating agent writes this"
//     (the assumption that made a cross-process race acceptable) stopped being true. Two
//     read-modify-writes that interleave drop whichever row the loser had not read.
//
// Both are taken for the span of ONE call. A caller that merges fields by reading with
// List and writing back the whole row takes them twice, and two such merges on one id
// then lose whichever field the second one read before the first wrote. [Store.Update] is
// that merge done under a single acquisition, and it is what a field-at-a-time writer (the
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
// digested against the state dir reads as an ordinary empty job store.
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
// location on the way past. The leases file itself is created by the first Update.
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
	s.path, s.err = jobsPath(loc)
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

// jobsPath places the jobs file: <XDG state>/magus/jobs/<repo>/jobs.json, after
// carrying forward whatever an older magus left at <CacheDir>/job.
func jobsPath(loc Location) (string, error) {
	base := loc.StateBase
	if base == "" {
		var err error
		if base, err = config.UserStateDir(); err != nil {
			return "", fmt.Errorf("job: resolve state dir: %w (set XDG_STATE_HOME to a writable absolute path)", err)
		}
	}
	dir, err := repoid.StateDir(base, "jobs", loc.Root)
	if err != nil {
		return "", fmt.Errorf("job: %w", err)
	}
	// compat(until: no checkout's cache dir still holds a ledger/ directory; observe
	// with `find ~ -path '*/.magus/cache/ledger' -maxdepth 6` returning nothing):
	// carries forward the rows an older magus kept per checkout.
	if loc.CacheDir != "" {
		if err := repoid.Adopt(filepath.Join(loc.CacheDir, "ledger"), dir); err != nil {
			return "", fmt.Errorf("job: %w", err)
		}
	}
	path := filepath.Join(dir, "jobs.json")
	if err := adoptLedgerPlan(base, dir, path); err != nil {
		return "", err
	}
	return path, nil
}

// The store's previous home, one rename ago: the kind directory and the file name the
// plan was kept under while a job was called a lease.
const (
	legacyKind = "ledger"
	legacyFile = "leases.json"
)

// adoptLedgerPlan copies a pre-rename plan into the job store, once, when the job store
// has none of its own.
//
// A COPY rather than a move, which is the whole reason this is not repoid.Adopt: binaries
// of the previous vintage are still reading leases.json from other checkouts of this
// repository, and taking the file out from under them empties their plan mid-session. The
// cost is that rows written to the old file after this runs are not seen here, which is
// what a rename between two live binaries buys either way.
//
// The rows are carried as RAW JSON, so a member this magus does not know survives the
// copy. Decoding into the row struct would drop exactly what the reader of an older file
// most needs kept.
//
// compat(until: no store still holds a pre-rename leases.json; observe:
// `find ~/.local/state/magus/ledger -name leases.json` returning nothing).
func adoptLedgerPlan(base, dir, path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	legacy := filepath.Join(base, "magus", legacyKind, filepath.Base(dir), legacyFile)
	raw, err := os.ReadFile(legacy)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("job: read the plan at %s to carry it into %s: %w", legacy, path, err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("job: the plan at %s is not readable, so it was not carried into %s: %w", legacy, path, err)
	}
	rows, ok := envelope["leases"]
	if !ok {
		return nil
	}
	carried, err := json.MarshalIndent(map[string]json.RawMessage{"jobs": rows}, "", "  ")
	if err != nil {
		return fmt.Errorf("job: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("job: %w", err)
	}
	return file.WriteFileAtomic(path, append(carried, '\n'), 0o644)
}

// Path is the leases file this Store reads and writes, for a reader that has to name it:
// a person asking where their plan is kept, or a test planting one. The file itself may
// not exist yet, which is an empty ledger and not an error.
func (s *Store) Path() (string, error) { return s.path, s.err }

// jobsFile is the on-disk envelope. An object rather than a bare array so a later
// field (a plan identity, a schema version) can be added without every existing reader
// failing to parse the file.
type jobsFile struct {
	Jobs []types.Job `json:"jobs"`
}

// Update applies apply to the row with this id and writes the result back while holding
// both of the Store's locks ONCE: a merge spread across List and a whole-row write
// releases them in between, so two concurrent writers advancing different fields of one
// row each read it before the other wrote, and the second write reverts the first.
//
// A caller that wants to replace a row whole rather than merge fields passes an apply that
// does `*cur = row`; the timestamps and everything else the store computes are ignored on
// the way in and returned on the way out.
//
// The row is CREATED when absent, so declaring a lease and advancing one are the same
// call, and the id is the key: whatever apply writes into ID is overwritten with it.
//
// Releases are stamped here because they are the store's to say: a write that drops a path
// from WritePaths IS the release announcement, and reading the previous write set and
// writing the next one has to be one step or a concurrent put decides which release
// happened.
//
// apply runs while the lock is held, so it must not touch the store, and it cannot fail:
// anything that could be rejected belongs in the caller, before the call.
//
// ctx reaches the release digests and nothing else. A cancelled call still WRITES the row,
// since the merge is already done, but it stops hashing files rather than reading the disk
// with the store's lock held.
func (s *Store) Update(ctx context.Context, id string, apply func(*types.Job)) (types.Job, error) {
	return s.mutate(ctx, id, asDeclaration, func(cur *types.Job, _ bool, _ int64) error {
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

// RecordUnattributedWrite notes that somebody outside lease id wrote one of its write paths,
// with the content they left behind.
//
// ONE ROW PER PATH, newest wins: a person saves a file a dozen times while an agent works,
// and the twelfth save is the only one that describes the tree.
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
	_, err := s.mutate(ctx, id, asObservation, func(cur *types.Job, exists bool, now int64) error {
		if !exists {
			return fmt.Errorf("job: no such lease %q: %w", id, errNoSuchJob)
		}
		next := make([]types.JobUnattributedWrite, 0, len(cur.Unattributed)+1)
		for _, w := range cur.Unattributed {
			if w.Path != path {
				next = append(next, w)
			}
		}
		next = append(next, types.JobUnattributedWrite{
			Path: path, Digest: s.digest(ctx, path), At: now,
		})
		if len(next) > MaxUnattributedWrites {
			next = next[len(next)-MaxUnattributedWrites:]
		}
		cur.Unattributed = next
		return nil
	})
	if errors.Is(err, errNoSuchJob) {
		return nil
	}
	return err
}

// errNoSuchJob is internal to RecordUnattributedWrite: mutate creates a row that is not there, and
// this is how the apply func declines that without inventing a lease nobody declared.
var errNoSuchJob = errors.New("job: no such lease")

// mutate is the locked read-modify-write [Store.Update] and [Store.Exec] share, and
// the only place jobs.json is rewritten row-wise. kind says what the write is; see
// [grading].
//
// apply gets what only Exec needs: exists, because Update creates a row and Exec
// refuses one nobody declared, and now, the one clock read the write is stamped from. It
// may fail, which is what lets that refusal be decided under the lock; nothing is written
// when it does.
func (s *Store) mutate(ctx context.Context, id string, kind grading, apply func(cur *types.Job, exists bool, now int64) error) (types.Job, error) {
	if strings.TrimSpace(id) == "" {
		return types.Job{}, ErrNoID
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	actor := s.Actor()
	var stored types.Job
	err := s.withFileLock(ctx, func() error {
		f, err := s.read()
		if err != nil {
			return err
		}
		i := slices.IndexFunc(f.Jobs, func(e types.Job) bool { return e.ID == id })
		var prev types.Job
		if i >= 0 {
			prev = f.Jobs[i]
		}
		row := prev.Clone()
		row.ID = id
		now := time.Now().Unix()
		if aerr := apply(&row, i >= 0, now); aerr != nil {
			return aerr
		}
		row = row.Clone()
		row.ID = id
		if kind.graded() {
			if aerr := authorizeRow(actor, id, prev, row, i >= 0, f.Jobs); aerr != nil {
				return aerr
			}
		}
		// The store's own record of what HAPPENED is not a caller's to send. On an
		// existing row it is carried forward, so a whole-row write neither forges nor
		// erases a registration; a row a BOUND caller creates carries none, since a child
		// handed a registration it never made writes without ever reporting its base.
		switch {
		case i >= 0:
			if kind != asExec {
				row.ReportedBase, row.BaseVerdict, row.Registered = prev.ReportedBase, prev.BaseVerdict, prev.Registered
			}
			if kind != asObservation {
				row.Unattributed = prev.Unattributed
			}
		case actor.Bound():
			row.ReportedBase, row.BaseVerdict, row.Registered, row.Unattributed = "", "", 0, nil
		}
		row.Updated = now
		row.Created = now
		row.SchemaVersion = types.JobSchemaVersion
		row.Releases = s.releases(ctx, prev, row, now)
		if i >= 0 {
			row.Created = prev.Created
			row.RegisteredBy = prev.RegisteredBy
			f.Jobs[i] = row
		} else {
			row.RegisteredBy = actor.leaseActor()
			f.Jobs = append(f.Jobs, row)
		}
		if werr := s.write(f); werr != nil {
			return werr
		}
		stored = row.Clone()
		return nil
	})
	if err != nil {
		return types.Job{}, err
	}
	return stored, nil
}

// List returns every row in the order it was first recorded. The rows are copies, so a
// caller may keep or mutate them without reaching back into the file's next read.
func (s *Store) List() ([]types.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := s.read()
	if err != nil {
		return nil, err
	}
	out := make([]types.Job, len(f.Jobs))
	for i, row := range f.Jobs {
		out[i] = row.Clone()
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
		if len(f.Jobs) > 0 {
			if err := s.archive(f); err != nil {
				return err
			}
		}
		dropped = len(f.Jobs)
		return s.write(jobsFile{})
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
func (s *Store) archive(f jobsFile) error {
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	name := "jobs-" + time.Now().UTC().Format("20060102T150405Z") + ".json"
	return file.WriteFileAtomic(filepath.Join(filepath.Dir(s.path), name), append(raw, '\n'), 0o644)
}

// releases carries the row's recorded releases forward and adds the paths this write
// gave up, the ones prev owned and next does not.
//
// A path that is owned again drops OUT of the list: a row saying it both owns and has
// released the same path tells a reader nothing they can act on. A path released twice
// keeps its position and takes the NEWER digest, because the version the next agent
// inherits is the one left behind last.
func (s *Store) releases(ctx context.Context, prev, next types.Job, now int64) []types.JobRelease {
	out := slices.DeleteFunc(slices.Clone(prev.Releases), func(r types.JobRelease) bool {
		return slices.Contains(next.WritePaths, r.Path)
	})
	for _, p := range prev.WritePaths {
		if slices.Contains(next.WritePaths, p) {
			continue
		}
		rel := types.JobRelease{Path: p, Digest: s.digest(ctx, p), ReleasedAt: now}
		if at := slices.IndexFunc(out, func(r types.JobRelease) bool { return r.Path == p }); at >= 0 {
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
func (s *Store) read() (jobsFile, error) {
	if s.err != nil {
		return jobsFile{}, s.err
	}
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return jobsFile{}, nil
	}
	if err != nil {
		return jobsFile{}, err
	}
	var f jobsFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return jobsFile{}, err
	}
	if err := foldStoredLanes(raw, f.Jobs); err != nil {
		return jobsFile{}, fmt.Errorf("job: %s: %w", s.path, err)
	}
	// A row this binary cannot read whole stops every operation, not just the read of that
	// row: mutate rewrites EVERY row in the file, so one unrelated put would silently drop
	// whatever a newer magus recorded across the whole plan.
	for _, row := range f.Jobs {
		if row.SchemaVersion > types.JobSchemaVersion {
			return jobsFile{}, fmt.Errorf("job: row %s in %s is schema_version %d and this magus accepts version %d only."+
				" A newer ledger is not readable by an older magus; update magus", row.ID, s.path, row.SchemaVersion, types.JobSchemaVersion)
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
		return fmt.Errorf("job: lock %s: %w", fl.Path(), err)
	}
	if !got {
		wait, cancel := context.WithTimeout(ctx, lockWait)
		defer cancel()
		if got, err = fl.TryLockContext(wait, lockRetryDelay); err != nil || !got {
			// Whose deadline ended the wait decides which of two different problems the
			// caller has, and blaming a stuck holder for their own cancellation would send
			// them looking for a process that is working fine.
			if ctx.Err() != nil {
				return fmt.Errorf("job: the caller was cancelled while waiting for the lease ledger lock at %s,"+
					" so this write was not applied: %w", fl.Path(), ctx.Err())
			}
			return fmt.Errorf("job: another process has held the lease ledger lock at %s for more than %s,"+
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

// write replaces the file atomically, so a reader never sees a half-written job store.
func (s *Store) write(f jobsFile) error {
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
	if !types.ValidJobID(id) {
		return ""
	}
	return id
}

// BindLease writes the marker binding lease id to the checkout whose cache dir is
// cacheDir, creating the dir if needed. An id ValidJobID rejects is refused, so the
// marker never holds a value LeaseFromMarker would read back as nothing.
//
// BINDING IS ONE-WAY for a worker. A session already acting under a lease cannot bind
// itself to a different one, because that is the whole boundary: a worker that can name
// the orchestrator's row is graded against the orchestrator's paths from its next command.
// Re-binding the lease it already holds is allowed and does nothing, so a worker that
// runs its bootstrap twice is not refused. Only an UNBOUND session, which is the
// orchestrator or the person, binds a checkout.
func BindLease(cacheDir, id string) error {
	if !types.ValidJobID(id) {
		return fmt.Errorf("job: %q is not a lease id (letters, digits and -_./: only)", id)
	}
	if bound := ActingLease(cacheDir); bound != "" && bound != id {
		return &RefusedError{
			Lease: id, Actor: Actor{Lease: bound},
			Rule: fmt.Sprintf("re-binding it to %s is how a worker would be graded against another lease's paths", id),
		}
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return fmt.Errorf("job: bind lease: %w", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, LeaseMarkerName), []byte(id+"\n"), 0o644); err != nil {
		return fmt.Errorf("job: bind lease: %w", err)
	}
	return nil
}
