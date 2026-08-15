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
	"errors"
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
// The mutex serializes writers within one process. It does NOT lock across processes:
// the CLI, the daemon, and an MCP client are separate processes that can each hold a
// Store on the same file, and a concurrent read-modify-write from two of them can drop
// a row. That is accepted for v1 because the writer is one orchestrating agent by
// construction - the ledger has a single author by definition of what it records.
type Store struct {
	mu   sync.Mutex
	path string
}

// NewStore returns the ledger rooted at stateDir, writing <stateDir>/ledger/units.json.
// It touches no disk: the file is created by the first Put.
func NewStore(stateDir string) *Store {
	return &Store{path: filepath.Join(stateDir, "ledger", "units.json")}
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
	if strings.TrimSpace(u.ID) == "" {
		return types.DelegationUnit{}, ErrNoID
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := s.read()
	if err != nil {
		return types.DelegationUnit{}, err
	}
	now := time.Now().Unix()
	u = u.Clone()
	u.Updated = now
	u.Created = now
	if i := slices.IndexFunc(f.Units, func(e types.DelegationUnit) bool { return e.ID == u.ID }); i >= 0 {
		u.Created = f.Units[i].Created
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
