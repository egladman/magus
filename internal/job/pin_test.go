package job

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/testkit"
)

// PinSuite pins `magus job pin` and `magus job advance`: the root re-pins a checkout's
// binary and moves a row's authority, and a worker does neither for itself.
type PinSuite struct {
	suite.Suite
	repo *testkit.Repo
	loc  Location
	bin  string
}

func TestPinSuite(t *testing.T) {
	suite.Run(t, new(PinSuite))
}

func (s *PinSuite) SetupTest() {
	s.repo = testkit.NewRepository(s.T(), map[string]string{"a.txt": "a\n"})
	s.loc = Location{StateBase: s.T().TempDir(), CacheDir: s.T().TempDir(), Root: s.repo.Root(), Actor: &Actor{}}
	s.bin = filepath.Join(s.repo.Root(), "magus")
	s.Require().NoError(os.WriteFile(s.bin, []byte("#!/bin/sh\necho one\n"), 0o755))
	row := job.Job{ID: "pinned-work", State: job.StateDeclared}
	_, err := s.store().Update(s.T().Context(), row.ID, func(cur *job.Job) { *cur = row })
	s.Require().NoError(err)
	_, err = s.store().RecordWorktree(s.T().Context(), row.ID, job.Worktree{Root: s.repo.Root(), Binary: s.bin})
	s.Require().NoError(err)
}

func (s *PinSuite) store() *Store { return NewStore(s.loc) }

func (s *PinSuite) row() job.Job {
	rows, err := s.store().List(s.T().Context())
	s.Require().NoError(err)
	for _, r := range rows {
		if r.ID == "pinned-work" {
			return r
		}
	}
	s.FailNow("no row pinned-work")
	return job.Job{}
}

// workerStore binds the checkout to id and returns the store a worker there opens: its
// actor is the one the binding names, not an unbound root's.
func (s *PinSuite) workerStore(id string) *Store {
	loc := s.loc
	loc.Actor = nil
	s.Require().NoError(NewStore(Location{StateBase: loc.StateBase, CacheDir: loc.CacheDir}).Bind(Caller{}, id))
	return NewStore(loc)
}

func (s *PinSuite) TestPinRecordsTheHashTheBinaryHasNow() {
	s.Require().NoError(os.WriteFile(s.bin, []byte("#!/bin/sh\necho relinked\n"), 0o755))
	_, err := PinOrAdvance(s.T().Context(), s.store(), "pin", []string{"pinned-work"})
	s.Require().NoError(err)
	want, err := BinaryDigest(s.bin)
	s.Require().NoError(err)
	s.Equal(want, s.row().Worktree.BinaryDigest)
	refusal, err := s.store().PinRefusal(s.T().Context(), s.bin)
	s.Require().NoError(err)
	s.Empty(refusal, "the hook hands calls to the binary it pinned")
}

func (s *PinSuite) TestAdvanceMovesTheAuthorityToHead() {
	head := s.repo.Commit("landed", nil)
	_, err := PinOrAdvance(s.T().Context(), s.store(), "advance", []string{"pinned-work"})
	s.Require().NoError(err)
	s.Equal(head, s.row().Authority)
}

func (s *PinSuite) TestAWorkerPinsNothing() {
	_, err := PinOrAdvance(s.T().Context(), s.workerStore("pinned-work"), "pin", []string{"pinned-work"})
	s.Require().Error(err)
	s.Contains(err.Error(), "a worker that pinned its own build would pick its judge")
	s.Empty(s.row().Worktree.BinaryDigest)
}

func (s *PinSuite) TestAWorkerAdvancesNothing() {
	before := s.row().Authority
	s.repo.Commit("its own", nil)
	_, err := PinOrAdvance(s.T().Context(), s.workerStore("pinned-work"), "advance", []string{"pinned-work"})
	s.Require().Error(err)
	s.Contains(err.Error(), "only the root advances a row's authority, or the integrator that lands it")
	s.Equal(before, s.row().Authority)
}

func (s *PinSuite) TestTheIntegratorAdvancesARowItLands() {
	integrator := job.Job{ID: "landing", State: job.StateDeclared, Integrates: []string{"pinned-work"}}
	_, err := s.store().Update(s.T().Context(), integrator.ID, func(cur *job.Job) { *cur = integrator })
	s.Require().NoError(err)
	head := s.repo.Commit("landed", nil)
	_, err = PinOrAdvance(s.T().Context(), s.workerStore("landing"), "advance", []string{"pinned-work"})
	s.Require().NoError(err)
	s.Equal(head, s.row().Authority)
}

func (s *PinSuite) TestPrintPinnedNamesEachRowTheVerbChanged() {
	rows, err := PinOrAdvance(s.T().Context(), s.store(), "pin", []string{"pinned-work"})
	s.Require().NoError(err)
	var out bytes.Buffer
	PrintPinned(&out, "pin", rows)
	s.Equal(fmt.Sprintf("pinned %s for pinned-work: %s\n", s.bin, s.row().Worktree.BinaryDigest), out.String())
}
