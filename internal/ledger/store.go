// Package ledger persists the delegation ledger: the rows an orchestrating agent
// declares about the plan it is running, kept where a human can read them.
//
// It RECORDS AND NOTHING ELSE. Nothing here gates a run, blocks a write, or derives a
// verdict from a row - see types.DelegationUnit for why enforcement would make the
// ledger something agents route around. The store's whole job is that a plan an agent
// stated in a prompt stops being trapped in one session's transcript.
//
// ONE PLAN PER WORKSPACE, and no history of past plans: Clear wipes the rows so the
// next plan starts empty. A plan that ended is not archived anywhere, which is the v1
// scope on purpose - keeping every past plan means deciding what identifies one, and
// nothing in the vocabulary names a plan yet.
package ledger

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// ErrNoID reports a unit with no id. The id is what Put upserts on, so a row without
// one could never be updated or referred to again - it is unaddressable, not merely
// incomplete.
var ErrNoID = errors.New("ledger: a unit needs an id")

// Store is the workspace's delegation ledger, a single JSON file under the cache
// directory. Every operation reads the file, acts, and writes it back, so a Store is
// cheap to construct and holds no state between calls beyond the path and its lock.
//
// The mutex serializes writers within one process, and only for the span of ONE call: a
// caller that merges fields by reading with List and writing back with Put takes the
// lock twice, and two such merges on one id then lose whichever field the second one
// read before the first wrote. [Store.Update] is that merge done under a single
// acquisition, and it is what a field-at-a-time writer (the magus_ledger MCP tool) has
// to use.
//
// It does NOT lock across processes: the CLI, the daemon, and an MCP client are separate
// processes that can each hold a Store on the same file, and a concurrent
// read-modify-write from two of them can drop a row. That is accepted for v1 because the
// writer is one orchestrating agent by construction - the ledger has a single author by
// definition of what it records.
type Store struct {
	mu   sync.Mutex
	path string
	root string
}

// NewStore returns the ledger rooted at stateDir, writing <stateDir>/ledger/units.json.
// It touches no disk: the file is created by the first Put.
//
// root is the workspace a row's paths are relative to, and it is read for one purpose:
// digesting a path at the moment a unit releases it (see Update). A Store built with an
// empty root still records releases - it just cannot say what was in them.
func NewStore(stateDir, root string) *Store {
	return &Store{path: filepath.Join(stateDir, "ledger", "units.json"), root: root}
}

// unitsFile is the on-disk envelope. An object rather than a bare array so a later
// field (a plan identity, a schema version) can be added without every existing reader
// failing to parse the file.
type unitsFile struct {
	Units []types.DelegationUnit `json:"units"`
}

// Put records one unit, replacing any row with the same id IN PLACE. Position is
// preserved on update because the ledger is a table a person reads top to bottom, and
// a row that jumped to the bottom every time its state changed would reorder itself
// exactly when it is being watched.
//
// It stamps Created on the first write and Updated on every write, ignoring whatever
// the caller passed for either. The stored row is returned.
func (s *Store) Put(u types.DelegationUnit) (types.DelegationUnit, error) {
	return s.Update(u.ID, func(cur *types.DelegationUnit) { *cur = u })
}

// Update applies apply to the row with this id and writes the result back, all under a
// SINGLE lock acquisition. That is the whole point: a merge spread across List and Put
// releases the lock in between, so two concurrent writers advancing different fields of
// one row each read it before the other wrote, and the second write reverts the first.
//
// The row is CREATED when absent, matching Put: apply then sees a zero unit carrying
// only the id, so declaring a unit and advancing one are the same call. Created is
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
func (s *Store) Update(id string, apply func(*types.DelegationUnit)) (types.DelegationUnit, error) {
	if strings.TrimSpace(id) == "" {
		return types.DelegationUnit{}, ErrNoID
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := s.read()
	if err != nil {
		return types.DelegationUnit{}, err
	}
	i := slices.IndexFunc(f.Units, func(e types.DelegationUnit) bool { return e.ID == id })
	var prev types.DelegationUnit
	if i >= 0 {
		prev = f.Units[i]
	}
	u := prev.Clone()
	u.ID = id
	apply(&u)
	u = u.Clone()
	u.ID = id
	now := time.Now().Unix()
	u.Updated = now
	u.Created = now
	u.Releases = s.releases(prev, u, now)
	if i >= 0 {
		u.Created = prev.Created
		f.Units[i] = u
	} else {
		f.Units = append(f.Units, u)
	}
	if err := s.write(f); err != nil {
		return types.DelegationUnit{}, err
	}
	return u.Clone(), nil
}

// List returns every row in the order it was first recorded. The rows are copies, so a
// caller may keep or mutate them without reaching back into the file's next read.
func (s *Store) List() ([]types.DelegationUnit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := s.read()
	if err != nil {
		return nil, err
	}
	out := make([]types.DelegationUnit, len(f.Units))
	for i, u := range f.Units {
		out[i] = u.Clone()
	}
	return out, nil
}

// Clear drops every row, which is how a fresh plan starts. Clearing an empty or absent
// ledger is not an error - the caller asked for an empty ledger and got one.
func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.write(unitsFile{})
}

// releases carries the row's recorded releases forward and adds the paths this write
// gave up - the ones prev owned and next does not.
//
// A path that is owned again drops OUT of the list: a row saying it both owns and has
// released the same path tells a reader nothing they can act on. A path released twice
// keeps its position and takes the NEWER digest, because the version the next agent
// inherits is the one left behind last.
func (s *Store) releases(prev, next types.DelegationUnit, now int64) []types.DelegationRelease {
	out := slices.DeleteFunc(slices.Clone(prev.Releases), func(r types.DelegationRelease) bool {
		return slices.Contains(next.OwnedPaths, r.Path)
	})
	for _, p := range prev.OwnedPaths {
		if slices.Contains(next.OwnedPaths, p) {
			continue
		}
		rel := types.DelegationRelease{Path: p, Digest: s.digest(p), ReleasedAt: now}
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
// Everything that is not a readable file inside the root answers with one of the two
// documented markers rather than a hash - types.ReleaseAbsent and types.ReleaseDir say
// which. A path escaping the root is absent by the same rule: the ledger describes this
// workspace, so a row is never handed a digest of something outside it.
func (s *Store) digest(declared string) string {
	if s.root == "" {
		return types.ReleaseAbsent
	}
	full := filepath.Join(s.root, filepath.FromSlash(declared))
	rel, err := filepath.Rel(s.root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return types.ReleaseAbsent
	}
	info, err := os.Stat(full)
	switch {
	case err != nil:
		return types.ReleaseAbsent
	case info.IsDir():
		return types.ReleaseDir
	}
	fh, err := os.Open(full)
	if err != nil {
		return types.ReleaseAbsent
	}
	defer fh.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, fh); err != nil {
		return types.ReleaseAbsent
	}
	return "sha256:" + hex.EncodeToString(sum.Sum(nil))
}

// read loads the file. An absent file is an empty ledger, not a failure: nothing has
// been recorded yet in this workspace.
func (s *Store) read() (unitsFile, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return unitsFile{}, nil
	}
	if err != nil {
		return unitsFile{}, err
	}
	var f unitsFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return unitsFile{}, err
	}
	return f, nil
}

// write replaces the file atomically, so a reader never sees a half-written ledger.
func (s *Store) write(f unitsFile) error {
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return file.WriteFileAtomic(s.path, append(raw, '\n'), 0o644)
}
