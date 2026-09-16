package doctor

import (
	"fmt"
	"slices"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/types"
)

// recurringGuardDenialLimit bounds the activity window this reads. Doctor runs often and
// the trail is append-only, so an unbounded walk would grow without limit; a recurring
// pattern shows up well inside a window this size, and one that does not is not recurring.
const recurringGuardDenialLimit = 500

// checkRecurringGuardDenials reports guard denials an agent hit repeatedly, so the friction
// a workspace keeps causing is visible where every other workspace verdict already is.
//
// It replaced `magus agent improve`, which was a verb for a read-only report: it took the
// same evidence, ranked it, and ended in "a human decides", which is what doctor IS. Its
// only other half, --apply, duplicated `magus agent harness apply` call for call.
//
// ADVICE, never a failure. A denial is the guard doing its job, and a recurring one may
// still be correct: the same wrong instinct repeated is a reader's habit, not a workspace
// defect. What is reportable is that it KEEPS happening, because that is the part no single
// session can see. The destination is a human's to choose, and the magus-workspace-rules
// skill carries the method for choosing it.
func (r *runner) checkRecurringGuardDenials() types.DoctorCheck {
	const name = "recurring-guard-denials"
	report, err := agent.Improve(r.ctx, agent.ImproveOptions{
		Root:           r.root,
		CacheDir:       r.cacheDir(),
		Limit:          recurringGuardDenialLimit,
		WiredHarnesses: workspaceHarnesses(r.ws),
	})
	if err != nil {
		return types.DoctorCheck{
			Name:     name,
			Status:   types.DoctorOK,
			Evidence: types.EvidenceUnknown,
			Message:  "no readable activity trail, so nothing can be said about recurring denials",
		}
	}
	if len(report.Candidates) == 0 {
		return types.DoctorCheck{Name: name, Status: types.DoctorOK, Message: "no guard denial has recurred across sessions"}
	}
	details := make([]string, 0, len(report.Candidates))
	for _, c := range report.Candidates {
		details = append(details, fmt.Sprintf("%s on %s: %d denial(s) across %d session(s); %s",
			c.Rule, c.Surface, c.Denied, c.Sessions, c.Next))
	}
	slices.Sort(details)
	return types.DoctorCheck{
		Name:     name,
		Status:   types.DoctorAdvice,
		Evidence: types.EvidenceInferred,
		Message: fmt.Sprintf("%d guard rule(s) denied the same shape more than once; the friction is real whether the rule is right or the reader is",
			len(report.Candidates)),
		Details: details,
	}
}
