package doctor

import (
	"fmt"
	"slices"

	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// recurringGuardDenialLimit bounds the activity window this reads. Doctor runs often and
// the trail is append-only, so an unbounded walk would grow without limit; a recurring
// pattern shows up well inside a window this size, and one that does not is not recurring.
const recurringGuardDenialLimit = 500

// checkRecurringGuardDenials reports guard denials an agent hit repeatedly, so the friction
// a workspace keeps causing is visible where every other workspace verdict already is.
//
// ADVICE, never a failure. A denial is the guard doing its job, and a recurring one may
// still be correct: the same wrong instinct repeated is a reader's habit, not a workspace
// defect. What is reportable is that it KEEPS happening, because that is the part no single
// session can see. This states the evidence only; choosing a destination is a human's job,
// and the magus-workspace-rules skill carries the method for choosing it.
func (r *runner) checkRecurringGuardDenials() types.DoctorCheck {
	const name = "recurring-guard-denials"
	feedback, err := trail.RecentGuardFeedback(r.cacheDir(), "", recurringGuardDenialLimit)
	if err != nil {
		return types.DoctorCheck{
			Name:     name,
			Status:   types.DoctorOK,
			Evidence: types.EvidenceUnknown,
			Message:  "no readable activity trail, so nothing can be said about recurring denials",
		}
	}
	recurring := make([]trail.GuardFeedback, 0, len(feedback))
	for _, item := range feedback {
		if item.NeedsReview() {
			recurring = append(recurring, item)
		}
	}
	if len(recurring) == 0 {
		return types.DoctorCheck{Name: name, Status: types.DoctorOK, Message: "no guard denial has recurred across sessions"}
	}
	details := make([]string, 0, len(recurring))
	for _, c := range recurring {
		details = append(details, fmt.Sprintf("%s on %s: %d denial(s) across %d session(s), followed %d/%d",
			c.Rule, c.Surface, c.Denied, c.Sessions, c.FollowedSessions, c.Sessions))
	}
	slices.Sort(details)
	return types.DoctorCheck{
		Name:     name,
		Status:   types.DoctorAdvice,
		Evidence: types.EvidenceInferred,
		Message: fmt.Sprintf("%d guard rule(s) denied the same shape more than once; the friction is real whether the rule is right or the reader is",
			len(recurring)),
		Details: details,
	}
}
