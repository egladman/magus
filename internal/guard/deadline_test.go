package guard

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/trail"
)

func TestPhaseBudgetSplitsWhatRemains(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), judgeReserve+2*time.Second)
	defer cancel()
	got := phaseBudget(ctx, 1, 2)
	assert.LessOrEqual(t, got, time.Second)
	assert.Greater(t, got, 900*time.Millisecond)
	assert.Greater(t, phaseBudget(ctx, 2, 4), 900*time.Millisecond)

	assert.Equal(t, HookBudget*2/3, phaseBudget(t.Context(), 2, 3), "with no deadline, the same proportion of the hook's")

	gone, cancelGone := context.WithTimeout(t.Context(), judgeReserve/2)
	defer cancelGone()
	assert.LessOrEqual(t, phaseBudget(gone, 1, 1), time.Duration(0))
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
	assert.WithinDuration(t, time.Now().Add(HookBudget), got, time.Second)
}

// A load that failed after the deadline passed lapsed; one that answered did not, however
// late, and one with no deadline has no budget to lapse against.
func TestTimeRuleLoad(t *testing.T) {
	expired, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	<-expired.Done()
	failed := errors.New("magusfile: context deadline exceeded")

	assert.True(t, TimeRuleLoad(expired, func() error { return failed }).Lapsed)
	assert.False(t, TimeRuleLoad(expired, func() error { return nil }).Lapsed)

	unbounded := TimeRuleLoad(t.Context(), func() error { return failed })
	assert.Equal(t, trail.Phase{Name: phaseLoadRules, ElapsedMs: unbounded.ElapsedMs}, unbounded)
}

func TestLapseClause(t *testing.T) {
	assert.Equal(t, "the rule did not answer within 2.1s", lapseClause("the rule", "answer", 2140*time.Millisecond))
	assert.Equal(t, "the rule did not answer within 50ms", lapseClause("the rule", "answer", 50*time.Millisecond))
	assert.Equal(t, "the rule had no time left before the guard's deadline", lapseClause("the rule", "answer", 0))
}

// A rule load that ran out of time is judged as a phase that lapsed, not as a broken
// magusfile: a write is denied naming the load and its budget, a read goes through with the
// same named in its note.
func TestLapsedRuleLoadDeniesAWriteAndAdvisesARead(t *testing.T) {
	deps := Dependencies{
		LoadFailure: errors.New("magusfile: context deadline exceeded"),
		LoadRules:   trail.Phase{Name: phaseLoadRules, ElapsedMs: 6600, BudgetMs: 6500, Lapsed: true},
	}

	ctx, _ := spawnFixture(t)
	write := Judge(ctx, deps, Request{Host: "claude-code", Session: "s1", Input: "notes.txt", IsPath: true})
	assert.Equal(t, "deny", write.Decision)
	assert.Contains(t, write.Reason, "magus ran out of time judging this file write: loading this workspace's guard rules did not finish within 6.5s.")

	read := Judge(ctx, deps, Request{Host: "claude-code", Session: "s1", Input: "ls"})
	assert.Equal(t, "advise", read.Decision)
	assert.Contains(t, read.Context, "The working tree's magus\\guard.command rule judged nothing: the magusfile did not load within 6.5s")
}
