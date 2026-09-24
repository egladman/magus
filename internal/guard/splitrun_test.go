package guard

import (
	"os"
	"testing"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The pure parser: verb, target and projects off a magus invocation's own args, global
// flags before the verb skipped the way cmd/magus's peekSub does.
func TestSplitRunInvocationParses(t *testing.T) {
	for _, tc := range []struct {
		name         string
		args         []string
		verb, target string
		projects     []string
		ok           bool
	}{
		{name: "plain run", args: []string{"run", "lint", ".", "docs"}, verb: "run", target: "lint", projects: []string{".", "docs"}, ok: true},
		{name: "affected with no projects", args: []string{"affected", "ci"}, verb: "affected", target: "ci", ok: true},
		{name: "a global value flag before the verb", args: []string{"--root", ".", "run", "lint", ".", "docs"}, verb: "run", target: "lint", projects: []string{".", "docs"}, ok: true},
		{name: "a global flag=value before the verb", args: []string{"--root=.", "affected", "ci"}, verb: "affected", target: "ci", ok: true},
		{name: "a bare boolean flag before the verb", args: []string{"-v", "run", "build", "api"}, verb: "run", target: "build", projects: []string{"api"}, ok: true},
		{name: "not run or affected", args: []string{"doctor"}},
		{name: "the target position is itself a flag", args: []string{"run", "--dry-run"}},
		{name: "projects stop at a following flag", args: []string{"run", "lint", ".", "--silent", "docs"}, verb: "run", target: "lint", projects: []string{"."}, ok: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			verb, target, projects, ok := splitRunInvocation(tc.args)
			require.Equal(t, tc.ok, ok)
			if !ok {
				return
			}
			assert.Equal(t, tc.verb, verb)
			assert.Equal(t, tc.target, target)
			assert.Equal(t, tc.projects, projects)
		})
	}
}

// Charms are part of the target identity (lint and lint:rw are different targets), and
// charm ORDER is not (lint:a,b and lint:b,a are the same target).
func TestSplitRunTargetIdentity(t *testing.T) {
	lint, ok := targetIdentity("lint")
	require.True(t, ok)
	lintRW, ok := targetIdentity("lint:rw")
	require.True(t, ok)
	assert.NotEqual(t, lint, lintRW)

	ab, ok := targetIdentity("generate:a,b")
	require.True(t, ok)
	ba, ok := targetIdentity("generate:b,a")
	require.True(t, ok)
	assert.Equal(t, ab, ba, "charm order does not change the target")

	_, ok = targetIdentity("")
	assert.False(t, ok)
}

// The ONE-LINE shape: chainedRunRe already flags this line, and a shared target across
// every invocation on it narrows the text to the combined form. A chain of different
// targets keeps chained-run's own text, which stays its domain.
func TestSplitRunLineGivesCombinedForm(t *testing.T) {
	v := Evaluate(testDependencies(), "magus run lint . && magus run lint docs")
	assert.Empty(t, v.Deny)
	assert.Contains(t, v.Context, "magus run lint . docs", "the combined form the reader can run instead")
	assert.Contains(t, v.Context, "runs both in one invocation")
	assert.Equal(t, denyRuleName(advisorySplitRun), v.Rule.Name)

	different := Evaluate(testDependencies(), "magus run generate . && magus run lint .")
	assert.Contains(t, different.Context, "compose through ctx.needs", "different targets stay chained-run's domain")
	assert.NotContains(t, different.Context, "runs both in one invocation")
}

// Charms differing on one line is the same "different target" shape as a differing
// name: lint and lint:rw are never folded into one combined command.
func TestSplitRunLineDifferentCharmsStaysChainedRun(t *testing.T) {
	v := Evaluate(testDependencies(), "magus run lint . && magus run lint:rw docs")
	assert.Contains(t, v.Context, "compose through ctx.needs")
	assert.NotContains(t, v.Context, "runs both in one invocation")
}

// The ACROSS-CALLS shape, exercised through gradeSplitRun directly: no cache dir and no
// prior call both mean nothing to compare against.
func TestGradeSplitRunSilentWithoutFacts(t *testing.T) {
	_, ok := gradeSplitRun(hint.NewGate("", "session-1"), "magus run lint .")
	assert.False(t, ok, "a gate with no cache base remembers nothing")

	_, ok = gradeSplitRun(hint.NewGate(t.TempDir(), "session-1"), "magus run lint .")
	assert.False(t, ok, "a first call has no prior call to compare against")
}

// A pair outside splitRunWindow is not the same mistake; readLastRun's age comes from the
// fact file's own mtime.
func TestGradeSplitRunSilentOutsideTheWindow(t *testing.T) {
	facts := hint.NewGate(t.TempDir(), "session-1")
	_, ok := gradeSplitRun(facts, "magus run lint .")
	require.False(t, ok)

	path := hint.MarkerPath(facts.CacheDir(), facts.Session(), factsLastRun)
	old := time.Now().Add(-splitRunWindow - time.Minute)
	require.NoError(t, os.Chtimes(path, old, old))

	_, ok = gradeSplitRun(facts, "magus run lint docs")
	assert.False(t, ok, "a pair an hour apart is unrelated work, not a split call")
}

// gradeSplitRun records every qualifying call, whatever it decides: the fact behind a
// match must be the CURRENT call, not the stale pair that produced it.
func TestGradeSplitRunRecordsEveryCall(t *testing.T) {
	facts := hint.NewGate(t.TempDir(), "session-1")

	_, ok := gradeSplitRun(facts, "magus run lint .")
	require.False(t, ok)

	text, ok := gradeSplitRun(facts, "magus run lint docs")
	require.True(t, ok)
	assert.Contains(t, text, "magus run lint . docs")

	// A third call compares against the SECOND call, not the first: the fact was
	// overwritten, not accumulated.
	text, ok = gradeSplitRun(facts, "magus run lint web")
	require.True(t, ok)
	assert.Contains(t, text, "magus run lint docs")
	assert.Contains(t, text, "magus run lint web")
	assert.NotContains(t, text, "magus run lint .", "the first call is no longer the recorded fact")
}

// The full pipeline: Judge holds the cross-call advisory to one firing per session, keyed
// by the session's own facts (the same `facts` gate scope-drift's touched-projects file
// uses), not a new store.
func TestSplitRunAcrossCallsFiresOncePerSession(t *testing.T) {
	ctx, _ := spawnFixture(t)
	deps := Dependencies{}

	first := Judge(ctx, deps, Request{Input: "magus run lint .", Host: "test-host", Session: "s1"})
	assert.Empty(t, first.Context, "a first call has nothing to compare against")

	second := Judge(ctx, deps, Request{Input: "magus run lint docs", Host: "test-host", Session: "s1"})
	assert.Contains(t, second.Context, "runs both in one invocation")
	assert.Equal(t, string(advisorySplitRun), second.Rule)

	third := Judge(ctx, deps, Request{Input: "magus run lint web", Host: "test-host", Session: "s1"})
	assert.Empty(t, third.Context, "a second differing pair in the same session stays silent")
}

// The same target on the same projects is the same call, not a split one.
func TestSplitRunAcrossCallsSilentForIdenticalCalls(t *testing.T) {
	ctx, _ := spawnFixture(t)
	deps := Dependencies{}

	Judge(ctx, deps, Request{Input: "magus run lint .", Host: "test-host", Session: "s2"})
	repeat := Judge(ctx, deps, Request{Input: "magus run lint .", Host: "test-host", Session: "s2"})
	assert.Empty(t, repeat.Context)
}

// Different targets are chained-run's domain on one line and simply unrelated across
// calls; either way this rule stays silent.
func TestSplitRunAcrossCallsSilentForDifferentTargets(t *testing.T) {
	ctx, _ := spawnFixture(t)
	deps := Dependencies{}

	Judge(ctx, deps, Request{Input: "magus run format .", Host: "test-host", Session: "s3"})
	v := Judge(ctx, deps, Request{Input: "magus run lint docs", Host: "test-host", Session: "s3"})
	assert.Empty(t, v.Context)
}

// Charms are part of the target identity across calls too.
func TestSplitRunAcrossCallsSilentForDifferentCharms(t *testing.T) {
	ctx, _ := spawnFixture(t)
	deps := Dependencies{}

	Judge(ctx, deps, Request{Input: "magus run generate .", Host: "test-host", Session: "s4"})
	v := Judge(ctx, deps, Request{Input: "magus run generate:rw docs", Host: "test-host", Session: "s4"})
	assert.Empty(t, v.Context)
}

// Global flags before the verb (`--root .`) must not be misread as the target.
func TestSplitRunAcrossCallsSkipsGlobalFlags(t *testing.T) {
	ctx, _ := spawnFixture(t)
	deps := Dependencies{}

	Judge(ctx, deps, Request{Input: "magus --root . run lint .", Host: "test-host", Session: "s5"})
	v := Judge(ctx, deps, Request{Input: "magus run lint docs", Host: "test-host", Session: "s5"})
	assert.Contains(t, v.Context, "runs both in one invocation")
}
