package doctor

import (
	"context"
	"fmt"
	"time"

	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/check"
)

type admissionsKey struct{}

// WithAdmissions returns ctx carrying the reader of the broker's admissions
// store for the budget-admissions check. The store lives with the broker, so
// the CLI hands it in. A Run whose ctx carries none reports it skipped.
func WithAdmissions(ctx context.Context, load func(ctx context.Context) ([]types.MachineAdmission, error)) context.Context {
	return context.WithValue(ctx, admissionsKey{}, load)
}

const budgetAdmissionsCheck = "budget-admissions"

// admissionsWindow is how far back the check counts refusals.
const admissionsWindow = 24 * time.Hour

// checkBudgetAdmissions counts refusals in the last day and seated waits past
// a quarter of their bound. It changes nothing and never fails.
func (r *runner) checkBudgetAdmissions(ctx context.Context) check.Check {
	load, ok := ctx.Value(admissionsKey{}).(func(context.Context) ([]types.MachineAdmission, error))
	if !ok {
		return check.Check{Name: budgetAdmissionsCheck, Status: check.OK, Evidence: check.EvidenceUnknown, Message: "no admissions reader supplied; skipped"}
	}
	admissions, err := load(ctx)
	if err != nil {
		return check.Check{Name: budgetAdmissionsCheck, Status: check.Advice, Evidence: check.EvidenceUnknown, Message: fmt.Sprintf("could not read the broker's admissions: %v", err)}
	}
	return gradeBudgetAdmissions(admissions, time.Now())
}

// gradeBudgetAdmissions counts refusals within admissionsWindow of now, and
// waits past a quarter of their bound.
func gradeBudgetAdmissions(admissions []types.MachineAdmission, now time.Time) check.Check {
	var refused, slow int
	for _, a := range admissions {
		if now.Sub(a.At) > admissionsWindow {
			continue
		}
		switch a.Outcome {
		case types.AdmissionRefused:
			refused++
		case types.AdmissionSeated:
			if a.WaitBoundMS > 0 && a.WaitedMS*4 > a.WaitBoundMS {
				slow++
			}
		}
	}
	return check.Check{
		Name:   budgetAdmissionsCheck,
		Status: check.OK,
		Message: fmt.Sprintf("%d refusal(s) in the last %s, %d wait(s) past a quarter of their bound",
			refused, admissionsWindow, slow),
	}
}
