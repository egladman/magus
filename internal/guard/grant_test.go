package guard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
)

// grantRows are two live parentless rows, so a root session holding either commits from
// its own checkout and hooks-off is the only rule a --no-verify commit meets.
func grantRows() (granted, other types.Job) {
	granted, other = narrowLease(), narrowLease()
	granted.ID, other.ID = "guard/granted", "guard/other"
	return granted, other
}

func TestGrantedHooksOffAdvisesForThatLeaseAlone(t *testing.T) {
	granted, other := grantRows()
	ctx, root := fleetFixture(t, granted, other)
	at := ctx.Value(locationKey{}).(location)
	store := job.NewStore(job.Location{CacheDir: at.cacheDir, StateBase: fixture.StateBase(t), Root: root, Actor: &job.Actor{}})
	_, err := store.Grant(t.Context(), granted.ID, string(denyRuleHooksOff), 0)
	require.NoError(t, err)

	line := bashCall(t, "s1", "", "git commit --no-verify -m x")
	v := Judge(ctx, Dependencies{}, Request{Input: line, Host: "claude-code", Lease: granted.ID})
	require.Equal(t, types.GuardJudgment{Decision: decisionAdvise, Rule: string(denyRuleHooksOff)}, judgedAs(v), "the grant lowers hooks-off: %s", v.Reason)
	assert.Contains(t, v.Context, "grant")
	assert.Contains(t, v.Context, "until the job ends")

	v = Judge(ctx, Dependencies{}, Request{Input: line, Host: "claude-code", Lease: other.ID})
	assert.Equal(t, types.GuardJudgment{Decision: decisionDeny, Rule: string(denyRuleHooksOff)}, judgedAs(v), "another lease's call is still refused")

	_, err = store.Revoke(t.Context(), granted.ID, string(denyRuleHooksOff))
	require.NoError(t, err)
	v = Judge(ctx, Dependencies{}, Request{Input: line, Host: "claude-code", Lease: granted.ID})
	assert.Equal(t, types.GuardJudgment{Decision: decisionDeny, Rule: string(denyRuleHooksOff)}, judgedAs(v), "a revoked grant lowers nothing")
}

func TestAgentGrantIsRefused(t *testing.T) {
	granted, _ := grantRows()
	ctx, _ := fleetFixture(t, granted)

	for _, line := range []string{
		"magus job grant guard/granted hooks-off",
		"./magus job grant --revoke guard/granted hooks-off",
		"/usr/local/bin/magus -C /tmp job grant guard/granted hooks-off --for 1h",
		"cd /tmp && magus job grant guard/granted hooks-off",
		"env A=1 magus job grant guard/granted hooks-off",
		"bash -c 'magus job grant guard/granted hooks-off'",
	} {
		for _, lease := range []string{"", granted.ID} {
			v := Judge(ctx, Dependencies{}, Request{Input: line, Lease: lease})
			assert.Equal(t, types.GuardJudgment{Decision: decisionDeny, Rule: string(denyRuleGrantIsAPersons)}, judgedAs(v), "%q under %q", line, lease)
		}
	}
	for _, line := range []string{"magus job ls", "echo magus job grant", "magus describe rules"} {
		assert.False(t, denyAgentGrant(line, DialectBash).refused(), "%q", line)
	}
}

func TestAgentWaiveIsRefused(t *testing.T) {
	for _, line := range []string{
		"magus gate waive HEAD --reason ci-down",
		"./magus gate waive --revoke HEAD",
		"/usr/local/bin/magus -C /tmp gate waive abc1234 --reason x",
		"cd /tmp && magus gate waive HEAD --reason x",
		"env A=1 magus gate waive HEAD --reason x",
		"bash -c 'magus gate waive HEAD --reason x'",
	} {
		d := denyAgentWaive(line, DialectBash)
		assert.Equal(t, fmt.Sprintf("magus workspace: only a person runs `%s gate waive`; ask the person to type it.", hint.BinaryName()), d.Say, "%q", line)
	}
	for _, line := range []string{"magus gate", "echo magus gate waive HEAD", "magus job grant x hooks-off", "magus describe rules"} {
		assert.False(t, denyAgentWaive(line, DialectBash).refused(), "%q", line)
	}
}

// waivedPush judges `git push` at revision in a workspace whose run log records no gate,
// after fill writes waivers to its store.
func waivedPush(t *testing.T, revision string, fill func(context.Context, *job.Store)) types.GuardJudgment {
	t.Helper()
	ctx, root := fleetFixture(t)
	at := ctx.Value(locationKey{}).(location)
	require.NoError(t, os.MkdirAll(filepath.Join(at.cacheDir, cache.RunsDir), 0o755))
	fill(t.Context(), job.NewStore(job.Location{CacheDir: at.cacheDir, StateBase: fixture.StateBase(t), Root: root, Actor: &job.Actor{}}))
	deps := Dependencies{Revision: func(context.Context, string, string) string { return revision }}
	return Judge(ctx, deps, Request{Input: "git push origin HEAD", RendersAsk: true})
}

func TestGateWaiverLetsThatCommitAloneThrough(t *testing.T) {
	const waived = "abc1234def567890abc1234def567890abc12345"
	waive := func(ctx context.Context, store *job.Store) {
		w, err := WaiveGate(ctx, store, waived, "CI gates the release branch")
		require.NoError(t, err)
		assert.Equal(t, types.GateWaiver{Commit: waived, By: w.By, At: w.At, Reason: "CI gates the release branch"}, w)
		assert.NotZero(t, w.At)
	}

	v := waivedPush(t, "abc1234", waive)
	assert.NotEqual(t, string(denyRulePushUngated), v.Rule, "the waived commit goes out: %s", v.Reason)
	assert.NotEqual(t, decisionDeny, v.Decision)

	v = waivedPush(t, "fed9876", waive)
	assert.Equal(t, types.GuardJudgment{Decision: decisionDeny, Rule: string(denyRulePushUngated)}, judgedAs(v), "another commit is still refused")

	v = waivedPush(t, "abc1234", func(ctx context.Context, store *job.Store) {
		waive(ctx, store)
		_, err := RevokeGateWaiver(ctx, store, waived)
		require.NoError(t, err)
	})
	assert.Equal(t, types.GuardJudgment{Decision: decisionDeny, Rule: string(denyRulePushUngated)}, judgedAs(v), "a revoked waiver covers nothing")
}

func TestGateWaiverIsAPersonsKeystroke(t *testing.T) {
	_, root := fleetFixture(t)
	leased := job.NewStore(job.Location{StateBase: fixture.StateBase(t), Root: root, Actor: &job.Actor{Lease: "guard/worker"}})
	_, err := WaiveGate(t.Context(), leased, "abc1234def5678", "skip")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `acts under lease "guard/worker"`)

	person := job.NewStore(job.Location{StateBase: fixture.StateBase(t), Root: root, Actor: &job.Actor{}})
	_, err = WaiveGate(t.Context(), person, "abc1234def5678", " ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--reason")
}

func TestWaiverNoteNamesWhoWhenAndWhy(t *testing.T) {
	w := types.GateWaiver{Commit: "abc1234def5678", By: types.Origin{User: "eli"}, At: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC).Unix(), Reason: "CI is down"}
	want := fmt.Sprintf("magus workspace: abc1234 goes out ungated under the waiver %s typed at 2026-10-24T12:00:00Z: CI is down", w.By.Label())
	assert.Equal(t, want, waiverNote("abc1234", w))
}
