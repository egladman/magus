package guard

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A judgment past the host's timeout is counted once, by whichever later judgment ends
// first; one still inside it is left alone, since it may be running beside this one.
func TestAbandonedCountsEachUnfinishedJudgmentOnce(t *testing.T) {
	cacheDir, now := t.TempDir(), time.Now()
	beginJudgment(cacheDir, "claude-code/s1", now.Add(-time.Minute))
	beginJudgment(cacheDir, "claude-code/s1", now.Add(-time.Minute-time.Millisecond))
	running := beginJudgment(cacheDir, "claude-code/s1", now.Add(-time.Second))
	beginJudgment(cacheDir, "claude-code/s2", now.Add(-time.Minute))

	assert.Equal(t, 2, beginJudgment(cacheDir, "claude-code/s1", now).abandoned(now))
	assert.Equal(t, 0, beginJudgment(cacheDir, "claude-code/s1", now).abandoned(now))
	assert.Equal(t, 0, running.abandoned(now))
}

func TestBeginJudgmentMarksNothingWithoutASessionKey(t *testing.T) {
	assert.Equal(t, judgmentMark{}, beginJudgment(t.TempDir(), "", time.Now()))
	assert.Equal(t, judgmentMark{}, beginJudgment("", "claude-code/s1", time.Now()))
	assert.Equal(t, 0, judgmentMark{}.abandoned(time.Now()))
}

// A judgment that answers inside the host's timeout removes its mark. One that ran past it
// keeps the mark: its host has already run the call unjudged, and an orphaned `magus shell`
// finishing late must not erase the record of that.
func TestEndKeepsTheMarkOfAJudgmentItsHostAbandoned(t *testing.T) {
	cacheDir, now := t.TempDir(), time.Now()
	answered := beginJudgment(cacheDir, "claude-code/s1", now)
	answered.end(now.Add(time.Second))
	assert.NoFileExists(t, answered.path)

	orphan := beginJudgment(cacheDir, "claude-code/s1", now.Add(-hostHookTimeout))
	orphan.end(now)
	assert.FileExists(t, orphan.path)
	assert.Equal(t, 1, beginJudgment(cacheDir, "claude-code/s1", now.Add(unfinishedAfter)).abandoned(now.Add(unfinishedAfter)))
}

func TestUnfinishedNoteCountsTheSkippedChecks(t *testing.T) {
	assert.Equal(t, "1 earlier guard check in this session never finished and was allowed: "+
		"the host stopped waiting for a verdict and ran the call with no rule applied.", unfinishedNote(1))
	assert.Contains(t, unfinishedNote(3), "3 earlier guard checks in this session never finished and were allowed")
}

// The next judgment that finishes in a session tells it about the checks its host gave up
// on, once, under the notice of its own.
func TestJudgeReportsChecksThatNeverFinished(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	beginJudgment(cacheDir, FactsKey("claude-code", "s1"), time.Now().Add(-time.Minute))

	first := Judge(ctx, Dependencies{}, Request{Host: "claude-code", Session: "s1", Input: "ls"})
	assert.Equal(t, verdictWithRule("advise", string(advisoryUnfinished)), unworded(first))
	assert.Contains(t, first.Context, unfinishedNote(1))

	again := Judge(ctx, Dependencies{}, Request{Host: "claude-code", Session: "s1", Input: "ls"})
	assert.NotContains(t, again.Context, "never finished")
}

// BeginJudgment marks the judgment before any rule loads, keyed on the session an envelope
// names, and Judge ends that mark rather than leaving a second one.
func TestBeginJudgmentMarksAHookBeforeItsRulesLoad(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	envelope := `{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"Bash","tool_input":{"command":"ls"}}`
	noCacheDir := func(string) (string, error) { return "", os.ErrNotExist }
	req := Request{Host: "claude-code", Input: envelope}

	marked := BeginJudgment(ctx, noCacheDir, req)
	m := begunJudgment(marked)
	require.NotEmpty(t, m.path, "the location is pinned, so no cache dir is resolved")
	assert.Equal(t, filepath.Join(cacheDir, unfinishedDir), filepath.Dir(m.dir))
	assert.FileExists(t, m.path)

	Judge(marked, Dependencies{}, req)
	entries, err := os.ReadDir(m.dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "the mark is ended, and no second one is left")

	for _, skipped := range []Request{
		{Host: "claude-code", Session: "s1"},
		{Host: "claude-code", Session: "s1", Input: "ls", Observe: true},
		{Host: "claude-code", Session: "s1", Input: "ls", DryRun: true},
		{Host: "claude-code", Session: "s1", Input: "hello", Message: true},
	} {
		assert.Empty(t, begunJudgment(BeginJudgment(ctx, noCacheDir, skipped)).path, "%+v", skipped)
	}
	assert.Empty(t, begunJudgment(context.Background()).path)
}
