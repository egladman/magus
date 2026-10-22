package doctor

import (
	"context"
	"fmt"
	"slices"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
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
func (r *runner) checkGateFits(projects []*types.Project) types.Check {
	sizing, ok := r.runCtx().Value(gateClaimsKey{}).(gateSizing)
	if !ok {
		return types.Check{Name: gateFitsCheck, Status: types.CheckOK, Evidence: types.EvidenceUnknown, Message: "no admission sizer supplied; skipped"}
	}
	var targets []types.Target
	for _, p := range projects {
		if _, ok := p.TargetChains[types.TargetCI]; ok {
			targets = append(targets, gateSteps(p)...)
		}
	}
	if len(targets) == 0 {
		return types.Check{Name: gateFitsCheck, Status: types.CheckOK, Evidence: types.EvidenceUnknown, Message: "no project composes a ci target; skipped"}
	}
	claims, err := sizing.claims(r.runCtx(), targets)
	if err != nil {
		return types.Check{Name: gateFitsCheck, Status: types.CheckFail, Message: fmt.Sprintf("could not size the ci gate's claims: %v", err)}
	}
	return gradeGateFits(claims, sizing.budgetMB, sizing.budgetSlots)
}

// gateSteps is p's ci and every same-project target its chain reaches. Each is weighed
// alone: a skip_cache composer's member claims for itself, and a cacheable composer's
// is inside that composer's figure. Cross-project members run inline under ci's claim.
func gateSteps(p *types.Project) []types.Target {
	seen := map[string]bool{}
	var out []types.Target
	var walk func(name string)
	walk = func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		out = append(out, types.Target{Path: p.Path, Name: name})
		for _, step := range p.TargetChains[name] {
			if step.Project == "" {
				walk(step.Target)
			}
		}
	}
	walk(types.TargetCI)
	return out
}

// gradeGateFits fails on every claim larger than the whole budget on either axis. A
// non-positive budget is an axis this host could not measure, which bounds nothing.
func gradeGateFits(claims []types.MachineClaim, budgetMB, budgetSlots int) types.Check {
	var details []string
	for _, c := range claims {
		memory := budgetMB > 0 && c.MemoryMB > budgetMB
		slots := budgetSlots > 0 && max(c.Slots, 1) > budgetSlots
		if !memory && !slots {
			continue
		}
		what := fmt.Sprintf("%s %s claims %s (%s) and %d slot(s)",
			types.ProjectDisplayName(c.Project, "", ""), c.Target, cache.FormatMB(c.MemoryMB), c.Sizing, max(c.Slots, 1))
		if c.DeclaredBy != "" && c.DeclaredBy != c.Target {
			what += ", most of it from " + c.DeclaredBy
		}
		details = append(details, what)
	}
	if len(details) == 0 {
		return types.Check{
			Name: gateFitsCheck, Status: types.CheckOK,
			Message: fmt.Sprintf("every step of the ci gate fits this machine's build budget of %s across %d slots",
				cache.FormatMB(budgetMB), budgetSlots),
		}
	}
	slices.Sort(details)
	return types.Check{
		Name:   gateFitsCheck,
		Status: types.CheckFail,
		Message: fmt.Sprintf(
			"%d step(s) of the ci gate claim more than this machine's whole build budget of %s across %d slots, so `%s` is refused here every time (see %s). Lower the claim: correct a drifted memory_mb (the memory-declarations check compares it to what magus measured), split the target, or run the gate on a bigger machine",
			len(details), cache.FormatMB(budgetMB), budgetSlots, hint.Affected.With("ci"), types.CodeURL(types.MachineBudgetExhausted)),
		Details: details,
	}
}
