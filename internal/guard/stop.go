package guard

import (
	"context"
	"fmt"
	"slices"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/types"
)

// denyRuleStopUnfiled names the refusal of a leased subagent's stop while its
// job row carries no filed result.
const denyRuleStopUnfiled denyRuleName = "stop-unfiled"

// judgeStopUnfiled refuses a leased subagent's first stop while the row it
// holds has no result, false when it has nothing to say. A second stop passes
// with a note, so a child that will not file is never held open.
func judgeStopUnfiled(ctx context.Context, deps Dependencies, raw string, who hookAttribution, at location, lease string) (types.GuardJudgment, bool) {
	setting, ok := deps.setting(string(denyRuleStopUnfiled))
	if !ok || setting.Decision == builtin.Off || lease == "" || !slices.Contains(stopEvents, who.Event) {
		return types.GuardJudgment{}, false
	}
	env, ok := decodeStop(raw)
	if !ok || (who.Event != "SubagentStop" && env.AgentID == "") {
		return types.GuardJudgment{}, false
	}
	row, found := unfiledRow(ctx, at, lease)
	if !found {
		return types.GuardJudgment{}, false
	}
	if env.StopHookActive != nil && *env.StopHookActive {
		return types.GuardJudgment{
			APIVersion: agent.GuardAPIVersion,
			Decision:   decisionAdvise,
			Rule:       string(denyRuleStopUnfiled),
			Context: fmt.Sprintf("magus workspace: job %s stopped a second time with no filed result, and stop-unfiled let it through; "+
				"`magus job wait %s` shows the row never passed.", row.ID, row.ID),
		}, true
	}
	decision := decisionDeny
	if setting.Decision == builtin.Advise {
		decision = decisionAdvise
	}
	v := types.GuardJudgment{APIVersion: agent.GuardAPIVersion, Decision: decision, Rule: string(denyRuleStopUnfiled)}
	say := unfiledStopReason(row)
	if decision == decisionDeny {
		v.Reason = say
	} else {
		v.Context = say
	}
	return v, true
}

// unfiledRow is the live row lease names when it carries no result.
func unfiledRow(ctx context.Context, at location, lease string) (types.Job, bool) {
	rows, err := leaseRows(ctx, at)
	if err != nil {
		return types.Job{}, false
	}
	i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == lease })
	if i < 0 || rows[i].Result != nil || !rows[i].State.Live() {
		return types.Job{}, false
	}
	return rows[i], true
}

// unfiledStopReason names the two commands that file the result, first line
// the one a host shows the person.
func unfiledStopReason(row types.Job) string {
	changed := "each file you changed"
	if row.ReadOnly {
		changed = `nothing: a read-only row files "changed_paths": []`
	}
	return fmt.Sprintf("magus workspace: job %s has no filed result, so this stop is refused once [%s]\n"+
		"File it: `magus job exit %s --stdin`, its changed_paths naming %s; `magus job exit --schema` prints the shape.",
		row.ID, denyRuleStopUnfiled, row.ID, changed)
}
