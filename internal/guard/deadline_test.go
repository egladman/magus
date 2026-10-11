package guard

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// A phase spends its shares' part of what remains, leaves the rest to the phases after it,
// runs unbounded with no deadline, and is never handed a negative budget.
func TestPhaseSharesSplitWhatRemains(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), judgeReserve+2*time.Second)
	defer cancel()
	shares := phaseShares{phaseApprovedResolve: 2, phaseApprovedRule: 1, phaseWorktreeRule: 1}
	got, bounded := shares.take(ctx, phaseApprovedResolve)
	require.True(t, bounded)
	assert.LessOrEqual(t, got, time.Second)
	assert.Greater(t, got, 900*time.Millisecond)
	assert.NotContains(t, shares, phaseApprovedResolve, "a phase that started holds no shares")
	got, _ = shares.take(ctx, phaseApprovedRule)
	assert.Greater(t, got, 900*time.Millisecond, "half of what remains, the approved resolve having spent none")

	_, bounded = phaseShares{phaseWorktreeRule: 1}.take(t.Context(), phaseWorktreeRule)
	assert.False(t, bounded, "with no deadline, a phase runs to completion")

	gone, cancelGone := context.WithTimeout(t.Context(), judgeReserve/2)
	defer cancelGone()
	got, bounded = phaseShares{phaseWorktreeRule: 1}.take(gone, phaseWorktreeRule)
	assert.True(t, bounded)
	assert.Zero(t, got, "past the reserve, nothing is left, and never less")
}

func TestWithHookDeadlineKeepsAnEarlierOne(t *testing.T) {
	early, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	want, _ := early.Deadline()
	ctx, cancelHook := WithHookDeadline(early)
	defer cancelHook()
	got, ok := ctx.Deadline()
	require.True(t, ok)
	assert.Equal(t, want, got)

	ctx, cancelHook = WithHookDeadline(t.Context())
	defer cancelHook()
	got, ok = ctx.Deadline()
	require.True(t, ok)
	assert.WithinDuration(t, time.Now().Add(hookBudget), got, time.Second)
}

// A load that failed after the deadline passed lapsed; one that answered did not, however
// late, and one with no deadline has no budget to lapse against. The load's own answer
// comes back beside the phase.
func TestTimeRuleLoadLapsesOnlyAFailurePastTheDeadline(t *testing.T) {
	expired, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	<-expired.Done()
	failed := errors.New("magusfile: context deadline exceeded")
	load := func(err error) func() (string, error) { return func() (string, error) { return "rules", err } }

	_, phase, err := TimeRuleLoad(expired, load(failed))
	assert.True(t, phase.Lapsed)
	require.ErrorIs(t, err, failed)
	got, phase, err := TimeRuleLoad(expired, load(nil))
	require.NoError(t, err)
	assert.Equal(t, "rules", got)
	assert.False(t, phase.Lapsed)

	_, unbounded, _ := TimeRuleLoad(t.Context(), load(failed))
	assert.Equal(t, trail.Phase{Name: phaseLoadRules, ElapsedMs: unbounded.ElapsedMs}, unbounded)
}

// A lapse is worded against its budget, rounded for a reader, or as having had none.
func TestLapseWordingNamesTheBudget(t *testing.T) {
	rule := lapseWording{"the rule", "answer"}
	assert.Equal(t, "the rule did not answer within 2.1s", rule.clause(2140*time.Millisecond))
	assert.Equal(t, "the rule did not answer within 50ms", rule.clause(50*time.Millisecond))
	assert.Equal(t, "the rule had no time left before the guard's deadline", rule.clause(0))
}

// A rule load that ran out of time is judged as a phase that lapsed, not as a broken
// magusfile: a write is denied naming the load and its budget, a read goes through with the
// same named in its note.
func TestLapsedRuleLoadDeniesAWriteAndAdvisesARead(t *testing.T) {
	deps := Dependencies{
		LoadFailure: errors.New("magusfile: context deadline exceeded"),
		RuleLoad:    trail.Phase{Name: phaseLoadRules, ElapsedMs: 6600, BudgetMs: 6500, Lapsed: true},
	}

	ctx, _ := spawnFixture(t)
	write := Judge(ctx, deps, Request{Host: "claude-code", Session: "s1", Input: "notes.txt", IsPath: true})
	assert.Equal(t, "deny", write.Decision)
	assert.Contains(t, write.Reason, "magus ran out of time judging this file write: loading this workspace's guard rules did not finish within 6.5s.")

	read := Judge(ctx, deps, Request{Host: "claude-code", Session: "s1", Input: "ls"})
	assert.Equal(t, "advise", read.Decision)
	assert.Contains(t, read.Context, "The working tree's magus\\guard.command rule judged nothing: the magusfile did not load within 6.5s")
}

// A dry run grades for a person at a terminal, a fork or a verb's own edit, so its rules
// run without the hook's budget: a cold first load there must not read as a lapse.
func TestDryRunJudgesWithoutTheHookBudget(t *testing.T) {
	ctx, _ := spawnFixture(t)
	var bounded []bool
	rule := func(ctx context.Context, _ types.CommandRequest, _ hint.Gate) (types.GuardVerdict, error) {
		_, ok := ctx.Deadline()
		bounded = append(bounded, ok)
		return types.GuardVerdict{}, nil
	}
	Judge(ctx, Dependencies{CommandRule: rule}, Request{Input: "ls", DryRun: true})
	Judge(ctx, Dependencies{CommandRule: rule}, Request{Input: "ls"})
	assert.Equal(t, []bool{false, true}, bounded)
}
