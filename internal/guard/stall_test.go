package guard

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

func TestStallLevel(t *testing.T) {
	t.Parallel()
	light := builtin.Setting{Decision: builtin.Advise}
	assert.Equal(t, 3, stallLevel(stallRecord{}, string(denyRuleLeaseWrite), light), "a refusal only the root can clear")
	assert.Equal(t, 1, stallLevel(stallRecord{}, string(denyRuleSedInPlace), light))
	assert.Equal(t, 2, stallLevel(stallRecord{Run: 1, Last: string(denyRuleSedInPlace)}, string(denyRuleSedInPlace), light), "the same rule again")
	assert.Equal(t, 1, stallLevel(stallRecord{Run: 2, Last: string(denyRuleRawTool)}, string(denyRuleSedInPlace), light))
	assert.Equal(t, 2, stallLevel(stallRecord{Run: 3, Last: string(denyRuleRawTool)}, string(denyRuleSedInPlace), light), "a run of four")
	assert.Equal(t, 2, stallLevel(stallRecord{Run: 2, Last: string(denyRuleRawTool)}, string(denyRuleSedInPlace), builtin.Setting{Decision: builtin.Advise, Calls: 3}))
}

func TestGradeStallNamesTheExitOnTheVerdictLine(t *testing.T) {
	t.Parallel()
	at := location{cacheDir: t.TempDir()}
	on := withSetting(string(denyRuleStall), builtin.Advise)
	blocked := types.GuardJudgment{Decision: decisionDeny, Rule: string(denyRuleLeaseWrite), Reason: "outside the write paths.\nthe rationale"}

	v := gradeStall(on, blocked, "job-a", at)
	assert.Equal(t, fmt.Sprintf("outside the write paths. Only the root can clear this: exit with `%s`, naming it in unresolved_risks.\nthe rationale",
		hint.JobExit.With("job-a", "--stdin")), v.Reason)

	assert.Equal(t, blocked, gradeStall(Dependencies{}, blocked, "job-a", at), "off by default")
	assert.Equal(t, blocked, gradeStall(on, blocked, "", at), "no lease, no job to exit")
	clearable := blocked
	clearable.Rule = string(denyRuleLeaseGate)
	assert.Equal(t, clearable, gradeStall(on, clearable, "job-a", at), "the worker clears lease-gate by running its check")

	stallGate(at.cacheDir, "job-a").MarkFired(stallRowKind(string(denyRuleLeaseWrite)))
	assert.Equal(t, blocked, gradeStall(on, blocked, "job-a", at), "said once per lease and rule")
}

// The whole path: a leased worker's write outside its paths is refused naming the exit,
// and the root reads one attention row stating the facts however often the worker is
// refused.
func TestStallRoutesAnUnclearableBlockToTheRoot(t *testing.T) {
	testkit.Isolate(t)
	ctx, root := fleetFixture(t, fixture.FleetLeases()...)
	deps := withSetting(string(denyRuleStall), builtin.Advise)
	write := func() types.GuardJudgment {
		return Judge(ctx, deps, Request{Input: filepath.Join(root, "README.md"), IsPath: true, Session: "s", Host: "test-host", Lease: "lease-b"})
	}

	first := write()
	require.Equal(t, types.GuardJudgment{Decision: decisionDeny, Rule: string(denyRuleLeaseWrite)}, judgedAs(first))
	assert.Contains(t, first.Reason, hint.JobExit.With("lease-b", "--stdin"))
	again := write()
	assert.NotContains(t, again.Reason, hint.JobExit.With("lease-b", "--stdin"), "said once")

	dir, err := sessions.Dir(root)
	require.NoError(t, err)
	fold, err := sessions.ReadAll(dir)
	require.NoError(t, err)
	queue := sessions.AttentionQueue(fold)
	require.Len(t, queue, 1, "one row per lease and rule")
	assert.Equal(t, "lease-b", queue[0].Lease)
	assert.True(t, strings.HasPrefix(queue[0].Message, "job lease-b is blocked by lease-write, which only the root can clear: "), queue[0].Message)
	assert.Contains(t, queue[0].Message, "a model magus has no record of")
	assert.Contains(t, queue[0].Message, "refusals under this lease: 1.")

	rec := readStall(stallGate(hookLocation(ctx, deps).cacheDir, "lease-b"))
	assert.Equal(t, stallRecord{Refusals: 2, Run: 2, Last: string(denyRuleLeaseWrite), Level: 3}, rec)
}
