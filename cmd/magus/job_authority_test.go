package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// pinCheckoutBinary records bin, hashed as it stands, as the binary of a live
// row's checkout in the job store session reads.
func pinCheckoutBinary(t *testing.T, session, bin string) {
	t.Helper()
	digest, err := job.BinaryDigest(bin)
	require.NoError(t, err)
	_, cacheDir := seedJobStore(t, session, types.Job{ID: "pinned", State: types.StateDeclared, WritePaths: []string{"a.go"}})
	store := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: session})
	_, err = store.RecordWorktree(t.Context(), "pinned", types.JobWorktree{Root: filepath.Dir(bin), Binary: bin, BinaryDigest: digest})
	require.NoError(t, err)
}

// JobAuthoritySuite pins `magus job pin` and `magus job advance`: the root
// re-pins a checkout's binary and moves a row's authority, and a worker does
// neither for itself.
type JobAuthoritySuite struct {
	suite.Suite
	repo     *testkit.Repo
	cacheDir string
	bin      string
}

func TestJobAuthoritySuite(t *testing.T) {
	suite.Run(t, new(JobAuthoritySuite))
}

func (s *JobAuthoritySuite) SetupTest() {
	row := leaseRow("pinned-work", "")
	row.State = types.StateDeclared
	s.repo, s.cacheDir = execRepositoryFixture(s.T(), row)
	s.bin = filepath.Join(s.repo.Root(), "magus")
	s.Require().NoError(os.WriteFile(s.bin, []byte("#!/bin/sh\necho one\n"), 0o755))
	_, err := s.store().RecordWorktree(s.T().Context(), row.ID, types.JobWorktree{Root: s.repo.Root(), Binary: s.bin})
	s.Require().NoError(err)
}

func (s *JobAuthoritySuite) store() *job.Store {
	return job.NewStore(job.Location{StateBase: fixture.StateBase(s.T()), CacheDir: s.cacheDir, Root: s.repo.Root(), Actor: &job.Actor{}})
}

func (s *JobAuthoritySuite) row() types.Job {
	rows, err := s.store().List()
	s.Require().NoError(err)
	for _, r := range rows {
		if r.ID == "pinned-work" {
			return r
		}
	}
	s.FailNow("no row pinned-work")
	return types.Job{}
}

func (s *JobAuthoritySuite) run(args ...string) error {
	var err error
	captureStdout(s.T(), func() { err = jobCommand(s.T().Context(), s.repo.Root(), args) })
	return err
}

func (s *JobAuthoritySuite) TestPinRecordsTheHashTheBinaryHasNow() {
	s.Require().NoError(os.WriteFile(s.bin, []byte("#!/bin/sh\necho relinked\n"), 0o755))
	s.Require().NoError(s.run("pin", "pinned-work"))
	want, err := job.BinaryDigest(s.bin)
	s.Require().NoError(err)
	s.Equal(want, s.row().Worktree.BinaryDigest)
	refusal, err := s.store().PinRefusal(s.bin)
	s.Require().NoError(err)
	s.Empty(refusal, "the hook hands calls to the binary it pinned")
}

func (s *JobAuthoritySuite) TestAdvanceMovesTheAuthorityToHead() {
	head := s.repo.Commit("landed", nil)
	s.Require().NoError(s.run("advance", "pinned-work"))
	s.Equal(head, s.row().Authority)
}

func (s *JobAuthoritySuite) TestAWorkerPinsNothing() {
	bindCheckout(s.T(), s.cacheDir, "pinned-work")
	err := s.run("pin", "pinned-work")
	s.Require().Error(err)
	s.Contains(err.Error(), "a worker that pinned its own build would pick its judge")
	s.Empty(s.row().Worktree.BinaryDigest)
}

func (s *JobAuthoritySuite) TestAWorkerAdvancesNothing() {
	before := s.row().Authority
	s.repo.Commit("its own", nil)
	bindCheckout(s.T(), s.cacheDir, "pinned-work")
	err := s.run("advance", "pinned-work")
	s.Require().Error(err)
	s.Contains(err.Error(), "only the root advances a row's authority, or the integrator that lands it")
	s.Equal(before, s.row().Authority)
}

func (s *JobAuthoritySuite) TestPinNeedsAJob() {
	s.ErrorContains(s.run("pin"), "magus job pin: requires at least one job")
}

func (s *JobAuthoritySuite) TestAdvanceNeedsAJob() {
	s.ErrorContains(s.run("advance"), "magus job advance: requires at least one job")
}

func (s *JobAuthoritySuite) TestTheIntegratorAdvancesARowItLands() {
	integrator := leaseRow("landing", "")
	integrator.State, integrator.Integrates = types.StateDeclared, []string{"pinned-work"}
	_, err := s.store().Update(s.T().Context(), integrator.ID, func(cur *types.Job) { *cur = integrator })
	s.Require().NoError(err)
	head := s.repo.Commit("landed", nil)
	bindCheckout(s.T(), s.cacheDir, "landing")
	s.Require().NoError(s.run("advance", "pinned-work"))
	s.Equal(head, s.row().Authority)
}
