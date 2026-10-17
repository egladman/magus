package guard

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// deadPID is the pid of a process that has exited: this test binary, run to no test.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	require.NoError(t, cmd.Run())
	return cmd.Process.Pid
}

// markOwnedBy is beginJudgment for a judgment run by the process owner.
func markOwnedBy(t *testing.T, cacheDir, key string, started time.Time, owner int) judgmentMark {
	t.Helper()
	m := beginJudgment(cacheDir, key, started)
	path := filepath.Join(m.dir, strconv.FormatInt(started.UnixNano(), 10)+"-"+strconv.Itoa(owner))
	require.NoError(t, os.Rename(m.path, path))
	m.path = path
	return m
}

// A judgment whose process ended without ending it is counted once, by whichever later
// judgment ends first. One whose process still runs is left alone, however old: it may be
// running beside this one.
func TestAbandonedCountsEachUnfinishedJudgmentOnce(t *testing.T) {
	cacheDir, now, dead := t.TempDir(), time.Now(), deadPID(t)
	markOwnedBy(t, cacheDir, "claude-code/s1", now.Add(-time.Second), dead)
	markOwnedBy(t, cacheDir, "claude-code/s1", now.Add(-time.Second-time.Millisecond), dead)
	running := beginJudgment(cacheDir, "claude-code/s1", now.Add(-time.Minute))
	markOwnedBy(t, cacheDir, "claude-code/s2", now.Add(-time.Minute), dead)

	assert.Equal(t, 2, beginJudgment(cacheDir, "claude-code/s1", now).abandoned())
	assert.Equal(t, 0, beginJudgment(cacheDir, "claude-code/s1", now).abandoned(), "each is counted once")
	assert.FileExists(t, running.path, "a judgment still running is not taken to be abandoned")
}

func TestBeginJudgmentMarksNothingWithoutASessionKey(t *testing.T) {
	assert.Equal(t, judgmentMark{}, beginJudgment(t.TempDir(), "", time.Now()))
	assert.Equal(t, judgmentMark{}, beginJudgment("", "claude-code/s1", time.Now()))
	assert.Equal(t, 0, judgmentMark{}.abandoned())
}

// A judgment that answers while its host still waits removes its mark, however slow. One its
// host stopped waiting for keeps it: the host has run the call without its verdict, and the
// `magus shell` finishing late must not erase the record of that. The host stopped waiting
// when the process that started this one is gone.
func TestEndKeepsTheMarkOfAJudgmentItsHostAbandoned(t *testing.T) {
	cacheDir, now := t.TempDir(), time.Now()
	slow := beginJudgment(cacheDir, "claude-code/s1", now.Add(-15*time.Second))
	slow.end()
	assert.NoFileExists(t, slow.path, "a slow judgment its host still waits on")

	orphan := beginJudgment(cacheDir, "claude-code/s1", now.Add(-5*time.Second))
	orphan.parent = os.Getppid() + 1
	orphan.end()
	assert.FileExists(t, orphan.path, "the glue the host waited on is gone, so the host stopped waiting")
}

func TestUnfinishedNoteCountsTheSkippedChecks(t *testing.T) {
	assert.Equal(t, "1 earlier guard check in this session never finished: "+
		"the host stopped waiting for a verdict, so no rule judged the call.", unfinishedNote(1))
	assert.Contains(t, unfinishedNote(3), "3 earlier guard checks in this session never finished:")
}

// The next judgment that finishes in a session tells it about the checks its host gave up
// on, once, under the notice of its own.
func TestJudgeReportsChecksThatNeverFinished(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	markOwnedBy(t, cacheDir, FactsKey("claude-code", "s1"), time.Now().Add(-time.Minute), deadPID(t))

	first := Judge(ctx, Dependencies{}, Request{Host: "claude-code", Session: "s1", Input: "ls"})
	assert.Equal(t, verdictWithRule(decisionAdvise, string(advisoryUnfinished)), unworded(first))
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
