package doctor

import (
	"context"
	"fmt"
	"slices"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/check"
)

type gateClaimsKey struct{}

type gateSizing struct {
	claims      func(ctx context.Context, targets []types.Target) ([]types.MachineClaim, error)
	budgetMB    int
	budgetSlots int
}

// WithGateClaims returns ctx carrying the admission sizer and this machine's whole
// build budget for the gate-fits check. claims sizes targets' machine claims the way
// admission does, from the measured runs of a ci shape; it lives with the run path,
// outside this package, so the CLI hands it in. A Run whose ctx carries none reports
// that check skipped.
func WithGateClaims(ctx context.Context, claims func(ctx context.Context, targets []types.Target) ([]types.MachineClaim, error), budgetMB, budgetSlots int) context.Context {
	return context.WithValue(ctx, gateClaimsKey{}, gateSizing{claims: claims, budgetMB: budgetMB, budgetSlots: budgetSlots})
}

const gateFitsCheck = "gate-fits"

// checkGateFits is MGS3009 ahead of the run: a ci step whose claim exceeds this
// machine's whole budget is refused every time, so the gate can never pass here.
func (r *runner) checkGateFits(ctx context.Context, projects []*types.Project) check.Check {
	sizing, ok := ctx.Value(gateClaimsKey{}).(gateSizing)
	if !ok {
		return check.Check{Name: gateFitsCheck, Status: check.OK, Evidence: check.EvidenceUnknown, Message: "no admission sizer supplied; skipped"}
	}
	var targets []types.Target
	for _, p := range projects {
		if _, ok := p.TargetChains[types.TargetCI]; ok {
			// The sizer claims every member ci reaches, each weighed alone.
			targets = append(targets, types.Target{Path: p.Path, Name: types.TargetCI})
		}
	}
	if len(targets) == 0 {
		return check.Check{Name: gateFitsCheck, Status: check.OK, Evidence: check.EvidenceUnknown, Message: "no project composes a ci target; skipped"}
	}
	claims, err := sizing.claims(ctx, targets)
	if err != nil {
		return check.Check{Name: gateFitsCheck, Status: check.Fail, Message: fmt.Sprintf("could not size the ci gate's claims: %v", err)}
	}
	return gradeGateFits(claims, sizing.budgetMB, sizing.budgetSlots)
}

// gradeGateFits fails on every claim larger than the whole budget on either axis. A
// non-positive budget is an axis this host could not measure, which bounds nothing.
func gradeGateFits(claims []types.MachineClaim, budgetMB, budgetSlots int) check.Check {
	var details []string
	for _, c := range claims {
		memory := budgetMB > 0 && c.MemoryMB > budgetMB
		slots := budgetSlots > 0 && max(c.Slots, 1) > budgetSlots
		if !memory && !slots {
			continue
		}
		what := fmt.Sprintf("%s %s claims %s (%s) and %d slot(s)",
			types.ProjectDisplayName(c.Project, "", ""), c.Target, types.FormatMB(c.MemoryMB), c.Sizing, max(c.Slots, 1))
		if c.Under != "" {
			what += ", running under " + c.Under
		}
		details = append(details, what)
	}
	if len(details) == 0 {
		return check.Check{
			Name: gateFitsCheck, Status: check.OK,
			Message: fmt.Sprintf("every step of the ci gate fits this machine's build budget of %s across %d slots",
				types.FormatMB(budgetMB), budgetSlots),
		}
	}
	slices.Sort(details)
	return check.Check{
		Name:   gateFitsCheck,
		Status: check.Fail,
		Message: fmt.Sprintf(
			"%d step(s) of the ci gate claim more than this machine's whole build budget of %s across %d slots, so `%s` is refused here every time (see %s). Lower the claim: correct a drifted memory_mb (the memory-declarations check compares it to what magus measured), split the target, or run the gate on a bigger machine",
			len(details), types.FormatMB(budgetMB), budgetSlots, hint.Affected.With("ci"), types.CodeURL(types.MachineBudgetExhausted)),
		Details: details,
	}
}
