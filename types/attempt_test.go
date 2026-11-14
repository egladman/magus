package types

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/json"
)

type AttemptSuite struct {
	suite.Suite
}

func TestAttemptSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(AttemptSuite))
}

func (s *AttemptSuite) TestAStartedTryWithNoEndIsRunning() {
	s.True(Attempt{Status: AttemptStatus{Started: 10}}.Running())
	s.False(Attempt{Status: AttemptStatus{Started: 10, Ended: 20}}.Running())
	s.False(Attempt{}.Running(), "a try the guard never saw start")
}

func (s *AttemptSuite) TestATryRunningPastItsDeadlineReadsAsEndedThere() {
	got := Attempt{Status: AttemptStatus{Started: 10}}.Bound(50, 60)
	s.Equal(AttemptStatus{Started: 10, Ended: 50, End: AttemptEndDeadline}, got.Status)
}

func (s *AttemptSuite) TestATryBeforeItsDeadlineStaysRunning() {
	got := Attempt{Status: AttemptStatus{Started: 10}}.Bound(50, 40)
	s.True(got.Running())
}

func (s *AttemptSuite) TestATryWithNoDeadlineStaysRunning() {
	got := Attempt{Status: AttemptStatus{Started: 10}}.Bound(0, 1<<40)
	s.True(got.Running())
}

func (s *AttemptSuite) TestAnObservedEndOutlivesTheDeadline() {
	ended := Attempt{Status: AttemptStatus{Started: 10, Ended: 20, End: AttemptEndHandback}}
	s.Equal(ended, ended.Bound(15, 60))
}

func (s *AttemptSuite) TestAForkedTryIsSuperseded() {
	s.True(Attempt{Status: AttemptStatus{State: StateFail}}.Superseded())
	s.False(Attempt{Status: AttemptStatus{Started: 10}}.Superseded())
}

// Prior tries are the next holder's evidence, so a clone handed out of
// the store must not alias a slice or the result.
func (s *AttemptSuite) TestCloneSharesNoSliceOrResult() {
	refs := func() []string { return append(make([]string, 0, 4), "out1") }
	orig := Attempt{Job: "a", N: 1, Status: AttemptStatus{
		Result:      &JobResult{Job: "a", ChangedPaths: refs(), UnresolvedRisks: refs()},
		Unmet:       []VerificationStatus{{ID: "unit", Violations: refs()}},
		Violations:  refs(),
		FailingRefs: refs(),
		Denials:     append(make([]JobDenial, 0, 4), JobDenial{Rule: "lease-write"}),
	}}
	want := Attempt{Job: "a", N: 1, Status: AttemptStatus{
		Result:      &JobResult{Job: "a", ChangedPaths: refs(), UnresolvedRisks: refs()},
		Unmet:       []VerificationStatus{{ID: "unit", Violations: refs()}},
		Violations:  refs(),
		FailingRefs: refs(),
		Denials:     []JobDenial{{Rule: "lease-write"}},
	}}

	c := orig.Clone()
	c.Status.Result.ChangedPaths[0] = "changed"
	c.Status.Result.UnresolvedRisks = append(c.Status.Result.UnresolvedRisks, "x")
	c.Status.Result.Job = "b"
	c.Status.Unmet[0].Violations[0] = "changed"
	c.Status.Violations = append(c.Status.Violations, "x")
	c.Status.FailingRefs[0] = "changed"
	c.Status.Denials = append(c.Status.Denials, JobDenial{Rule: "x"})

	s.Equal(want, orig)
}

func (s *AttemptSuite) TestNewJobAttemptsGroupsByJobInOrderOfN() {
	got := NewJobAttempts([]Attempt{{Job: "a", N: 2}, {Job: "b", N: 1}, {Job: "a", N: 1}})
	s.Equal(JobAttempts{
		"a": {{Job: "a", N: 1}, {Job: "a", N: 2}},
		"b": {{Job: "b", N: 1}},
	}, got)
}

func (s *AttemptSuite) TestAJobWithNoTriesIsOnItsFirst() {
	tries := NewJobAttempts(nil)
	s.Equal(1, tries.LiveN("a"))
	s.Equal(Attempt{Job: "a", N: 1}, tries.Live("a"))
	s.Empty(tries.Prior("a"))
	s.Nil(tries.Result("a"))
}

func (s *AttemptSuite) TestATryNoForkSupersededIsLive() {
	running := Attempt{Job: "a", N: 2, Status: AttemptStatus{Started: 10}}
	tries := NewJobAttempts([]Attempt{{Job: "a", N: 1, Status: AttemptStatus{State: StateFail}}, running})
	s.Equal(2, tries.LiveN("a"))
	s.Equal(running, tries.Live("a"))
}

func (s *AttemptSuite) TestAForkStartsTheTryPastTheNewestSuperseded() {
	failed := Attempt{Job: "a", N: 1, Status: AttemptStatus{State: StateFail}}
	tries := NewJobAttempts([]Attempt{failed})
	s.Equal(2, tries.LiveN("a"))
	s.Equal(Attempt{Job: "a", N: 2}, tries.Live("a"))
	s.Equal([]Attempt{failed}, tries.Prior("a"))
}

func (s *AttemptSuite) TestResultIsWhatTheLiveTryFiled() {
	filed := &JobResult{Job: "a", ChangedPaths: []string{"x.go"}}
	tries := NewJobAttempts([]Attempt{
		{Job: "a", N: 1, Status: AttemptStatus{State: StateFail, Result: &JobResult{Job: "a"}}},
		{Job: "a", N: 2, Status: AttemptStatus{Result: filed}},
	})
	s.Equal(filed, tries.Result("a"))
}

func (s *AttemptSuite) TestAZeroStatusWritesNoMembers() {
	out, err := json.Marshal(AttemptStatus{})
	s.Require().NoError(err)
	s.JSONEq(`{}`, string(out))
}
