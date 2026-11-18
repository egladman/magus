package sessions

import (
	"fmt"
	"testing"
	"time"

	"github.com/egladman/magus/libs/safetyscissors/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// recordResults files each result under its own invocation, in the order given, so a later
// one is newer by name the way minted ids are.
func recordResults(t *testing.T, dir string, results ...session.TargetResult) {
	t.Helper()
	for i, r := range results {
		w, err := session.Open(t.Context(), dir, fmt.Sprintf("invmv%04d1", i), session.InvocationStart{Command: "run"})
		require.NoError(t, err)
		require.NoError(t, w.Append(t.Context(), session.KindTargetResult, r))
	}
}

type RunDurationWithSuite struct {
	suite.Suite
	dir string
}

func TestRunDurationWithSuite(t *testing.T) { suite.Run(t, new(RunDurationWithSuite)) }

func (s *RunDurationWithSuite) SetupTest() {
	s.dir = s.T().TempDir()
	for i, run := range []struct {
		command string
		took    int64
	}{
		{"run go-test .", 400_000},
		{"run go-test . -- -run TestA", 20_000},
		{"run go-test . --", 300_000},
	} {
		w, err := session.Open(s.T().Context(), s.dir, fmt.Sprintf("invwith%04d1", i), session.InvocationStart{Command: run.command})
		s.Require().NoError(err)
		s.Require().NoError(w.Append(s.T().Context(), session.KindTargetResult, session.TargetResult{Target: "go-test", Project: ".", Outcome: session.OutcomePass, DurationMs: run.took}))
	}
}

func (s *RunDurationWithSuite) TestTheSameForwardedArgumentsMatch() {
	got, ok := LastRunDurationWith(s.T().Context(), s.dir, ".", "go-test", []string{"-run", "TestA"})
	s.Require().True(ok)
	s.Equal(20*time.Second, got)
}

func (s *RunDurationWithSuite) TestNoForwardedArgumentsMatchAnUnfilteredRun() {
	got, ok := LastRunDurationWith(s.T().Context(), s.dir, ".", "go-test", nil)
	s.Require().True(ok)
	s.Equal(300*time.Second, got, "a bare -- forwards nothing")
}

func (s *RunDurationWithSuite) TestOtherArgumentsHaveNoRecord() {
	_, ok := LastRunDurationWith(s.T().Context(), s.dir, ".", "go-test", []string{"-run", "TestB"})
	s.False(ok)
}

type DurationSuite struct{ suite.Suite }

func TestDuration(t *testing.T) { suite.Run(t, new(DurationSuite)) }

func (s *DurationSuite) TestLastRunDurationIsTheNewestRun() {
	t := s.T()
	dir := t.TempDir()
	recordResults(t, dir,
		session.TargetResult{Target: "go-test", Project: ".", Outcome: session.OutcomePass, DurationMs: 400_000},
		session.TargetResult{Target: "go::go-test:rw", Project: ".", Outcome: session.OutcomeFail, DurationMs: 300_000},
		session.TargetResult{Target: "go-test", Project: ".", Outcome: session.OutcomePass, DurationMs: 5, Replayed: true},
		session.TargetResult{Target: "go-test", Project: ".", Outcome: session.OutcomeFail, DiagnosticCode: "MGS3009"},
		session.TargetResult{Target: "go-test", Project: "docs", Outcome: session.OutcomePass, DurationMs: 9_000},
		session.TargetResult{Target: "lint", Project: ".", Outcome: session.OutcomePass, DurationMs: 70_000},
	)

	got, ok := LastRunDuration(t.Context(), dir, ".", "go-test")
	require.True(t, ok)
	assert.Equal(t, 300*time.Second, got, "the newest run that ran: a replay, a refusal and another project do not count, and a spell filter or charm names the same target")

	got, ok = LastRunDuration(t.Context(), dir, "", "go_test:rw")
	require.True(t, ok)
	assert.Equal(t, 300*time.Second, got, "an empty project is the root, and the name is compared normalized")

	_, ok = LastRunDuration(t.Context(), dir, ".", "build")
	assert.False(t, ok, "a target that never ran here has no duration")
	_, ok = LastRunDuration(t.Context(), t.TempDir(), ".", "go-test")
	assert.False(t, ok, "an empty store")
	_, ok = LastRunDuration(t.Context(), dir+"/absent", ".", "go-test")
	assert.False(t, ok, "a missing store")
}

func (s *DurationSuite) TestLastRunDurationReadsOnlyTheNewestFiles() {
	t := s.T()
	dir := t.TempDir()
	results := make([]session.TargetResult, maxInvocationFiles+1)
	results[0] = session.TargetResult{Target: "go-test", Project: ".", Outcome: session.OutcomePass, DurationMs: 60_000}
	for i := 1; i < len(results); i++ {
		results[i] = session.TargetResult{Target: "lint", Project: ".", Outcome: session.OutcomePass, DurationMs: 1_000}
	}
	recordResults(t, dir, results...)

	_, ok := LastRunDuration(t.Context(), dir, ".", "go-test")
	assert.False(t, ok, "a run older than the newest %d files is not read", maxInvocationFiles)
}
