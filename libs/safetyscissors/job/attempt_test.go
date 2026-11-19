package job

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/codec"
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
		Result:      &Result{Job: "a", ChangedPaths: refs(), UnresolvedRisks: refs()},
		Unmet:       []VerificationStatus{{ID: "unit", Violations: refs()}},
		Violations:  refs(),
		FailingRefs: refs(),
		Denials:     append(make([]Denial, 0, 4), Denial{Rule: "lease-write"}),
	}}
	want := Attempt{Job: "a", N: 1, Status: AttemptStatus{
		Result:      &Result{Job: "a", ChangedPaths: refs(), UnresolvedRisks: refs()},
		Unmet:       []VerificationStatus{{ID: "unit", Violations: refs()}},
		Violations:  refs(),
		FailingRefs: refs(),
		Denials:     []Denial{{Rule: "lease-write"}},
	}}

	c := orig.Clone()
	c.Status.Result.ChangedPaths[0] = "changed"
	c.Status.Result.UnresolvedRisks = append(c.Status.Result.UnresolvedRisks, "x")
	c.Status.Result.Job = "b"
	c.Status.Unmet[0].Violations[0] = "changed"
	c.Status.Violations = append(c.Status.Violations, "x")
	c.Status.FailingRefs[0] = "changed"
	c.Status.Denials = append(c.Status.Denials, Denial{Rule: "x"})

	s.Equal(want, orig)
}

func (s *AttemptSuite) TestNewJobAttemptsGroupsByJobInOrderOfN() {
	got := NewAttempts([]Attempt{{Job: "a", N: 2}, {Job: "b", N: 1}, {Job: "a", N: 1}})
	s.Equal(Attempts{
		"a": {{Job: "a", N: 1}, {Job: "a", N: 2}},
		"b": {{Job: "b", N: 1}},
	}, got)
}

func (s *AttemptSuite) TestAJobWithNoTriesIsOnItsFirst() {
	tries := NewAttempts(nil)
	s.Equal(1, tries.LiveN("a"))
	s.Equal(Attempt{Job: "a", N: 1}, tries.Live("a"))
	s.Empty(tries.Prior("a"))
	s.Nil(tries.Result("a"))
}

func (s *AttemptSuite) TestATryNoForkSupersededIsLive() {
	running := Attempt{Job: "a", N: 2, Status: AttemptStatus{Started: 10}}
	tries := NewAttempts([]Attempt{{Job: "a", N: 1, Status: AttemptStatus{State: StateFail}}, running})
	s.Equal(2, tries.LiveN("a"))
	s.Equal(running, tries.Live("a"))
}

func (s *AttemptSuite) TestAForkStartsTheTryPastTheNewestSuperseded() {
	failed := Attempt{Job: "a", N: 1, Status: AttemptStatus{State: StateFail}}
	tries := NewAttempts([]Attempt{failed})
	s.Equal(2, tries.LiveN("a"))
	s.Equal(Attempt{Job: "a", N: 2}, tries.Live("a"))
	s.Equal([]Attempt{failed}, tries.Prior("a"))
}

func (s *AttemptSuite) TestResultIsWhatTheLiveTryFiled() {
	filed := &Result{Job: "a", ChangedPaths: []string{"x.go"}}
	tries := NewAttempts([]Attempt{
		{Job: "a", N: 1, Status: AttemptStatus{State: StateFail, Result: &Result{Job: "a"}}},
		{Job: "a", N: 2, Status: AttemptStatus{Result: filed}},
	})
	s.Equal(filed, tries.Result("a"))
}

func (s *AttemptSuite) TestAZeroStatusWritesNoMembers() {
	out, err := codec.Marshal(AttemptStatus{})
	s.Require().NoError(err)
	s.JSONEq(`{}`, string(out))
}

type RecordAttemptSuite struct {
	suite.Suite
	store *Store
}

func TestRecordAttemptSuite(t *testing.T) { suite.Run(t, new(RecordAttemptSuite)) }

func (s *RecordAttemptSuite) SetupTest() {
	st, err := Open(s.T().Context(), Config{Dir: s.T().TempDir()}, writable()...)
	s.Require().NoError(err)
	s.store = st
	_, err = st.Update(s.T().Context(), "a", func(*Job) {})
	s.Require().NoError(err)
}

// ended is a try of a the host started at 10 and saw exit at 20.
func ended(n int) Attempt {
	return Attempt{Job: "a", N: n, Status: AttemptStatus{Started: 10, Ended: 20, End: AttemptEndExit}}
}

func (s *RecordAttemptSuite) invalid(err error) *InvalidAttemptError {
	refused, ok := errors.AsType[*InvalidAttemptError](err)
	s.Require().True(ok, err)
	return refused
}

func (s *RecordAttemptSuite) stored() []Attempt {
	tries, err := s.store.Attempts(s.T().Context())
	s.Require().NoError(err)
	return tries
}

func (s *RecordAttemptSuite) TestAHostRunTryIsStoredWithItsTierRefsAndVerdict() {
	a := ended(1)
	a.Status.ModelTier = "design"
	a.Status.FailingRefs = []string{"r1", "r2"}
	a.Status.Retry = RetryVerdict{Change: RetryAfterWidening, Reason: "needs b.go", Code: "E1"}

	s.Require().NoError(s.store.RecordAttempt(s.T().Context(), a))

	s.Equal([]Attempt{a}, s.stored())
}

func (s *RecordAttemptSuite) TestTriesAreRecordedInOrder() {
	s.Require().NoError(s.store.RecordAttempt(s.T().Context(), ended(1)))
	s.Require().NoError(s.store.RecordAttempt(s.T().Context(), ended(2)))

	s.Equal([]Attempt{ended(1), ended(2)}, s.stored())
	s.Equal(2, NewAttempts(s.stored()).LiveN("a"))
}

func (s *RecordAttemptSuite) TestAnOutOfOrderNIsRefused() {
	s.Require().NoError(s.store.RecordAttempt(s.T().Context(), ended(1)))

	for _, n := range []int{0, 1, 3} {
		refused := s.invalid(s.store.RecordAttempt(s.T().Context(), ended(n)))
		s.Equal(n, refused.N)
	}
	s.Len(s.stored(), 1)
}

func (s *RecordAttemptSuite) TestAnUnknownJobIsRefused() {
	a := ended(1)
	a.Job = "b"

	s.Require().ErrorIs(s.store.RecordAttempt(s.T().Context(), a), ErrUnknownJob)
	s.Empty(s.stored())
}

func (s *RecordAttemptSuite) TestAnEndBeforeTheStartIsRefused() {
	a := ended(1)
	a.Status.Ended = 5

	s.invalid(s.store.RecordAttempt(s.T().Context(), a))
	s.Empty(s.stored())
}

func (s *RecordAttemptSuite) TestATryWithNoStartIsRefused() {
	a := ended(1)
	a.Status.Started = 0

	s.invalid(s.store.RecordAttempt(s.T().Context(), a))
}

func (s *RecordAttemptSuite) TestATryWithNoEndIsRefused() {
	a := ended(1)
	a.Status.Ended = 0

	s.invalid(s.store.RecordAttempt(s.T().Context(), a))
}

func (s *RecordAttemptSuite) TestAnUnknownOrDeadlineEndIsRefused() {
	for _, end := range []AttemptEnd{"", "crashed", AttemptEndDeadline} {
		a := ended(1)
		a.Status.End = end

		s.invalid(s.store.RecordAttempt(s.T().Context(), a))
	}
	s.Empty(s.stored())
}

func (s *RecordAttemptSuite) TestAStoreWithNoActorRefusesTheWrite() {
	st, err := Open(s.T().Context(), Config{Dir: s.T().TempDir()}, WithNotices(func(Notice) {}))
	s.Require().NoError(err)

	refused, ok := errors.AsType[*UnconfiguredError](st.RecordAttempt(s.T().Context(), ended(1)))

	s.Require().True(ok)
	s.Equal("WithActor", refused.Option)
}
