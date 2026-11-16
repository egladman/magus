package doctor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/safetyscissors/budget"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/check"
)

// The machine entry 94 was refused on: 18.0 GiB across 10 slots.
const entry94BudgetMB, entry94Slots = 18 * 1024, 10

// recordedCurve is a curve as eight measured runs record it: early buckets low, the
// peak late.
func recordedCurve(peakMB uint32) budget.Profile {
	p := budget.Profile{D10Ms: 240_000, D90Ms: 300_000, Runs: 8}
	for i := range p.Curve {
		p.Curve[i] = peakMB / 4
		if i >= budget.CurveBuckets/2 {
			p.Curve[i] = peakMB
		}
	}
	return p
}

func TestGateFitsFailsOnAMemberOverTheWholeBudget(t *testing.T) {
	t.Parallel()
	member := budget.Claim{
		Project: ".", Target: "test", DeclaredBy: "test", Under: ".:ci", MemoryMB: 19_354, Slots: 1,
		Sizing: budget.Sizing{Samples: 8, Fallback: "test: no memory curve for this shape (2 of 3 runs); claimed the 18.9 GiB peak"},
	}
	got := gradeGateFits([]budget.Claim{member}, entry94BudgetMB, entry94Slots)
	assert.Equal(t, check.Fail, got.Status)
	assert.Contains(t, got.Message, "1 step(s) of the ci gate claim more than this machine's whole build budget of 18.0 GiB across 10 slots")
	assert.Contains(t, got.Message, "magus affected ci")
	require.Len(t, got.Details, 1)
	assert.Contains(t, got.Details[0], "test claims 18.9 GiB")
	assert.Contains(t, got.Details[0], "running under .:ci")
}

func TestGateFitsPassesWhenEachStepClaimsItsOwn(t *testing.T) {
	t.Parallel()
	claims := []budget.Claim{
		{Project: ".", Target: "ci", DeclaredBy: "format", MemoryMB: 500, Slots: 1},
		{Project: ".", Target: "lint", MemoryMB: 3000, Slots: 4, Profile: recordedCurve(3000), Sizing: budget.Sizing{Samples: 8}},
		{Project: ".", Target: "test", MemoryMB: 10_240, Slots: 8, Profile: recordedCurve(10_240), Sizing: budget.Sizing{Samples: 8}},
		{Project: ".", Target: "security", MemoryMB: 3615, Slots: 2, Profile: recordedCurve(3615), Sizing: budget.Sizing{Samples: 8}},
	}
	got := gradeGateFits(claims, entry94BudgetMB, entry94Slots)
	assert.Equal(t, check.OK, got.Status, "16.9 GiB summed is over budget, but no single step is")
}

func TestGateFitsFailsOnASlotClaimWiderThanTheMachine(t *testing.T) {
	t.Parallel()
	got := gradeGateFits([]budget.Claim{{Project: ".", Target: "test", MemoryMB: 1000, Slots: 12}}, entry94BudgetMB, entry94Slots)
	assert.Equal(t, check.Fail, got.Status)
	assert.Contains(t, got.Details[0], "test claims 1000 MiB (declared) and 12 slot(s)")
}

func TestGateFitsAsksTheSizerForEachProjectsCi(t *testing.T) {
	t.Parallel()
	p := &types.Project{
		Path: ".",
		TargetChains: map[string][]types.ChainStep{
			"ci":   types.Needs("format", "libs/x:format").Needs("lint", "test"),
			"test": types.Needs("generate"),
		},
	}
	var asked []types.Target
	sizer := func(_ context.Context, targets []types.Target) ([]budget.Claim, error) {
		asked = targets
		return []budget.Claim{{Project: ".", Target: "test", MemoryMB: 19_000}}, nil
	}
	r := &runner{}

	got := r.checkGateFits(WithGateClaims(t.Context(), sizer, entry94BudgetMB, entry94Slots), []*types.Project{p, {Path: "libs/x"}})
	assert.Equal(t, check.Fail, got.Status)
	assert.Equal(t, []types.Target{{Path: ".", Name: "ci"}}, asked,
		"the sizer walks ci's members itself; libs/x composes no ci")

	skipped := (&runner{}).checkGateFits(t.Context(), []*types.Project{p})
	assert.Equal(t, check.Check{
		Name: gateFitsCheck, Status: check.OK, Evidence: check.EvidenceUnknown,
		Message: "no admission sizer supplied; skipped",
	}, skipped, "no sizer is not a pass")
}
