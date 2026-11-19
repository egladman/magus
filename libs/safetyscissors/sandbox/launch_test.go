package sandbox

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/safetyscissors/job"
)

// Confines and Command follow the mode against what the host probe answers.
type ConfinesSuite struct{ suite.Suite }

func TestConfinesSuite(t *testing.T) { suite.Run(t, new(ConfinesSuite)) }

// unheard is the WithUnconfined a test gives a BestEffort policy whose unconfined
// children it does not inspect.
func unheard(context.Context, error) {}

func (s *ConfinesSuite) policy(level int, err error, opts ...Option) *Policy {
	p, newErr := New(Config{}, append(opts, WithKernelProbe(func() (int, error) { return level, err }))...)
	s.Require().NoError(newErr)
	return p
}

func (s *ConfinesSuite) TestANilPolicyIsRefused() {
	var p *Policy
	var refused *NilPolicyError

	ok, err := p.Confines()
	s.Require().ErrorAs(err, &refused)
	s.False(ok)
	cmd, err := Command(s.T().Context(), p, "true")
	s.Require().ErrorAs(err, &refused, "a nil policy never starts an unconfined child")
	s.Nil(cmd)
	s.ErrorAs(p.Check(0, "/"), &refused)
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
	ok, err := s.policy(1, nil, WithMode(BestEffort), WithUnconfined(unheard)).Confines()
	s.Require().NoError(err)
	s.True(ok)
}

// A BestEffort policy on a host without a sandbox still says why it cannot confine.
func (s *ConfinesSuite) TestBestEffortReturnsTheProbeError() {
	ok, err := s.policy(0, &UnsupportedError{}, WithMode(BestEffort), WithUnconfined(unheard)).Confines()
	s.False(ok)
	var unconfined *UnconfinedError
	s.Require().ErrorAs(err, &unconfined)
	s.Equal(BestEffort, unconfined.Mode)
	var unsupported *UnsupportedError
	s.Require().ErrorAs(err, &unsupported, "the host's answer stays reachable")
	s.NotContains(err.Error(), "WithMode(BestEffort)", "a best-effort policy is not told to opt out")
}

func (s *ConfinesSuite) TestNewRefusesBestEffortWithoutWithUnconfined() {
	_, err := New(Config{}, WithMode(BestEffort))
	var refused *OptionError
	s.Require().ErrorAs(err, &refused)
	s.Equal("WithMode(BestEffort)", refused.Option)
}

func (s *ConfinesSuite) TestNewRefusesAnEmptyLauncherName() {
	_, err := New(Config{}, WithLauncherName(""))
	var refused *OptionError
	s.Require().ErrorAs(err, &refused)
	s.Equal("WithLauncherName", refused.Option)
}

func (s *ConfinesSuite) TestNewRefusesEveryInvalidOptionAtOnce() {
	_, err := New(Config{}, WithMode(Mode(7)), WithUnconfined(nil), WithLease("", job.LeaseSourceMarker), WithKernelProbe(nil))
	var refused []string
	for _, e := range err.(interface{ Unwrap() []error }).Unwrap() {
		opt, ok := errors.AsType[*OptionError](e)
		s.Require().True(ok, "%v is an OptionError", e)
		refused = append(refused, opt.Option)
	}
	s.Equal([]string{"WithMode", "WithUnconfined", "WithLease", "WithKernelProbe"}, refused)
}

func (s *ConfinesSuite) TestCommandRefusesARequiredPolicyItCannotConfine() {
	cmd, err := Command(s.T().Context(), s.policy(0, errors.New("no sandbox here")), "true")
	var unconfined *UnconfinedError
	s.Require().ErrorAs(err, &unconfined)
	s.Nil(cmd)
}

func (s *ConfinesSuite) TestCommandStartsABestEffortChildPlainWithoutASandbox() {
	var heard []error
	p := s.policy(0, &UnsupportedError{}, WithMode(BestEffort), WithUnconfined(func(_ context.Context, err error) { heard = append(heard, err) }))
	cmd, err := Command(s.T().Context(), p, "true", "x")
	s.Require().NoError(err)
	s.Equal([]string{"true", "x"}, cmd.Args)
	s.Empty(cmd.ExtraFiles)
	s.Require().Len(heard, 1, "the caller hears that the child runs unconfined")
	var unconfined *UnconfinedError
	s.ErrorAs(heard[0], &unconfined)
}

// Relaunch handles only a process Command started as its launcher.
type RelaunchSuite struct{ suite.Suite }

func TestRelaunchSuite(t *testing.T) { suite.Run(t, new(RelaunchSuite)) }

func (s *RelaunchSuite) TestAnyOtherArgvIsNotHandled() {
	for _, argv := range [][]string{nil, {"tool"}, {"tool", defaultLauncherName}} {
		handled, err := Relaunch(argv)
		s.Require().NoError(err)
		s.False(handled, "%q", argv)
	}
}

func (s *RelaunchSuite) TestAMalformedLaunchIsAnError() {
	handled, err := Relaunch([]string{defaultLauncherName})
	s.True(handled)
	s.Error(err, "the caller exits without running anything")
}

// A launcher named by WithLauncherName answers to that name alone.
func (s *RelaunchSuite) TestTheLauncherNameIsAnOption() {
	handled, err := Relaunch([]string{defaultLauncherName}, WithLauncherName("tool-launch"))
	s.Require().NoError(err)
	s.False(handled, "the default name is not the launcher's once another is named")
	handled, err = Relaunch([]string{"tool-launch"}, WithLauncherName("tool-launch"))
	s.True(handled)
	s.Error(err, "the named launcher is handled")
}

func (s *RelaunchSuite) TestAnEmptyLauncherNameIsRefused() {
	handled, err := Relaunch([]string{""}, WithLauncherName(""))
	s.False(handled)
	var refused *OptionError
	s.ErrorAs(err, &refused)
}
