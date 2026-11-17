//go:build unix

// The witnessed runs are sh commands, so these run only where sh does.

package job

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/suite"
)

type WitnessSuite struct{ gradingSuite }

func TestWitnessSuite(t *testing.T) { suite.Run(t, new(WitnessSuite)) }

func (s *WitnessSuite) SetupTest() {
	s.gradingSuite.SetupTest()
	s.declare(checkGoalAccepting("tests", BasisWitnessed))
}

func (s *WitnessSuite) command() *exec.Cmd {
	cmd := exec.Command(s.argv[0], s.argv[1:]...)
	cmd.Dir = s.work
	return cmd
}

func (s *WitnessSuite) attach(cmd *exec.Cmd) *Witness {
	w, err := s.store.Witness(s.T().Context(), "j1", "tests", cmd)
	s.Require().NoError(err)
	return w
}

// witnessed is the caller's whole side: attach, run its own command, close.
func (s *WitnessSuite) witnessed() RunRef {
	cmd := s.command()
	w := s.attach(cmd)
	_ = cmd.Run()
	ref, err := w.Close(s.T().Context())
	s.Require().NoError(err)
	return ref
}

func (s *WitnessSuite) refusal(err error) WitnessRefusal {
	refused, ok := errors.AsType[*WitnessRefusedError](err)
	s.Require().True(ok, err)
	return refused.Refusal
}

func (s *WitnessSuite) TestAWitnessedPassVerifies() {
	g := s.grade(s.witnessed())

	s.True(g.Verified, g.Violations)
	s.True(g.Passed)
	s.Equal(BasisWitnessed, g.Basis)
}

func (s *WitnessSuite) TestAWitnessedFailureFails() {
	s.argv = []string{"sh", "-c", "echo FAIL; exit 1"}

	g := s.grade(s.witnessed())

	s.False(g.Verified)
	s.False(g.Passed)
	s.Contains(g.Violations, "the check exited 1")
}

func (s *WitnessSuite) TestTheCallerStillGetsTheOutputAndTheStoreItsDigest() {
	cmd := s.command()
	var out bytes.Buffer
	cmd.Stdout = &out
	w := s.attach(cmd)
	s.Require().NoError(cmd.Run())
	ref, err := w.Close(s.T().Context())
	s.Require().NoError(err)

	s.Equal("ok\n", out.String())
	runs, err := s.store.runs()
	s.Require().NoError(err)
	sum := sha256.Sum256([]byte("ok\n"))
	s.Equal("sha256:"+hex.EncodeToString(sum[:]), runs[ref].output)
	s.Equal(s.argv, runs[ref].argv)
}

func (s *WitnessSuite) TestAHandBuiltProcessStateIsRefused() {
	cmd := s.command()
	w := s.attach(cmd)
	cmd.Process, cmd.ProcessState = &os.Process{Pid: 4242}, &os.ProcessState{}

	_, err := w.Close(s.T().Context())

	s.Equal(RefusalOtherProcess, s.refusal(err))
}

func (s *WitnessSuite) TestACommandNeverRunIsRefused() {
	w := s.attach(s.command())

	_, err := w.Close(s.T().Context())

	s.Equal(RefusalNeverWaited, s.refusal(err))
}

func (s *WitnessSuite) TestACommandAlreadyRunIsRefused() {
	cmd := s.command()
	s.Require().NoError(cmd.Run())

	_, err := s.store.Witness(s.T().Context(), "j1", "tests", cmd)

	s.Equal(RefusalStarted, s.refusal(err))
}

func (s *WitnessSuite) TestANarrowedCommandIsRefused() {
	cmd := exec.Command("sh", "-c", "echo ok", "narrowed")
	cmd.Dir = s.work

	_, err := s.store.Witness(s.T().Context(), "j1", "tests", cmd)

	s.Equal(RefusalOtherCheck, s.refusal(err))
}

func (s *WitnessSuite) TestACommandInAnotherDirectoryIsRefused() {
	cmd := s.command()
	cmd.Dir = s.T().TempDir()

	_, err := s.store.Witness(s.T().Context(), "j1", "tests", cmd)

	s.Equal(RefusalOtherCheck, s.refusal(err))
}

func (s *WitnessSuite) TestRedirectedOutputIsRefused() {
	cmd := s.command()
	w := s.attach(cmd)
	cmd.Stdout = nil
	_ = cmd.Run()

	_, err := w.Close(s.T().Context())

	s.Equal(RefusalRedirected, s.refusal(err))
}

func (s *WitnessSuite) TestASecondCloseIsRefused() {
	cmd := s.command()
	w := s.attach(cmd)
	s.Require().NoError(cmd.Run())
	_, err := w.Close(s.T().Context())
	s.Require().NoError(err)

	_, err = w.Close(s.T().Context())

	s.Equal(RefusalClosed, s.refusal(err))
}

func (s *WitnessSuite) TestARunTheTreeMovedUnderIsNotVerified() {
	cmd := s.command()
	w := s.attach(cmd)
	s.Require().NoError(cmd.Run())
	s.tree = "tree-2"
	ref, err := w.Close(s.T().Context())
	s.Require().NoError(err)

	g := s.grade(ref)

	s.False(g.Verified)
	s.Contains(g.Violations, "the tree changed while the check ran, so no one tree is what it read")
}

func (s *WitnessSuite) TestAStoreWithNoCheckCommandRefuses() {
	st, err := Open(s.dir, WithTree(s.store.tree))
	s.Require().NoError(err)

	_, err = st.Witness(s.T().Context(), "j1", "tests", s.command())

	refused, ok := errors.AsType[*GradingUnconfiguredError](err)
	s.Require().True(ok, err)
	s.Equal("WithCheckCommand", refused.Option)
}

func (s *WitnessSuite) TestAGoalThatAcceptsOnlyReportedIsNotVerifiedByAWitnessedRun() {
	s.declare(checkGoalAccepting("tests", BasisReported))

	g := s.grade(s.witnessed())

	s.False(g.Verified)
	s.Contains(g.Violations, "the run is witnessed and this goal accepts only [reported]")
}
