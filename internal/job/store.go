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
	"maps"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
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
//     why the server builds exactly one and hands it to both of its doors (the
//     client MCP tool's magus\job and the console's read route).
//   - An OS file lock beside jobs.json serializes writers across PROCESSES. The CLI, the
//     server, and an MCP client each hold their own Store on the same file, from any
//     worktree or clone of the repository, and workers now register and heartbeat against
//     it, so "one orchestrating agent writes this"
//     (the assumption that made a cross-process race acceptable) stopped being true. Two
//     read-modify-writes that interleave drop whichever row the loser had not read.
//
// Both are taken for the span of ONE call. A caller that merges fields by reading with
// List and writing back the whole row takes them twice, and two such merges on one id
// then lose whichever field the second one read before the first wrote. [Store.Update] is
// that merge done under a single acquisition, and it is what a field-at-a-time writer (the
// client MCP tool's magus\job\put) has to use.
//
// READS take neither. Every write replaces the file by rename, so a reader either sees the
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
	// from cacheDir and the environment. The server builds ONE Store at startup and serves
	// every MCP job caller from it, so an actor frozen at construction grades all of
	// them as whoever started the process.
	actor    *Actor
	cacheDir string
	// clock, staleAfter and notices are the sweep's seams (see [Store.List]); nil means
	// time.Now, the workspace's jobs.stale_after, and stderr.
	clock      func() time.Time
	staleAfter *time.Duration
	notices    io.Writer
	// outputs is Location.Outputs.
	outputs func(root string) AttemptResolver
	// landed is the sweep's landing probe, nil for [Store.landedOnBase]; see [sweeper.landed].
	landed func(context.Context, types.Job) string
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
	// REPOSITORY owns the rows (through vcs.StateDir, which folds every worktree and clone of
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
	// Outputs resolves an output ref against the output store of the checkout at root.
	// Exit and Wait fall back to it when the caller's resolver finds no run, trying the
	// checkouts the job and its descendants were taken in, so an orchestrator can file a
	// result its worker recorded in another checkout of the repository. Nil tries the
	// caller's resolver alone.
	Outputs func(root string) AttemptResolver
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
	s := &Store{root: loc.Root, actor: loc.Actor, cacheDir: loc.CacheDir, outputs: loc.Outputs}
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
	dir, err := vcs.StateDir(base, "jobs", loc.Root)
	if err != nil {
		return "", fmt.Errorf("job: %w", err)
	}
	// compat(until: no checkout's cache dir still holds a ledger/ directory; observe
	// with `find ~ -path '*/.magus/cache/ledger' -maxdepth 6` returning nothing):
	// carries forward the rows an older magus kept per checkout.
	if loc.CacheDir != "" {
		if err := vcs.Adopt(filepath.Join(loc.CacheDir, "ledger"), dir); err != nil {
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
// A COPY rather than a move, which is the whole reason this is not vcs.Adopt: binaries
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
// member can be added without every existing reader failing to parse the file.
//
// Its Schema.Requires is the one refusal wider than a row: a writer sets it only for a
// break no row-level requirement can express, and a reader lacking one refuses the file.
type jobsFile struct {
	types.Schema
	Jobs []types.Job `json:"jobs"`
}

// readOnly refuses a write to row when it requires a feature this magus lacks. The row
// stays listed and graded; only writing it is out of reach, since a write by a reader
// that cannot act on the row's content could corrupt it.
func readOnly(row types.Job) error {
	lacks := row.Unmet(types.JobSchema.Features())
	if lacks == nil {
		return nil
	}
	return fmt.Errorf("job: %s requires %s, which this magus (schema %d) lacks, so it will not write that row."+
		" Update magus, or run the command with the magus that wrote it",
		row.ID, quoteAll(lacks), types.JobSchemaVersion)
}

func quoteAll(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = strconv.Quote(n)
	}
	return strings.Join(quoted, ", ")
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
			Path: path, Digest: s.fileDigest(ctx, path), At: now,
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

// MaxJobEntries is how many entries one job takes. Past it, the job wants its holder
// resumed or its lease ended, not a third visitor.
const MaxJobEntries = 2

// Enter records that the caller will write rel, inside the write paths of live job id,
// once. The guard then lets one write by somebody other than the holder through and
// stamps it consumed; see [Store.ConsumeEntry]. A put carrying `enter` is the same write.
//
// Refused, and nothing is written, when the job is not live, when the caller is its
// holder, when rel is outside its write paths (that wants widening, not entering), when
// an entry for rel is still open, and when the job already took [MaxJobEntries].
func (s *Store) Enter(ctx context.Context, id, rel string) (types.Job, error) {
	rel, err := entryPath(rel)
	if err != nil {
		return types.Job{}, err
	}
	return s.Update(ctx, id, func(u *types.Job) { u.Entries = append(u.Entries, types.JobEntry{Path: rel}) })
}

// EditOptions is the change [Store.Edit] makes to a live job's write paths.
type EditOptions struct {
	// AddWritePaths widens the job. A path it already holds is left where it is.
	AddWritePaths []string
	// RemoveWritePaths revokes paths the job holds.
	RemoveWritePaths []string
	// DryRun returns the row the edit would write, releases included, and writes nothing.
	DryRun bool
}

// Edit merges opts into live job id's write paths in one write, and keeps everything else
// the row carries, its state and registration included, where a re-fork hands a taken job
// out again as declared. A removed path is released as a worker's own shrink releases one,
// with the digest of what it left, and marked revoked when someone other than the holder of
// a taken job removed it, so the holder's next write there is refused naming that.
//
// Who may do what is [authorizeRow]'s: an unbound writer widens, and a bound worker only
// removes its own paths. An added path is held to the rules a fork's write paths are (see
// [RefuseAddedWritePaths]). Naming no path, one path both ways, a path the job does not
// hold, a job nobody declared or one that ended is an error, and nothing is written.
func (s *Store) Edit(ctx context.Context, id string, opts EditOptions) (types.Job, error) {
	if len(opts.AddWritePaths)+len(opts.RemoveWritePaths) == 0 {
		return types.Job{}, fmt.Errorf("job: an edit of %s names no write path to add or remove", id)
	}
	for _, p := range opts.AddWritePaths {
		if slices.Contains(opts.RemoveWritePaths, p) {
			return types.Job{}, fmt.Errorf("job: %q is both added and removed", p)
		}
	}
	if err := RefuseAddedWritePaths(ctx, s, id, opts.AddWritePaths); err != nil {
		return types.Job{}, err
	}
	if !opts.DryRun {
		return s.mutate(ctx, id, asDeclaration, func(cur *types.Job, exists bool, _ int64) error {
			return editWritePaths(cur, id, exists, opts)
		})
	}
	rows, err := s.List()
	if err != nil {
		return types.Job{}, err
	}
	i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id })
	var prev types.Job
	if i >= 0 {
		prev = rows[i]
		if err := readOnly(prev); err != nil {
			return types.Job{}, err
		}
	}
	next := prev.Clone()
	if err := editWritePaths(&next, id, i >= 0, opts); err != nil {
		return types.Job{}, err
	}
	actor := s.Actor()
	if err := authorizeRow(actor, id, prev, next, true, rows); err != nil {
		return types.Job{}, err
	}
	next.Releases = s.releases(ctx, prev, next, time.Now().Unix(), prev.Registered != 0 && actor.Lease != id)
	return next, nil
}

// editWritePaths applies opts to cur, the stored row id, or says why it cannot.
func editWritePaths(cur *types.Job, id string, exists bool, opts EditOptions) error {
	switch {
	case !exists:
		return fmt.Errorf("job: there is no job %q to edit", id)
	case cur.State.Terminal():
		return fmt.Errorf("job: %s already ended %s, and an edit changes a live job", id, cur.State)
	}
	for _, p := range opts.RemoveWritePaths {
		if !slices.Contains(cur.WritePaths, p) {
			return fmt.Errorf("job: %s does not hold %q, so there is nothing to revoke; its write paths are %s",
				id, p, strings.Join(cur.WritePaths, ", "))
		}
	}
	paths := slices.DeleteFunc(slices.Clone(cur.WritePaths), func(p string) bool { return slices.Contains(opts.RemoveWritePaths, p) })
	for _, p := range opts.AddWritePaths {
		if !slices.Contains(paths, p) {
			paths = append(paths, p)
		}
	}
	// An empty write set is no boundary at all (the guard scopes nothing by it), so taking
	// the last path would free the job rather than stop it.
	if len(paths) == 0 {
		return fmt.Errorf("job: that revokes every write path %s holds, which leaves it bounded by nothing;"+
			" end the job with `%s` instead", id, hint.JobExit.With(id))
	}
	cur.WritePaths = paths
	return nil
}

// entryPath cleans the path an entry names, refusing one that is not workspace-relative.
func entryPath(rel string) (string, error) {
	clean := path.Clean(filepath.ToSlash(strings.TrimSpace(rel)))
	if clean == "." || path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("job: enter %q: name one workspace-relative path inside the job's write paths", rel)
	}
	return clean, nil
}

// requestsEntry reports a write whose only change is one unstamped entry appended to
// prev's, which is how Enter and a put carrying `enter` reach mutate.
func requestsEntry(prev, next types.Job) bool {
	n := len(prev.Entries)
	if len(next.Entries) != n+1 || next.Entries[n].At != 0 {
		return false
	}
	for k := range n {
		if !reflect.DeepEqual(next.Entries[k], prev.Entries[k]) {
			return false
		}
	}
	return true
}

// authorizeEntry grades an entry: prev is the row before it and next carries the one
// entry appended to it.
func authorizeEntry(actor Actor, id string, prev, next types.Job, exists bool, rows []types.Job) error {
	entry := next.Entries[len(next.Entries)-1]
	switch {
	case !exists:
		return fmt.Errorf("%w %q: there is nothing to enter", ErrUnknownJob, id)
	case len(changedFields(prev, next)) > 0:
		return fmt.Errorf("job: an entry declares nothing, and this write also changes %s", strings.Join(changedFields(prev, next), ", "))
	case actor.Lease == id:
		return refuse(actor, id, "a holder writes its own paths and never enters them")
	case actor.Bound() && !slices.ContainsFunc(types.JobAncestors(rows, id), func(r types.Job) bool { return r.ID == actor.Lease }):
		return refuse(actor, id, "a worker enters only a job forked beneath its own")
	case !prev.State.Live():
		return fmt.Errorf("job: %s is %s, so its paths are free to write and there is nothing to enter", id, prev.State)
	}
	if _, ok := matching(prev.WritePaths, entry.Path); !ok {
		return fmt.Errorf("job: %s is outside the write paths of %s (%s); a path the job does not own is widened into it, never entered",
			entry.Path, id, strings.Join(prev.WritePaths, ", "))
	}
	if open, ok := OpenEntry(prev, entry.Path); ok {
		return fmt.Errorf("job: %s already has an entry for %s, recorded at %s and not yet written; write it before entering again",
			id, open.Path, time.Unix(open.At, 0).UTC().Format(time.RFC3339))
	}
	if len(prev.Entries) >= MaxJobEntries {
		return fmt.Errorf("job: %s has taken its %d entries; resume its holder to make the change, or end its lease with `magus job exit %s`",
			id, MaxJobEntries, id)
	}
	return nil
}

// EntryAdvice is what an entry is answered with: what the entrant may now do, and once.
func EntryAdvice(row types.Job, rel string) string {
	return fmt.Sprintf("entered %s on %s (%d of %d entries): your next write to it passes once its holder has been idle for a minute, and is recorded on the job",
		rel, row.ID, len(row.Entries), MaxJobEntries)
}

// EntriesOf is the entries recorded on job id in rows, nil when rows holds no such job.
func EntriesOf(rows []types.Job, id string) []types.JobEntry {
	if i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id }); i >= 0 {
		return rows[i].Entries
	}
	return nil
}

// OpenEntry is the first entry on row that covers rel and has not been consumed.
func OpenEntry(row types.Job, rel string) (types.JobEntry, bool) {
	for _, e := range row.Entries {
		if e.Consumed == 0 && covers(e.Path, rel) {
			return e, true
		}
	}
	return types.JobEntry{}, false
}

// ConsumeEntry stamps the open entry on job id covering rel as used. An observation, like
// RecordUnattributedWrite: the guard records the write it just let through. A job with no
// open entry for rel is left alone.
func (s *Store) ConsumeEntry(ctx context.Context, id, rel string) error {
	_, err := s.mutate(ctx, id, asObservation, func(cur *types.Job, exists bool, now int64) error {
		if !exists {
			return fmt.Errorf("job: no such lease %q: %w", id, errNoSuchJob)
		}
		for i, e := range cur.Entries {
			if e.Consumed == 0 && covers(e.Path, rel) {
				cur.Entries[i].Consumed = now
				return nil
			}
		}
		return nil
	})
	if errors.Is(err, errNoSuchJob) {
		return nil
	}
	return err
}

// boundaryFields are the declared fields a widen or narrow changes, in changedFields'
// spelling.
var boundaryFields = []string{"write_paths", "deny_paths", "read_paths"}

// widensInPlace reports a declaration that changes a live row's boundaries and nothing
// else it declares, while resetting a state its holder already moved past declared: a
// re-fork that only moves paths. Such a write keeps the stored state, since resetting it
// would hand the holder's job out again. A deadline may move with it, because a fork
// re-stamps jobs.default_timeout. A re-fork that changes no boundary still resets the
// state, which is how a rejected job is handed out again.
func widensInPlace(prev, next types.Job) bool {
	if !prev.State.Live() || prev.State == types.StateDeclared || next.State != types.StateDeclared {
		return false
	}
	moved := false
	for _, field := range changedFields(prev, next) {
		switch {
		case slices.Contains(boundaryFields, field):
			moved = true
		case field != "state" && field != "deadline":
			return false
		}
	}
	return moved
}

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
	err := s.update(ctx, func(f *jobsFile) error {
		i := slices.IndexFunc(f.Jobs, func(e types.Job) bool { return e.ID == id })
		var prev types.Job
		if i >= 0 {
			prev = f.Jobs[i]
			if err := readOnly(prev); err != nil {
				return err
			}
		}
		row := prev.Clone()
		row.ID = id
		now := time.Now().Unix()
		if aerr := apply(&row, i >= 0, now); aerr != nil {
			return aerr
		}
		row = row.Clone()
		row.ID = id
		// Every door into the store declares here, so a record that names no state is
		// stored as declared: the guard reads an empty state as ended.
		if kind == asDeclaration && row.State == "" {
			row.State = types.StateDeclared
		}
		if kind == asDeclaration && i >= 0 && widensInPlace(prev, row) {
			row.State = prev.State
		}
		entering := kind == asDeclaration && requestsEntry(prev, row)
		switch {
		case entering:
			if aerr := authorizeEntry(actor, id, prev, row, i >= 0, f.Jobs); aerr != nil {
				return aerr
			}
			e := &row.Entries[len(row.Entries)-1]
			e.By, e.At, e.Consumed = trail.StampOrigin(ctx, types.Origin{}), now, 0
		case kind.graded():
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
				row.CheckoutRoot = prev.CheckoutRoot
			}
			if kind != asObservation {
				row.Unattributed = prev.Unattributed
			}
			if kind != asObservation && !entering {
				row.Entries = prev.Entries
			}
		case actor.Bound():
			row.ReportedBase, row.BaseVerdict, row.Registered, row.Unattributed = "", "", 0, nil
			row.CheckoutRoot = ""
		}
		if i < 0 {
			row.Entries = nil
		}
		// Only the sweep writes a reason, and a row brought back to life has none.
		row.EndReason = prev.EndReason
		if row.State.Live() {
			row.EndReason = ""
		}
		row.Updated = now
		if (kind == asObservation || entering) && i >= 0 {
			row.Updated = prev.Updated
		}
		row.Created = now
		// The envelope is the store's, like the timestamps: a whole-row write keeps the
		// members a newer magus stored, and the stamp never drops below what it read.
		row.Schema = prev.Schema
		row.Version = max(prev.Version, types.JobSchemaVersion)
		row.Releases = s.releases(ctx, prev, row, now, prev.Registered != 0 && actor.Lease != id)
		if i >= 0 {
			row.Created = prev.Created
			row.RegisteredBy = prev.RegisteredBy
			f.Jobs[i] = row
		} else {
			row.RegisteredBy = trail.StampOrigin(ctx, types.Origin{})
			f.Jobs = append(f.Jobs, row)
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
//
// Every read SWEEPS first: a live row magus can prove is dead is ended as no_return with
// its [types.Job.EndReason], and one line per ended row goes to stderr. See [sweeper.dead]
// for the rules. The sweep is the one write a read makes, and only when a row is dead, so
// a plan with nothing to end is still read without either lock.
func (s *Store) List() ([]types.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := s.read()
	if err != nil {
		return nil, err
	}
	if f, err = s.sweep(f); err != nil {
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
	err := s.update(ctx, func(f *jobsFile) error {
		if len(f.Jobs) > 0 {
			if err := s.archive(*f); err != nil {
				return err
			}
		}
		dropped = len(f.Jobs)
		*f = jobsFile{Schema: f.Schema}
		return nil
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
// inherits is the one left behind last. revoked marks what this write gave up as taken from
// its holder rather than given up by it.
func (s *Store) releases(ctx context.Context, prev, next types.Job, now int64, revoked bool) []types.JobRelease {
	out := slices.DeleteFunc(slices.Clone(prev.Releases), func(r types.JobRelease) bool {
		return slices.Contains(next.WritePaths, r.Path)
	})
	for _, p := range prev.WritePaths {
		if slices.Contains(next.WritePaths, p) {
			continue
		}
		rel := types.JobRelease{Path: p, Digest: s.digest(ctx, p), ReleasedAt: now, Revoked: revoked}
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
//
// A released declaration (`run.go#executeStages`) digests the lines the declaration spans
// in the file now, as the footprint places them, so the next holder inherits that body
// rather than a file other claims on it keep changing. A declaration no line is placed in
// any more is absent.
func (s *Store) digest(ctx context.Context, declared string) string {
	p, decl := types.SplitClaim(declared)
	if decl != "" {
		return s.declarationDigest(ctx, p, decl)
	}
	return s.fileDigest(ctx, p)
}

// fileDigest is digest for a path SplitClaim already cut.
func (s *Store) fileDigest(ctx context.Context, declared string) string {
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
		// released fifo would wedge every ledger operation in the server.
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

// declarationDigest is digest for one declaration of the file at p. The file passes every
// check digest makes of a whole file before a line of it is placed.
func (s *Store) declarationDigest(ctx context.Context, p, decl string) string {
	if whole := s.fileDigest(ctx, p); !strings.HasPrefix(whole, "sha256:") {
		return whole
	}
	root, err := filepath.EvalSymlinks(s.root)
	if err != nil {
		return types.DigestUnreadable
	}
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
	if err != nil {
		return types.DigestUnreadable
	}
	res, err := vcs.Resolve(ctx, s.root, "", types.VCSOptions{})
	if err != nil || res.VCS == nil {
		return types.DigestUnreadable
	}
	regions, err := res.VCS.RegionsBetween(ctx, s.root, path.Clean(p), nil, body)
	if err != nil {
		return types.DigestUnreadable
	}
	lines := strings.SplitAfter(string(body), "\n")
	var spanned strings.Builder
	found := false
	for _, r := range regions {
		if !types.NamesDeclaration(decl, r.Declaration) {
			continue
		}
		found = true
		for n := r.Lines[0]; n <= r.Lines[1] && n-1 < len(lines); n++ {
			spanned.WriteString(lines[n-1])
		}
	}
	if !found {
		return types.DigestAbsent
	}
	sum := sha256.Sum256([]byte(spanned.String()))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// maxDigestBytes bounds one release digest, because the hash is computed while the store
// holds its mutex. 32 MiB is far above the source files a lease actually releases and far
// below the build artifact that would otherwise stall every other ledger caller for as
// long as it takes to read it; a path over the cap records unreadable, which is what it
// is from the reader's side.
const maxDigestBytes = 32 << 20

// doc is the ledger file. Every rewrite goes through its Update, under the OS file lock
// beside jobs.json, so a read-modify-write cannot interleave with one from another magus
// process. The lock wait is BOUNDED, where the workspace project locks (magus/lock.go)
// wait forever with a heartbeat: their holder is a build that may run for hours, and a
// ledger write is a small file rewrite, so a longer wait means a stuck holder.
func (s *Store) doc() file.Doc[jobsFile] {
	return file.Doc[jobsFile]{Path: s.path, Decode: s.decode, Encode: encodeJobs}
}

// update runs fn over the ledger as it stands under the file lock and writes what fn
// leaves. fn returning an error, file.SkipWrite included, writes nothing.
func (s *Store) update(ctx context.Context, fn func(*jobsFile) error) error {
	if s.err != nil {
		return s.err
	}
	return s.doc().Update(ctx, fn)
}

// read loads the file. An absent file is an empty ledger, not a failure: nothing has
// been recorded yet for this repository.
func (s *Store) read() (jobsFile, error) {
	if s.err != nil {
		return jobsFile{}, s.err
	}
	return s.doc().Load()
}

// decode parses the ledger.
//
// A row newer than this magus is read like any other: the members it does not declare
// ride in the row's Schema.Unknown and go back out on the next write, and authorizeRow and
// jobOverlaps need only fields every version carries. A row requiring a feature this magus
// lacks is read too, and only a write to it is refused (see readOnly).
func (s *Store) decode(raw []byte, f *jobsFile) error {
	if err := json.Unmarshal(raw, f); err != nil {
		return err
	}
	if lacks := f.Unmet(types.JobSchema.Features()); lacks != nil {
		return fmt.Errorf("job: %s requires %s, which this magus (schema %d) lacks; update magus",
			s.path, quoteAll(lacks), types.JobSchemaVersion)
	}
	if err := foldStoredNames(f.Jobs); err != nil {
		return fmt.Errorf("job: %s: %w", s.path, err)
	}
	return nil
}

// encodeJobs serializes the ledger. Every row, touched this call or not, carries its
// Schema.Unknown back out, and the file's stamp never drops below what it read.
func encodeJobs(f jobsFile) ([]byte, error) {
	f.Version = max(f.Version, types.JobSchemaVersion)
	rows, err := mirrorLegacyGoals(f.Jobs)
	if err != nil {
		return nil, err
	}
	f.Jobs = rows
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// mirrorLegacyGoals writes each row's goals under their old name too, on a copy.
//
// compat: see foldStoredNames. One store serves every magus on the machine, and one that
// reads only completion_gates would grade a row's goals as absent and pass it on its check.
func mirrorLegacyGoals(rows []types.Job) ([]types.Job, error) {
	out := slices.Clone(rows)
	for i := range out {
		if len(out[i].Goals) == 0 {
			continue
		}
		raw, err := json.Marshal(out[i].Goals)
		if err != nil {
			return nil, err
		}
		out[i].Unknown = maps.Clone(out[i].Unknown)
		if out[i].Unknown == nil {
			out[i].Unknown = map[string]json.RawMessage{}
		}
		out[i].Unknown["completion_gates"] = raw
	}
	return out, nil
}

// Delete removes ONE row and returns it, or reports that no such row exists.
//
// A row that is over is still a row: `job exit` moves a job to a terminal state, which is
// the RECORD of what happened and is what a later reader wants. Delete is for a row that
// should never have been written -- a demo, a typo, a plan abandoned before it began --
// and it is the only way to take one out without Clear taking every other lease's row
// with it.
//
// It refuses a TERMINAL row by default. Deleting the record of a job that actually ran
// destroys the only account of it, and the caller who wants that says so with force. A
// live row is the opposite case: nothing has happened yet, so there is nothing to lose.
//
// Archived first, exactly as Clear archives, so a delete is recoverable from the sibling
// file rather than only from whatever the caller remembers.
func (s *Store) Delete(ctx context.Context, id string, force bool) (types.Job, error) {
	id = strings.TrimSpace(id)
	if err := authorizeDelete(s.Actor(), id); err != nil {
		return types.Job{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var dropped types.Job
	err := s.update(ctx, func(f *jobsFile) error {
		i := slices.IndexFunc(f.Jobs, func(row types.Job) bool { return row.ID == id })
		if i < 0 {
			return fmt.Errorf("job: there is no job %q", id)
		}
		dropped = f.Jobs[i]
		if err := readOnly(dropped); err != nil {
			return err
		}
		if dropped.State.Terminal() && !force {
			return fmt.Errorf("job: %s is %s, and that row is the record of what happened."+
				" Delete it anyway with --force, or leave it where a later reader can find it",
				id, dropped.State)
		}
		if err := s.archive(*f); err != nil {
			return err
		}
		f.Jobs = slices.Delete(f.Jobs, i, i+1)
		return nil
	})
	if err != nil {
		return types.Job{}, err
	}
	return dropped, nil
}
