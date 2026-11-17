//go:build unix

// The witnessed runs are sh commands, so these run only where sh does.

package job

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
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

func (s *WitnessSuite) witness(cmd *exec.Cmd) (RunRef, error) {
	return s.store.Witness(s.T().Context(), "j1", "tests", cmd)
}

func (s *WitnessSuite) witnessed() RunRef {
	ref, err := s.witness(s.command())
	s.Require().NoError(err)
	return ref
}

func (s *WitnessSuite) refusal(err error) WitnessRefusal {
	refused, ok := errors.AsType[*WitnessRefusedError](err)
	s.Require().True(ok, err)
	return refused.Refusal
}

func (s *WitnessSuite) output(ref RunRef) string {
	runs, err := s.store.runs()
	s.Require().NoError(err)
	return runs[ref].output
}

func digestOf(out string) string {
	sum := sha256.Sum256([]byte(out))
	return "sha256:" + hex.EncodeToString(sum[:])
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

	ref, err := s.witness(cmd)
	s.Require().NoError(err)

	s.Equal("ok\n", out.String())
	runs, err := s.store.runs()
	s.Require().NoError(err)
	s.Equal(digestOf("ok\n"), runs[ref].output)
	s.Equal(s.argv, runs[ref].argv)
}

func (s *WitnessSuite) TestAnotherProgramUnderTheChecksArgvIsRefused() {
	cmd := s.command()
	cmd.Path = "/bin/true"

	_, err := s.witness(cmd)

	s.Equal(RefusalOtherProgram, s.refusal(err))
}

func (s *WitnessSuite) TestAnEnvTheCheckDoesNotNameIsRefused() {
	cmd := s.command()
	cmd.Env = append(os.Environ(), "GOFLAGS=-run=TestOne")

	_, err := s.witness(cmd)

	s.Equal(RefusalEnv, s.refusal(err))
}

func (s *WitnessSuite) TestProcessAttributesAreRefused() {
	cmd := s.command()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	_, err := s.witness(cmd)

	s.Equal(RefusalSysProcAttr, s.refusal(err))
}

func (s *WitnessSuite) TestExtraFilesAreRefused() {
	cmd := s.command()
	r, w, err := os.Pipe()
	s.Require().NoError(err)
	s.T().Cleanup(func() { _, _ = r.Close(), w.Close() })
	cmd.ExtraFiles = []*os.File{w}

	_, err = s.witness(cmd)

	s.Equal(RefusalExtraFiles, s.refusal(err))
}

func (s *WitnessSuite) TestAnotherCommandsProcessIsRefused() {
	other := s.command()
	s.Require().NoError(other.Run())
	cmd := s.command()
	cmd.Process, cmd.ProcessState = other.Process, other.ProcessState

	_, err := s.witness(cmd)

	s.Equal(RefusalStarted, s.refusal(err))
}

func (s *WitnessSuite) TestAHandBuiltProcessStateIsRefused() {
	cmd := s.command()
	cmd.ProcessState = &os.ProcessState{}

	_, err := s.witness(cmd)

	s.Equal(RefusalStarted, s.refusal(err))
}

// swapper swaps cmd's Stdout away and back on its first write, as a caller redirecting
// output past the digest would.
type swapper struct {
	cmd     *exec.Cmd
	swapped bool
	out     bytes.Buffer
}

func (w *swapper) Write(p []byte) (int, error) {
	if !w.swapped {
		w.swapped = true
		held := w.cmd.Stdout
		w.cmd.Stdout = io.Discard
		w.cmd.Stdout = held
	}
	return w.out.Write(p)
}

func (s *WitnessSuite) TestARedirectSwappedAndRestoredStillDigestsTheWholeOutput() {
	s.argv = []string{"sh", "-c", "echo one; echo two"}
	cmd := s.command()
	w := &swapper{cmd: cmd}
	cmd.Stdout = w

	ref, err := s.witness(cmd)
	s.Require().NoError(err)

	s.True(w.swapped)
	s.Equal(digestOf("one\ntwo\n"), s.output(ref))
}

func (s *WitnessSuite) TestACommandAlreadyRunIsRefused() {
	cmd := s.command()
	s.Require().NoError(cmd.Run())

	_, err := s.witness(cmd)

	s.Equal(RefusalStarted, s.refusal(err))
}

func (s *WitnessSuite) TestANarrowedCommandIsRefused() {
	cmd := exec.Command("sh", "-c", "echo ok", "narrowed")
	cmd.Dir = s.work

	_, err := s.witness(cmd)

	s.Equal(RefusalOtherCheck, s.refusal(err))
}

func (s *WitnessSuite) TestACommandInAnotherDirectoryIsRefused() {
	cmd := s.command()
	cmd.Dir = s.T().TempDir()

	_, err := s.witness(cmd)

	s.Equal(RefusalOtherCheck, s.refusal(err))
}

func (s *WitnessSuite) TestNoCommandIsRefused() {
	_, err := s.witness(nil)

	s.Equal(RefusalNoCommand, s.refusal(err))
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func (s *WitnessSuite) TestAFailedOutputCopyIsRefused() {
	cmd := s.command()
	cmd.Stdout = failingWriter{}

	_, err := s.witness(cmd)

	s.Equal(RefusalUnfinished, s.refusal(err))
}

func (s *WitnessSuite) TestARunTheTreeMovedUnderIsNotVerified() {
	s.argv = []string{"sh", "-c", "echo ok"}
	cmd := s.command()
	cmd.Stdout = treeMover{s: s}

	ref, err := s.witness(cmd)
	s.Require().NoError(err)

	g := s.grade(ref)
	s.False(g.Verified)
	s.Contains(g.Violations, "the tree changed while the check ran, so no one tree is what it read")
}

// treeMover moves the suite's tree while the run writes, as an edit during a check does.
type treeMover struct{ s *WitnessSuite }

func (m treeMover) Write(p []byte) (int, error) {
	m.s.tree = "tree-2"
	return len(p), nil
}

func (s *WitnessSuite) TestAStoreWithNoCheckCommandRefuses() {
	st, err := Open(s.dir, writable(WithTree(s.store.tree))...)
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
