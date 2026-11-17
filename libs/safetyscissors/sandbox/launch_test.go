package sandbox

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/suite"
)

// Confines and Command follow the mode against what the host probe answers.
type ConfinesSuite struct{ suite.Suite }

func TestConfinesSuite(t *testing.T) { suite.Run(t, new(ConfinesSuite)) }

func (s *ConfinesSuite) policy(level int, err error, opts ...Option) *Policy {
	p, newErr := New(nil, append(opts, WithKernelProbe(func() (int, error) { return level, err }))...)
	s.Require().NoError(newErr)
	return p
}

func (s *ConfinesSuite) TestANilPolicyConfinesNothing() {
	var p *Policy
	ok, err := p.Confines()
	s.Require().NoError(err)
	s.False(ok)
}

func (s *ConfinesSuite) TestRequiredIsTheDefault() {
	_, err := s.policy(0, &UnsupportedError{}).Confines()
	var unconfined *UnconfinedError
	s.Require().ErrorAs(err, &unconfined)
	var unsupported *UnsupportedError
	s.ErrorAs(err, &unsupported, "the host's answer stays reachable")
	s.Contains(err.Error(), "WithMode(BestEffort)", "the refusal names the opt-out")
}

func (s *ConfinesSuite) TestRequiredRefusesBelowTheLevelItNeeds() {
	_, err := s.policy(requiredLevel-1, nil).Confines()
	var unconfined *UnconfinedError
	s.Require().ErrorAs(err, &unconfined)
	s.Equal(UnconfinedError{Have: requiredLevel - 1, Need: requiredLevel}, *unconfined)
}

func (s *ConfinesSuite) TestRequiredConfinesAtTheLevelItNeeds() {
	ok, err := s.policy(requiredLevel, nil).Confines()
	s.Require().NoError(err)
	s.True(ok)
}

func (s *ConfinesSuite) TestBestEffortTakesAPartialLevel() {
	ok, err := s.policy(1, nil, WithMode(BestEffort)).Confines()
	s.Require().NoError(err)
	s.True(ok)
}

func (s *ConfinesSuite) TestBestEffortRunsUnconfinedWithoutASandbox() {
	ok, err := s.policy(0, &UnsupportedError{}, WithMode(BestEffort)).Confines()
	s.Require().NoError(err)
	s.False(ok)
}

func (s *ConfinesSuite) TestCommandRefusesARequiredPolicyItCannotConfine() {
	cmd, err := Command(s.T().Context(), s.policy(0, errors.New("no sandbox here")), "true")
	var unconfined *UnconfinedError
	s.Require().ErrorAs(err, &unconfined)
	s.Nil(cmd)
}

func (s *ConfinesSuite) TestCommandStartsABestEffortChildPlainWithoutASandbox() {
	cmd, err := Command(s.T().Context(), s.policy(0, &UnsupportedError{}, WithMode(BestEffort)), "true", "x")
	s.Require().NoError(err)
	s.Equal([]string{"true", "x"}, cmd.Args)
	s.Empty(cmd.ExtraFiles)
}

func (s *ConfinesSuite) TestCommandStartsANilPolicyPlain() {
	cmd, err := Command(s.T().Context(), nil, "true")
	s.Require().NoError(err)
	s.Equal([]string{"true"}, cmd.Args)
	s.Empty(cmd.ExtraFiles)
}

// Relaunch handles only a process Command started as its launcher.
type RelaunchSuite struct{ suite.Suite }

func TestRelaunchSuite(t *testing.T) { suite.Run(t, new(RelaunchSuite)) }

func (s *RelaunchSuite) TestAnyOtherArgvIsNotHandled() {
	for _, argv := range [][]string{nil, {"magus"}, {"magus", launcherName}} {
		handled, err := Relaunch(argv)
		s.Require().NoError(err)
		s.False(handled, "%q", argv)
	}
}

func (s *RelaunchSuite) TestAMalformedLaunchIsAnError() {
	handled, err := Relaunch([]string{launcherName})
	s.True(handled)
	s.Error(err, "the caller exits without running anything")
}
