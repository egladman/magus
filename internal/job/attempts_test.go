package job

import (
	"errors"
	"testing"

	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/suite"
)

type AttemptsSuite struct {
	suite.Suite
}

func TestAttemptsSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(AttemptsSuite))
}

func unmetGoals(ids ...string) []job.VerificationStatus {
	out := make([]job.VerificationStatus, len(ids))
	for i, id := range ids {
		out[i] = job.VerificationStatus{ID: id}
	}
	return out
}

func tried(n int, status job.AttemptStatus) job.Attempt {
	return job.Attempt{Job: "a", N: n, Status: status}
}

func (s *AttemptsSuite) TestABoundaryDenialNeedsAWidening() {
	p := tried(1, job.AttemptStatus{Denials: []job.Denial{{Rule: "raw-tool"}, {Rule: "lease-write", Reason: "a.go is lease b's\nmore"}}})
	s.Equal(job.RetryVerdict{Change: job.RetryAfterWidening, Reason: "the guard denied its holder under lease-write: a.go is lease b's"},
		classifyAttempt(p, nil))
}

func (s *AttemptsSuite) TestAChangedPathOutsideTheWritePathsNeedsAWidening() {
	v := `changed path "b.go" is outside the job's write paths (a.go)`
	p := tried(1, job.AttemptStatus{Violations: []string{v}})
	s.Equal(job.RetryVerdict{Change: job.RetryAfterWidening, Reason: v}, classifyAttempt(p, nil))
}

func (s *AttemptsSuite) TestAGoalUnmetTwiceRunningNeedsAPerson() {
	p := tried(2, job.AttemptStatus{Unmet: unmetGoals("lint", "check")})
	earlier := []job.Attempt{tried(1, job.AttemptStatus{Unmet: unmetGoals("check")})}
	s.Equal(job.RetryVerdict{Change: job.RetryAfterPerson, Reason: "goal check was unmet in attempts 1 and 2"},
		classifyAttempt(p, earlier))
}

func (s *AttemptsSuite) TestAGoalUnmetOnlyInAnOlderAttemptIsRetryable() {
	p := tried(3, job.AttemptStatus{Unmet: unmetGoals("check")})
	earlier := []job.Attempt{
		tried(1, job.AttemptStatus{Unmet: unmetGoals("check")}),
		tried(2, job.AttemptStatus{Unmet: unmetGoals("lint")}),
	}
	s.Equal(job.RetryVerdict{Retryable: true}, classifyAttempt(p, earlier))
}

func (s *AttemptsSuite) TestAnEndTheSweepWroteIsRetryableAsIs() {
	p := tried(1, job.AttemptStatus{State: job.StateNoReturn, EndReason: "its checkout is gone"})
	s.Equal(job.RetryVerdict{Retryable: true}, classifyAttempt(p, nil))
}

func (s *AttemptsSuite) TestAFirstUnmetGoalIsRetryable() {
	p := tried(1, job.AttemptStatus{Unmet: unmetGoals("check"), Denials: []job.Denial{{Rule: "raw-tool"}}})
	s.Equal(job.RetryVerdict{Retryable: true}, classifyAttempt(p, nil))
}

// internal/job cannot import the guard, so its list of boundary rules is
// held here to the rules the guard catalogs: a renamed rule would
// otherwise classify nothing.
func (s *AttemptsSuite) TestWideningRulesAreRulesTheGuardCatalogs() {
	known := builtin.Defaults()
	for _, rule := range wideningRules {
		s.Contains(known, rule)
	}
}

// The violation classifyAttempt reads as a widening is the one VerifyGates words.
func (s *AttemptsSuite) TestVerifyGatesWordsAnOutsidePathAsClassifyAttemptReadsIt() {
	status := VerifyGates(job.Job{ID: "a", Paths: job.Paths{Write: []string{"a.go"}}}, job.Result{ChangedPaths: []string{"b.go"}},
		job.CapturedRun{}, nil, nil, Observed{})
	got := classifyAttempt(tried(1, job.AttemptStatus{Violations: status.Violations}), nil)
	s.Equal(job.RetryAfterWidening, got.Change)
}

func foldedRow() job.Job {
	prev := job.Job{ID: "a", State: job.StateFail, Generation: 2, Checkpoint: "abc", ReportedBase: "abd"}
	prev.Updated, prev.ReworkRounds, prev.Widenings = 40, 2, 1
	prev.ModelTier = &tier.Assignment{Name: "routine"}
	prev.Attempt = &job.CapturedRun{Ref: "out1", Failed: true}
	prev.GateAttempts = []job.GateAttempt{
		{GateID: "lint", Attempt: job.CapturedRun{Ref: "out2"}},
		{GateID: "unit", Attempt: job.CapturedRun{Ref: "out3", Failed: true}},
	}
	prev.Rejection = &job.Rejection{Unmet: []job.VerificationStatus{{ID: "check", Violations: []string{"failed"}}}, Violations: []string{"v"}}
	return prev
}

func (s *AttemptsSuite) TestFoldAttemptKeepsWhatTheNextHolderNeeds() {
	result := &job.Result{UnresolvedRisks: []string{"one", "two"}}
	live := tried(1, job.AttemptStatus{Started: 30, Result: result})
	denials := make([]job.Denial, 10)
	for i := range denials {
		denials[i] = job.Denial{Rule: "raw-tool", Ts: int64(i)}
	}

	got := foldAttempt(foldedRow(), live, nil, denials, true, 99)

	want := tried(1, job.AttemptStatus{Started: 30, Ended: 40, End: job.AttemptEndJob, Result: result,
		State: job.StateFail, ObservedGeneration: 2, ModelTier: "routine", Checkpoint: "abc", ReportedBase: "abd"})
	want.Status.ReworkRounds, want.Status.Widenings = 2, 1
	want.Status.Unmet = []job.VerificationStatus{{ID: "check", Violations: []string{"failed"}}}
	want.Status.Violations = []string{"v"}
	want.Status.FailingRefs = []string{"out1", "out3"}
	want.Status.Denials, want.Status.DenialsKnown = denials[2:], true
	want.Status.Retry = job.RetryVerdict{Retryable: true}
	s.Equal(want, got, "the eight newest denials are kept")
}

func (s *AttemptsSuite) TestFoldAttemptSharesNothingWithTheRow() {
	prev := foldedRow()
	got := foldAttempt(prev, tried(1, job.AttemptStatus{}), nil, nil, false, 99)
	prev.Rejection.Unmet[0].Violations[0] = "edited"
	s.Equal("failed", got.Status.Unmet[0].Violations[0])
}

// foldedStatus is foldedRow's fold with no denials read, its try
// started, ended and ended by end as given.
func foldedStatus(started, ended int64, end job.AttemptEnd) job.AttemptStatus {
	st := job.AttemptStatus{Started: started, Ended: ended, End: end, State: job.StateFail, ObservedGeneration: 2,
		ModelTier: "routine", Checkpoint: "abc", ReportedBase: "abd", ReworkRounds: 2, Widenings: 1}
	st.Unmet = []job.VerificationStatus{{ID: "check", Violations: []string{"failed"}}}
	st.Violations = []string{"v"}
	st.FailingRefs = []string{"out1", "out3"}
	st.Retry = job.RetryVerdict{Retryable: true}
	return st
}

func (s *AttemptsSuite) TestFoldAttemptEndsARowNeverStampedNow() {
	prev := foldedRow()
	prev.Updated = 0
	got := foldAttempt(prev, tried(1, job.AttemptStatus{}), nil, nil, false, 99)
	s.Equal(foldedStatus(0, 99, job.AttemptEndJob), got.Status)
}

func (s *AttemptsSuite) TestFoldAttemptKeepsAnObservedEnd() {
	live := tried(1, job.AttemptStatus{Started: 30, Ended: 35, End: job.AttemptEndHandback})
	got := foldAttempt(foldedRow(), live, nil, nil, false, 99)
	s.Equal(foldedStatus(30, 35, job.AttemptEndHandback), got.Status)
}

func (s *AttemptsSuite) TestEvidenceAtSetsAsideRunsOfAnEarlierSpec() {
	row := job.Job{ID: "a", Generation: 3}
	result := &job.Result{ChangedPaths: []string{"a.go"}, ObservedGeneration: 2}
	row.Attempt = &job.CapturedRun{Ref: "out1", ObservedGeneration: 2}
	row.GateAttempts = []job.GateAttempt{
		{GateID: "lint", Attempt: job.CapturedRun{Ref: "out2", ObservedGeneration: 3}},
		{GateID: "unit", Attempt: job.CapturedRun{Ref: "out3", ObservedGeneration: 1}},
		{GateID: "vet", Attempt: job.CapturedRun{Ref: "out4"}},
	}

	ev, prior := evidenceAt(row, result)

	s.Equal(1, prior, "the oldest generation set aside")
	s.Equal([]string{"a.go"}, ev.result.ChangedPaths, "a result of an earlier spec still grades")
	s.Equal(job.CapturedRun{}, ev.attempt, "a run of an earlier spec does not")
	s.Require().Len(ev.gateAttempts, 2)
	s.Equal("lint", ev.gateAttempts[0].GateID)
	s.Equal("vet", ev.gateAttempts[1].GateID, "an unstamped run is unknown, never prior")
}

func (s *AttemptsSuite) TestEvidenceAtSetsAsideNothingOfTheCurrentSpec() {
	_, prior := evidenceAt(job.Job{ID: "a", Generation: 1}, &job.Result{ObservedGeneration: 1})
	s.Zero(prior)
}

func (s *AttemptsSuite) TestRefuseAttemptPassesUnderTheCap() {
	s.Require().NoError(refuseAttempt("a", 2, job.Job{}), "no cap")
	s.Require().NoError(refuseAttempt("a", 2, job.Job{MaxAttempts: 3}))
}

func (s *AttemptsSuite) TestRefuseAttemptPastTheCap() {
	err := refuseAttempt("a", 2, job.Job{MaxAttempts: 2})
	s.Require().ErrorIs(err, job.ErrMaxAttempts)
	s.Contains(err.Error(), "a used 2 of its 2 attempt(s)")
	var nonRetryable *types.NonRetryableError
	s.Require().True(errors.As(err, &nonRetryable))
	s.Equal(job.RetryAfterPerson, types.ClassifyRetry(err).Change)
}
