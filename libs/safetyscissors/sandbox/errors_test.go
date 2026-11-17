package sandbox

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/safetyscissors/sandbox/filesystem"
)

type ErrorsSuite struct{ suite.Suite }

func TestErrorsSuite(t *testing.T) { suite.Run(t, new(ErrorsSuite)) }

func (s *ErrorsSuite) TestADenialReadsAsTheRulesetsRefusal() {
	cause := errors.Join(filesystem.ErrDenied, errors.New("write /x is outside the allowlist"))
	err := &DeniedError{Access: filesystem.Write, Path: "/x", Err: cause}
	s.Equal(cause.Error(), err.Error())
	s.ErrorIs(err, filesystem.ErrDenied)
}

func (s *ErrorsSuite) TestADenialWithNoCauseStillWrapsErrDenied() {
	err := &DeniedError{Access: filesystem.Exec, Path: "/bin/x"}
	s.ErrorIs(err, filesystem.ErrDenied)
	s.Contains(err.Error(), "exec /bin/x")
}

func (s *ErrorsSuite) TestUnconfinedNamesTheOptOut() {
	for _, err := range []error{&UnconfinedError{Err: &UnsupportedError{}}, &UnconfinedError{Have: 2, Need: 3}} {
		s.Contains(err.Error(), "WithMode(BestEffort)")
	}
	s.Contains((&UnconfinedError{Have: 2, Need: 3}).Error(), "is 2, below the 3")
}

func (s *ErrorsSuite) TestUnsupportedWrapsTheHostsAnswer() {
	cause := errors.New("ENOSYS")
	err := &UnsupportedError{Err: cause}
	s.ErrorIs(err, cause)
	s.Contains(err.Error(), "unsupported on this host: ENOSYS")
	s.Contains((&UnsupportedError{}).Error(), "unsupported on ")
}
