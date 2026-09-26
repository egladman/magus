package magus

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/types"
)

// gbTest is the host total these tests pin. slotsForPolicy takes it as an
// argument, so nothing here depends on the machine running the suite: the same
// trap the forecaster tests had to be fixed for.
const gbTest = int64(1) << 30

// perSlot is what one slot is worth at the pinned total and budget: 16GB over 4
// slots is 4096MB.
const (
	testBudget = 4
	testTotal  = 16 * gbTest
	perSlotMB  = 4096
)

// The cases that must behave exactly as they did before memory was declarable.
// This is the whole safety argument for converting in buildStep rather than only
// where someone opted in: an undeclared target, an unmeasurable host, and an
// unlimited budget all keep the caller's own Slots untouched.
func TestSlotsForPolicyIsInertWithoutADeclaration(t *testing.T) {
	assert.Equal(t, 0, slotsForPolicy(0, 0, testBudget, testTotal), "nothing declared")
	assert.Equal(t, 3, slotsForPolicy(3, 0, testBudget, testTotal), "slots declared, memory not")
	assert.Equal(t, 2, slotsForPolicy(2, 8192, 0, testTotal), "unlimited budget has nothing to throttle against")
	assert.Equal(t, 2, slotsForPolicy(2, -1, testBudget, testTotal), "a negative declaration is not a claim")
	assert.Equal(t, 2, slotsForPolicy(2, 8192, testBudget, 0), "an unmeasurable host is not a budget")
}

// The conversion itself, against a pinned host.
func TestSlotsForPolicyConvertsMemoryToShares(t *testing.T) {
	assert.Equal(t, 1, slotsForPolicy(0, perSlotMB, testBudget, testTotal), "exactly one share")
	assert.Equal(t, 2, slotsForPolicy(0, perSlotMB*2, testBudget, testTotal), "exactly two shares")
	assert.Equal(t, 3, slotsForPolicy(0, perSlotMB*2+1, testBudget, testTotal),
		"a share and a byte must round UP: rounding down admits a peer against memory already claimed")
	assert.Equal(t, 1, slotsForPolicy(0, 1, testBudget, testTotal), "a small claim is still one slot")
}

// Both spellings given: the larger wins, because they are claims about the same
// resource and the safe reading of a disagreement is the conservative one.
func TestSlotsForPolicyTakesTheLargerClaim(t *testing.T) {
	assert.Equal(t, 3, slotsForPolicy(3, perSlotMB, testBudget, testTotal), "slots=3 beats memory worth 1")
	assert.Equal(t, 3, slotsForPolicy(1, perSlotMB*3, testBudget, testTotal), "memory worth 3 beats slots=1")
}

// A declaration larger than the whole machine resolves to more slots than exist.
// Deliberately not clamped here: the limiter already clamps to its capacity, and
// clamping twice would hide a target that cannot fit at all.
func TestSlotsForPolicyDoesNotClampAnImpossibleClaim(t *testing.T) {
	assert.Greater(t, slotsForPolicy(0, 160_000, testBudget, testTotal), testBudget,
		"a claim bigger than the host must not silently look satisfiable")
}

// chainProject builds a project whose targets carry policies and ctx.needs chains.
func chainProject(path string, policies map[string]types.Target, chains map[string][]types.ChainStep) *types.Project {
	return &types.Project{Path: path, TargetPolicies: policies, TargetChains: chains}
}

// The defect this fold exists for: `magus affected ci` is the invocation that fills
// a machine, and only `ci` is scheduled as a step. Without the fold the heaviest
// target in the workspace runs as if it had declared nothing.
func TestChainMemoryInheritsFromAComposedTarget(t *testing.T) {
	p := chainProject(".",
		map[string]types.Target{"test": {MemoryMB: 10240}},
		map[string][]types.ChainStep{"ci": types.Needs("lint", "test")})

	mb, from := declaredClaim(p, "ci")
	assert.Equal(t, 10240, mb)
	assert.Equal(t, "test", from, "the refusal must name the target that wrote the figure")
}

// One ctx.needs call runs its members together, so their declarations add; a second
// call runs after the first, so the peak is the larger call. Summing across calls would
// refuse work that fits, and taking the maximum within one would admit work that does
// not.
func TestChainMemorySumsOneCallAndPeaksAcrossCalls(t *testing.T) {
	policies := map[string]types.Target{"test": {MemoryMB: 10240}, "build": {MemoryMB: 4096}}
	together := chainProject(".", policies,
		map[string][]types.ChainStep{"ci": types.Needs("build", "test")})
	mb, _ := declaredClaim(together, "ci")
	assert.Equal(t, 14336, mb, "ctx.needs(build, test) runs both at once")

	inTurn := chainProject(".", policies,
		map[string][]types.ChainStep{"ci": types.Needs("build").Needs("test")})
	mb, _ = declaredClaim(inTurn, "ci")
	assert.Equal(t, 10240, mb, "ctx.needs(build); ctx.needs(test) runs them in turn")
}

func TestChainMemoryFollowsNestedChains(t *testing.T) {
	p := chainProject(".",
		map[string]types.Target{"test": {MemoryMB: 8192}},
		map[string][]types.ChainStep{
			"ci":     types.Needs("verify"),
			"verify": types.Needs("test"),
		})

	mb, from := declaredClaim(p, "ci")
	assert.Equal(t, 8192, mb)
	assert.Equal(t, "test", from)
}

// A target's own declaration wins over a lighter chain, and an undeclared target
// with an undeclared chain still claims nothing.
func TestChainMemoryKeepsTheTargetsOwnFigure(t *testing.T) {
	p := chainProject(".",
		map[string]types.Target{"ci": {MemoryMB: 6144}, "test": {MemoryMB: 1024}},
		map[string][]types.ChainStep{"ci": types.Needs("test")})

	mb, from := declaredClaim(p, "ci")
	assert.Equal(t, 6144, mb)
	assert.Equal(t, "ci", from)

	bare := chainProject(".", nil, map[string][]types.ChainStep{"ci": types.Needs("lint")})
	mb, from = declaredClaim(bare, "ci")
	assert.Equal(t, 0, mb)
	assert.Empty(t, from)
}

// A chain that loops back must terminate rather than recurse forever. Cycles are
// rejected elsewhere, so this only has to not hang.
func TestChainMemoryTerminatesOnACycle(t *testing.T) {
	p := chainProject(".",
		map[string]types.Target{"a": {MemoryMB: 2048}},
		map[string][]types.ChainStep{"a": types.Needs("b"), "b": types.Needs("a")})

	mb, _ := declaredClaim(p, "a")
	assert.Equal(t, 2048, mb)
}

// declaredClaim is the claim buildStep gives a step before a run sizes it.
func declaredClaim(p *types.Project, target string) (int, string) {
	var step cache.Step
	(&Magus{}).claimMemory(&step, p, target, nil)
	return step.MemoryMB, step.MemoryDeclaredBy
}

const mib = int64(1) << 20

func TestSizeMemoryClaim(t *testing.T) {
	cases := []struct {
		name       string
		declaredMB int
		same, all  forecast.MeasuredPeak
		wantMB     int
		wantSizing types.MemorySizing
	}{
		{
			name:       "measured runs of this shape undercut the declaration",
			declaredMB: 10240,
			same:       forecast.MeasuredPeak{MaxBytes: 4703 * mib, Runs: 54},
			all:        forecast.MeasuredPeak{MaxBytes: 5000 * mib, Runs: 80},
			wantMB:     5879, // ceil(1.25 x 4703)
			wantSizing: types.MemorySizing{Samples: 54},
		},
		{
			name:       "the declaration caps a measurement above it",
			declaredMB: 4096,
			same:       forecast.MeasuredPeak{MaxBytes: 4000 * mib, Runs: 10},
			wantMB:     4096,
		},
		{
			name:       "a shape never run falls back to every run of the target",
			declaredMB: 10240,
			all:        forecast.MeasuredPeak{MaxBytes: 4000 * mib, Runs: 10},
			wantMB:     5000,
			wantSizing: types.MemorySizing{Samples: 10, AnyShape: true},
		},
		{
			name:       "a shape with too few runs falls back too, even to a larger peak",
			declaredMB: 10240,
			same:       forecast.MeasuredPeak{MaxBytes: 100 * mib, Runs: minPeakRuns - 1},
			all:        forecast.MeasuredPeak{MaxBytes: 4000 * mib, Runs: 10},
			wantMB:     5000,
			wantSizing: types.MemorySizing{Samples: 10, AnyShape: true},
		},
		{
			name:       "too few runs of any shape keeps the declaration",
			declaredMB: 10240,
			same:       forecast.MeasuredPeak{MaxBytes: 100 * mib, Runs: 1},
			all:        forecast.MeasuredPeak{MaxBytes: 100 * mib, Runs: minPeakRuns - 1},
			wantMB:     10240,
		},
		{
			name:       "no history keeps the declaration",
			declaredMB: 10240,
			wantMB:     10240,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mb, sizing := sizeMemoryClaim(tc.declaredMB, tc.same, tc.all)
			assert.Equal(t, tc.wantMB, mb)
			assert.Equal(t, tc.wantSizing, sizing)
		})
	}
}

// The whole path a run takes: recorded outcomes, the snapshot, and the chain fold. `ci`
// declares nothing and composes `test`, so the step claims test's SIZED figure, and the
// failed run that peaked higher is not part of it.
func TestClaimMemoryFoldsSizedFigures(t *testing.T) {
	shape := forecast.NewShape([]string{"rw"}, nil)
	outcome := func(result forecast.OutcomeResult, mb int64) forecast.Outcome {
		return forecast.Outcome{Result: result, MaxRSSBytes: mb * mib, Charms: shape.Charms, ArgsHash: shape.ArgsHash}
	}
	h := forecast.History{Projects: map[string]map[string]forecast.Stats{
		".": {"magusfile/test": {RecentOutcomes: []forecast.Outcome{
			outcome(forecast.OutcomePass, 3000),
			outcome(forecast.OutcomeVolatile, 4000),
			outcome(forecast.OutcomePass, 3500),
			outcome(forecast.OutcomeFail, 9000),
		}}},
	}}
	x := h.PeakIndex()
	p := chainProject(".",
		map[string]types.Target{"test": {MemoryMB: 10240}, "lint": {MemoryMB: 1024}},
		map[string][]types.ChainStep{"ci": types.Needs("lint", "test")})

	var step cache.Step
	sizing := (&Magus{}).claimMemory(&step, p, "ci", memorySizer(&x, shape))
	assert.Equal(t, 5000+1024, step.MemoryMB, "test sized to 1.25 x 4000; lint has no runs, so its declaration stands")
	assert.Equal(t, "test", step.MemoryDeclaredBy)
	assert.Equal(t, types.MemorySizing{Samples: 3}, sizing)

	assert.Nil(t, memorySizer(nil, shape), "no history, no sizer: declarations fold as written")
}

// An undeclared target claims slots only, measured or not.
func TestClaimMemoryNeverSizesAnUndeclaredTarget(t *testing.T) {
	p := chainProject(".", nil, map[string][]types.ChainStep{"ci": types.Needs("test")})
	called := false
	size := func(*types.Project, string, int) (int, types.MemorySizing) {
		called = true
		return 1, types.MemorySizing{Samples: 99}
	}
	var step cache.Step
	sizing := (&Magus{}).claimMemory(&step, p, "ci", size)
	assert.False(t, called)
	assert.Equal(t, 0, step.MemoryMB)
	assert.Equal(t, types.MemorySizing{}, sizing)
}
