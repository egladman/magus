package guard

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lapseNow judges req by a judgment that runs judge's setup and then never answers, past a
// short deadline.
func lapseNow(ctx context.Context, t *testing.T, base Dependencies, req Request, setup func(context.Context)) Verdict {
	t.Helper()
	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	return JudgeWithin(short, base, req, func(ctx context.Context) Verdict {
		setup(ctx)
		<-release
		return Verdict{Decision: decisionPass}
	})
}

// A worker's loop while every judgment lapses: it sleeps until a background task's output
// file is non-empty, then prints it. The lines only read, so the lapse ladder would pass
// them unjudged; busy-wait refuses them on every lapse, past the degrade too, graded by the
// settings the judgment loaded before it stalled.
func TestLapsedJudgmentStillRefusesAPollingLoop(t *testing.T) {
	ctx, _ := spawnFixture(t)
	capture := filepath.Join(t.TempDir(), "tasks", "bglno2daz.output")
	loops := []string{
		"until [ -s " + capture + " ]; do sleep 5; done; cat " + capture,
		"until grep -q '^summary:' " + capture + "; do sleep 5; done",
		"while ! test -s " + capture + "; do echo waiting; sleep 5; done",
	}
	settle := func(ctx context.Context) { judgmentPhases(ctx).settle(strict(Dependencies{})) }
	// One session, so the lapses count up past the degrade.
	for i, line := range append(loops, loops[0]) {
		v := lapseNow(ctx, t, Dependencies{}, Request{Input: line, Host: "claude-code", Session: "s1"}, settle)
		assert.Equal(t, verdictWithRule(decisionDeny, string(denyRuleBusyWait)), unworded(v), "lapse %d: %s", i+1, line)
	}
}

// A judgment that lapses before it loads anything grades by the settings the last judgment
// in the cache dir applied, so a workspace that raised busy-wait to deny keeps it raised.
func TestLapseBeforeTheLoadGradesByTheLastAppliedSettings(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	line := "until [ -s out.txt ]; do sleep 5; done"
	req := Request{Input: line, Host: "claude-code", Session: "s1"}
	nothing := func(context.Context) {}

	assert.Equal(t, string(advisoryUnfinished), lapseNow(ctx, t, Dependencies{}, req, nothing).Rule,
		"with no settings on record busy-wait only advises, as it does by default")

	rememberSettings(cacheDir, strict(Dependencies{}).Builtins)
	assert.Equal(t, verdictWithRule(decisionDeny, string(denyRuleBusyWait)), unworded(lapseNow(ctx, t, Dependencies{}, req, nothing)))
}

// A destructive VCS verb is refused by its own rule on a lapse, and a push is never
// degraded into running unjudged: its gate is in the run log only a finished judgment reads.
func TestLapseNeverDegradesADestructiveVerbOrAPush(t *testing.T) {
	ctx, _ := spawnFixture(t)
	nothing := func(context.Context) {}
	reset := "git reset --hard origin/main"
	want := Evaluate(Dependencies{}, reset)
	require.NotEmpty(t, want.Deny, "the destructive verb denies by default")
	for range lapsesBeforeDegrade + 2 {
		v := lapseNow(ctx, t, Dependencies{}, Request{Input: reset, Host: "claude-code", Session: "s1"}, nothing)
		assert.Equal(t, verdictWithRule(decisionDeny, want.RuleName()), unworded(v))
	}
	for i := range lapsesBeforeDegrade + 2 {
		v := lapseNow(ctx, t, Dependencies{}, Request{Input: "git push origin topic", Host: "claude-code", Session: "s2"}, nothing)
		assert.Equal(t, verdictWithRule(decisionDeny, string(advisoryUnfinished)), unworded(v), "lapse %d", i+1)
	}
}

// A judgment that answers in time records the settings it applied, and a lapse reads them.
func TestRememberSettingsRoundTrips(t *testing.T) {
	dir := t.TempDir()
	assert.Nil(t, recallSettings(dir))
	settings := strict(Dependencies{}).Builtins
	rememberSettings(dir, settings)
	assert.Equal(t, settings, recallSettings(dir))
	rememberSettings(dir, nil)
	assert.Nil(t, recallSettings(dir), "the defaults are recorded as none")
}
