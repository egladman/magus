package doctor

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/types"
)

// judgeableRetries is how many recorded retries a target needs before its declaration is
// graded. A retry fires only when the volatility runtime predicts one is worth trying, so
// they are rare; one or two that failed to rescue anything is a bad week, not a verdict.
const judgeableRetries = 3

// checkRetryDeclarations reports a target whose retry_on_volatile declaration has been
// spending a second run without ever turning a failure into a pass.
//
// Third of the family with memory-declarations and timeout-declarations, and the same
// question: a declaration is a claim about the target, and magus has the measurements to
// say whether it still holds. Here the claim is "this target fails for reasons a rerun
// clears", which retry_on_volatile makes the author write a reason for.
//
// This is the consumer forecast.Outcome.Attempts never had. Attempts is 1 or 2 and is 2
// only when a retry fired, so on its own it is nearly redundant with Result: a Volatile
// outcome already implies a retry that succeeded. The one thing Result cannot say is
// which failures were retried and lost anyway, and that is precisely the evidence that a
// declaration is costing double runtime for nothing.
//
// Advice, never a failure. A retry that has not paid off yet may still be correct about
// the target, and doctor does not know the flake magus has not seen.
func (r *runner) checkRetryDeclarations(projects []*types.Project) types.DoctorCheck {
	const name = "retry-declarations"

	var h forecast.History
	if err := h.Load(r.runCtx(), r.opts.cfg.HistoryPath); err != nil {
		// Advice rather than OK: the check could not RUN, which is a different answer
		// from "every declaration still holds".
		return types.DoctorCheck{
			Name: name, Status: types.DoctorAdvice,
			Message: "run history is unreadable, so retry_on_volatile declarations could not be checked against what magus recorded",
			Details: []string{err.Error()},
		}
	}

	var declared int
	var details []string
	for _, p := range projects {
		label := types.ProjectDisplayName(p.Path, p.Name, p.Dir)
		retries := measuredRetries(&h, p.Path)
		for target, pol := range p.TargetPolicies {
			if !pol.RetryOnVolatile {
				continue
			}
			declared++
			if d := gradeRetries(label, target, pol.RetryOnVolatileReason, retries[target]); d != "" {
				details = append(details, d)
			}
		}
	}

	if declared == 0 {
		return types.DoctorCheck{
			Name: name, Status: types.DoctorOK,
			Message: "no target declares retry_on_volatile, so no failure is retried",
		}
	}
	if len(details) == 0 {
		return types.DoctorCheck{
			Name: name, Status: types.DoctorOK,
			Message: fmt.Sprintf("%d declared retry policy(s) still rescue the failures they retry", declared),
		}
	}
	slices.Sort(details)
	return types.DoctorCheck{
		Name:   name,
		Status: types.DoctorAdvice,
		Message: fmt.Sprintf(
			"%d of %d declared retry policy(s) have not rescued a run; a retry that never turns a failure into a pass buys a second run and no verdict",
			len(details), declared),
		Details: details,
	}
}

// retryTally is one target's recorded retries: how many outcomes ran twice, and how many
// of those the second run rescued.
type retryTally struct {
	retried int
	rescued int
}

// measuredRetries counts retries per target name from the recorded outcomes.
//
// Keyed by bare target name, like measuredLongestRuns, because history keys carry the
// spell prefix (`go/test`) and a magusfile declares policy under the target alone.
func measuredRetries(h *forecast.History, project string) map[string]retryTally {
	targets, ok := h.Projects[project]
	if !ok {
		return nil
	}
	out := map[string]retryTally{}
	for key, st := range targets {
		name := key
		if i := strings.LastIndex(key, "/"); i >= 0 {
			name = key[i+1:]
		}
		t := out[name]
		for _, o := range st.RecentOutcomes {
			if o.Attempts < 2 {
				continue
			}
			t.retried++
			// Volatile is recorded only when the retry passed, so it is the rescue.
			if o.Result == forecast.OutcomeVolatile {
				t.rescued++
			}
		}
		out[name] = t
	}
	return out
}

// gradeRetries renders the finding for one declared policy, or "" while it still looks
// right. A target that has never been retried is silence: the declaration has cost
// nothing and proved nothing.
//
// reason is the magusfile's own prose, echoed back because the reader is being asked
// whether a claim they wrote still holds.
func gradeRetries(project, target, reason string, t retryTally) string {
	if t.retried < judgeableRetries || t.rescued > 0 {
		return ""
	}
	msg := fmt.Sprintf("%s %s retried %d recorded failure(s) and rescued none, so every retry so far has cost a second run and changed no verdict",
		project, target, t.retried)
	if reason != "" {
		msg += fmt.Sprintf("; it declares retry_on_volatile because %q", reason)
	}
	return msg
}
