package guard

import (
	"context"
	"fmt"
	"slices"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/job"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
)

// denyRuleStopUnfiled names the refusal of a leased subagent's stop while its
// job row carries no filed result.
const denyRuleStopUnfiled denyRuleName = "stop-unfiled"

// judgeStopUnfiled refuses a leased subagent's first stop while the row it
// holds has no result, false when it has nothing to say. A second stop passes
// with a note, so a child that will not file is never held open.
func judgeStopUnfiled(ctx context.Context, deps Dependencies, raw string, who hookAttribution, at location, lease string) (verdict.Judgment, bool) {
	setting, ok := deps.setting(string(denyRuleStopUnfiled))
	if !ok || setting.Decision == builtin.Off || lease == "" || !slices.Contains(stopEvents, who.Event) {
		return verdict.Judgment{}, false
	}
	env, ok := decodeStop(raw)
	if !ok || (who.Event != "SubagentStop" && env.AgentID == "") {
		return verdict.Judgment{}, false
	}
	row, found := unfiledRow(ctx, at, lease)
	if !found {
		return verdict.Judgment{}, false
	}
	if env.StopHookActive != nil && *env.StopHookActive {
		return verdict.Judgment{
			APIVersion: agent.GuardAPIVersion,
			Decision:   verdict.Advise,
			Rule:       string(denyRuleStopUnfiled),
			Context: fmt.Sprintf("magus workspace: job %s stopped a second time with no filed result, and stop-unfiled let it through; "+
				"`magus job wait %s` shows the row never passed.", row.ID, row.ID),
		}, true
	}
	decision := verdict.Deny
	if setting.Decision == builtin.Advise {
		decision = verdict.Advise
	}
	v := verdict.Judgment{APIVersion: agent.GuardAPIVersion, Decision: decision, Rule: string(denyRuleStopUnfiled)}
	say := unfiledStopReason(row)
	if decision == verdict.Deny {
		v.Reason = say
	} else {
		v.Context = say
	}
	return v, true
}

// unfiledRow is the live row lease names when its live attempt filed no result
// and did not end by handback: a delivered SubagentHandback call is its report.
func unfiledRow(ctx context.Context, at location, lease string) (jobtypes.Job, bool) {
	rows, err := leaseRows(ctx, at)
	if err != nil {
		return jobtypes.Job{}, false
	}
	tries, err := leaseAttempts(ctx, at)
	if err != nil {
		return jobtypes.Job{}, false
	}
	i := slices.IndexFunc(rows, func(r jobtypes.Job) bool { return r.ID == lease })
	live := tries.Live(lease)
	if i < 0 || live.Status.Result != nil || live.Status.End == jobtypes.AttemptEndHandback || !rows[i].State.Live() {
		return jobtypes.Job{}, false
	}
	return rows[i], true
}

// leaseAttempts are the pinned job store's tries, read when nothing
// pinned one, as [leaseRows] reads the rows.
func leaseAttempts(ctx context.Context, at location) (jobtypes.Attempts, error) {
	if pinned, ok := job.SnapshotFromContext(ctx); ok {
		return jobtypes.NewAttempts(pinned.Attempts), pinned.Err
	}
	all, err := job.NewStore(at.jobs()).Attempts()
	return jobtypes.NewAttempts(all), err
}

// unfiledStopReason names the two commands that file the result, first line
// the one a host shows the person.
func unfiledStopReason(row jobtypes.Job) string {
	changed := "each file you changed"
	if row.ReadOnly {
		changed = `nothing: a read-only row files "changed_paths": []`
	}
	return fmt.Sprintf("magus workspace: job %s filed no result, stop refused once [%s]\n"+
		"Run `magus job exit %s --stdin` naming %s; shape: magus job exit --schema",
		row.ID, denyRuleStopUnfiled, row.ID, changed)
}
