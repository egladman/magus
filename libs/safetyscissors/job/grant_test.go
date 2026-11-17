package job

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type GrantSuite struct{ suite.Suite }

func TestGrant(t *testing.T) { suite.Run(t, new(GrantSuite)) }

func (s *GrantSuite) running() Job { return Job{ID: "fix-parser", State: StateRunning} }

func (s *GrantSuite) TestAGrantLowersItsRuleForItsJobsLiveAttempt() {
	s.True(Grant{Job: "fix-parser", Rule: "write", Attempt: 2}.Lowers(s.running(), 2, 100))
}

func (s *GrantSuite) TestAGrantGivenInAnEarlierAttemptLowersNothing() {
	s.False(Grant{Job: "fix-parser", Rule: "write", Attempt: 1}.Lowers(s.running(), 2, 100))
}

func (s *GrantSuite) TestARevokedOrLapsedGrantLowersNothing() {
	s.False(Grant{Job: "fix-parser", Rule: "write", Revoked: true}.Lowers(s.running(), 0, 100))
	s.False(Grant{Job: "fix-parser", Rule: "write", Until: 100}.Lowers(s.running(), 0, 100), "Until is the second it lapses")
	s.True(Grant{Job: "fix-parser", Rule: "write", Until: 101}.Lowers(s.running(), 0, 100))
}

func (s *GrantSuite) TestAGrantEndsWithItsJob() {
	ended := s.running()
	ended.State = StatePass
	s.False(Grant{Job: "fix-parser", Rule: "write"}.Lowers(ended, 0, 100))
	s.False(Grant{Job: "other", Rule: "write"}.Lowers(s.running(), 0, 100), "another job's grant")
}

func (s *GrantSuite) TestTheKeyIsTheJobAndTheRule() {
	s.Equal(Grant{Job: "a", Rule: "write", At: 1}.Key(), Grant{Job: "a", Rule: "write", At: 2, Revoked: true}.Key())
	s.NotEqual(Grant{Job: "a", Rule: "write"}.Key(), Grant{Job: "a", Rule: "read"}.Key())
	s.NotEqual(Grant{Job: "ab", Rule: "c"}.Key(), Grant{Job: "a", Rule: "bc"}.Key(), "no concatenation collides")
}
