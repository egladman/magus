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

// declaredTimeout is a host whose harness declares d.
func declaredTimeout(d time.Duration) hookTimeout {
	return func() (time.Duration, bool) { return d, true }
}

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
// judgment ends first. One whose process still runs is counted only past the hook timeout
// its host declares, since it may be running beside this one; with none declared it is
// left alone.
func TestAbandonedCountsEachUnfinishedJudgmentOnce(t *testing.T) {
	cacheDir, now, dead := t.TempDir(), time.Now(), deadPID(t)
	markOwnedBy(t, cacheDir, "claude-code/s1", now.Add(-time.Second), dead)
	markOwnedBy(t, cacheDir, "claude-code/s1", now.Add(-time.Second-time.Millisecond), dead)
	running := beginJudgment(cacheDir, "claude-code/s1", now.Add(-time.Second))
	hung := beginJudgment(cacheDir, "claude-code/s1", now.Add(-time.Minute))
	markOwnedBy(t, cacheDir, "claude-code/s2", now.Add(-time.Minute), dead)

	assert.Equal(t, 2, beginJudgment(cacheDir, "claude-code/s1", now).abandoned(now, noHookTimeout))
	assert.Equal(t, 0, beginJudgment(cacheDir, "claude-code/s1", now).abandoned(now, noHookTimeout),
		"a judgment still running is not taken to be abandoned by a host that declares no hook timeout")
	assert.Equal(t, 0, beginJudgment(cacheDir, "claude-code/s1", now).abandoned(now, declaredTimeout(2*time.Minute)))
	assert.Equal(t, 1, running.abandoned(now, declaredTimeout(10*time.Second)), "past the declared timeout")
	assert.NoFileExists(t, hung.path)
	assert.FileExists(t, running.path)
}

func TestBeginJudgmentMarksNothingWithoutASessionKey(t *testing.T) {
	assert.Equal(t, judgmentMark{}, beginJudgment(t.TempDir(), "", time.Now()))
	assert.Equal(t, judgmentMark{}, beginJudgment("", "claude-code/s1", time.Now()))
	assert.Equal(t, 0, judgmentMark{}.abandoned(time.Now(), noHookTimeout))
}

// A judgment that answers while its host still waits removes its mark. One its host stopped
// waiting for keeps it: the host has run the call without its verdict, and the `magus shell`
// finishing late must not erase the record of that. The host stopped waiting when the
// process that started this one is gone, however soon, or past the hook timeout its harness
// declares; a host that declares none is not taken to have given up on a slow judgment.
func TestEndKeepsTheMarkOfAJudgmentItsHostAbandoned(t *testing.T) {
	cacheDir, now := t.TempDir(), time.Now()
	for name, c := range map[string]struct {
		ran     time.Duration
		timeout hookTimeout
		kept    bool
	}{
		"answered in time":                 {time.Second, declaredTimeout(10 * time.Second), false},
		"past the declared timeout":        {10 * time.Second, declaredTimeout(10 * time.Second), true},
		"inside a longer declared timeout": {15 * time.Second, declaredTimeout(time.Minute), false},
		"slow, with no timeout declared":   {15 * time.Second, noHookTimeout, false},
	} {
		m := beginJudgment(cacheDir, "claude-code/s1", now.Add(-c.ran))
		m.end(now, c.timeout)
		assert.Equal(t, c.kept, fileExists(m.path), name)
	}

	orphan := beginJudgment(cacheDir, "claude-code/s1", now.Add(-5*time.Second))
	orphan.parent = os.Getppid() + 1
	orphan.end(now, declaredTimeout(10*time.Second))
	assert.FileExists(t, orphan.path, "the glue the host waited on is gone, so the host stopped waiting")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
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
	assert.Equal(t, verdictWithRule("advise", string(advisoryUnfinished)), unworded(first))
	assert.Contains(t, first.Context, unfinishedNote(1))

	again := Judge(ctx, Dependencies{}, Request{Host: "claude-code", Session: "s1", Input: "ls"})
	assert.NotContains(t, again.Context, "never finished")
}

// The host's hook timeout is read off its harness, which opens the workspace. A judgment
// asks only while its own deadline has time left, so a slow read never holds a verdict past
// the bound its host is waiting on, and a check still running is then left uncounted.
func TestJudgeStopsAskingTheHostTimeoutAtItsDeadline(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	markOwnedBy(t, cacheDir, FactsKey("claude-code", "s1"), time.Now().Add(-time.Minute), os.Getpid())
	release := make(chan struct{})
	defer close(release)
	deps := Dependencies{HostHookTimeout: func(context.Context, string) (time.Duration, bool) {
		<-release
		return 10 * time.Second, true
	}}
	ctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()

	done := make(chan Verdict, 1)
	go func() { done <- Judge(ctx, deps, Request{Host: "claude-code", Session: "s1", Input: "ls"}) }()
	select {
	case v := <-done:
		assert.NotContains(t, v.Context, "never finished")
	case <-time.After(5 * time.Second):
		t.Fatal("Judge waited on the host's hook timeout past its own deadline")
	}
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
