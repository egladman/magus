package guard

import (
	"context"
	"fmt"
	"slices"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/job"
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

// unfiledRow is the live row lease names when its live attempt filed no result.
func unfiledRow(ctx context.Context, at location, lease string) (types.Job, bool) {
	rows, err := leaseRows(ctx, at)
	if err != nil {
		return types.Job{}, false
	}
	tries, err := leaseAttempts(ctx, at)
	if err != nil {
		return types.Job{}, false
	}
	i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == lease })
	if i < 0 || tries.Result(lease) != nil || !rows[i].State.Live() {
		return types.Job{}, false
	}
	return rows[i], true
}

// leaseAttempts are the pinned job store's tries, read when nothing
// pinned one, as [leaseRows] reads the rows.
func leaseAttempts(ctx context.Context, at location) (types.JobAttempts, error) {
	if pinned, ok := job.SnapshotFromContext(ctx); ok {
		return types.NewJobAttempts(pinned.Attempts), pinned.Err
	}
	all, err := job.NewStore(at.jobs()).Attempts()
	return types.NewJobAttempts(all), err
}

// unfiledStopReason names the two commands that file the result, first line
// the one a host shows the person.
func unfiledStopReason(row types.Job) string {
	changed := "each file you changed"
	if row.ReadOnly {
		changed = `nothing: a read-only row files "changed_paths": []`
	}
	return fmt.Sprintf("magus workspace: job %s filed no result, stop refused once [%s]\n"+
		"Run `magus job exit %s --stdin` naming %s; shape: magus job exit --schema",
		row.ID, denyRuleStopUnfiled, row.ID, changed)
}
