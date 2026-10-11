package guard

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// A judgment past the host's timeout is counted once, by whichever later judgment finishes
// first; one still inside it is left alone, since it may be running beside this one.
func TestFinishCountsEachUnfinishedJudgmentOnce(t *testing.T) {
	cacheDir, now := t.TempDir(), time.Now()
	beginJudgment(cacheDir, "claude-code/s1", now.Add(-time.Minute))
	beginJudgment(cacheDir, "claude-code/s1", now.Add(-time.Minute-time.Millisecond))
	running := beginJudgment(cacheDir, "claude-code/s1", now.Add(-time.Second))
	beginJudgment(cacheDir, "claude-code/s2", now.Add(-time.Minute))

	assert.Equal(t, 2, beginJudgment(cacheDir, "claude-code/s1", now).finish(now))
	assert.Equal(t, 0, beginJudgment(cacheDir, "claude-code/s1", now).finish(now))
	assert.Equal(t, 0, running.finish(now))
}

func TestBeginJudgmentMarksNothingWithoutAKey(t *testing.T) {
	assert.Equal(t, judgmentMark{}, beginJudgment(t.TempDir(), "", time.Now()))
	assert.Equal(t, judgmentMark{}, beginJudgment("", "claude-code/s1", time.Now()))
	assert.Equal(t, 0, judgmentMark{}.finish(time.Now()))
}

func TestUnfinishedNote(t *testing.T) {
	assert.Equal(t, "1 earlier guard check in this session never finished and was allowed: "+
		"the host stopped waiting for a verdict and ran the call with no rule applied.", unfinishedNote(1))
	assert.Contains(t, unfinishedNote(3), "3 earlier guard checks in this session never finished and were allowed")
}

// The next judgment that finishes in a session tells it about the checks its host gave up
// on, once.
func TestJudgeReportsChecksThatNeverFinished(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	beginJudgment(cacheDir, FactsKey("claude-code", "s1"), time.Now().Add(-time.Minute))

	first := Judge(ctx, Dependencies{}, Request{Host: "claude-code", Session: "s1", Input: "ls"})
	assert.Equal(t, verdictWithRule("advise", string(advisoryCommandRuleFailed)), unworded(first))
	assert.Contains(t, first.Context, unfinishedNote(1))

	again := Judge(ctx, Dependencies{}, Request{Host: "claude-code", Session: "s1", Input: "ls"})
	assert.NotContains(t, again.Context, "never finished")
}

func TestUnfinishedKind(t *testing.T) {
	assert.Equal(t, advisoryWriteRuleFailed, unfinishedKind(hookToolWrite))
	assert.Equal(t, advisoryCommandRuleFailed, unfinishedKind(hookToolCommand))
}
