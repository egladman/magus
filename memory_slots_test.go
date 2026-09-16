package magus

import (
	"testing"

	"github.com/stretchr/testify/assert"

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

	mb, from := (&Magus{}).chainMemoryMB(p, "ci")
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
	mb, _ := (&Magus{}).chainMemoryMB(together, "ci")
	assert.Equal(t, 14336, mb, "ctx.needs(build, test) runs both at once")

	inTurn := chainProject(".", policies,
		map[string][]types.ChainStep{"ci": types.Needs("build").Needs("test")})
	mb, _ = (&Magus{}).chainMemoryMB(inTurn, "ci")
	assert.Equal(t, 10240, mb, "ctx.needs(build); ctx.needs(test) runs them in turn")
}

func TestChainMemoryFollowsNestedChains(t *testing.T) {
	p := chainProject(".",
		map[string]types.Target{"test": {MemoryMB: 8192}},
		map[string][]types.ChainStep{
			"ci":     types.Needs("verify"),
			"verify": types.Needs("test"),
		})

	mb, from := (&Magus{}).chainMemoryMB(p, "ci")
	assert.Equal(t, 8192, mb)
	assert.Equal(t, "test", from)
}

// A target's own declaration wins over a lighter chain, and an undeclared target
// with an undeclared chain still claims nothing.
func TestChainMemoryKeepsTheTargetsOwnFigure(t *testing.T) {
	p := chainProject(".",
		map[string]types.Target{"ci": {MemoryMB: 6144}, "test": {MemoryMB: 1024}},
		map[string][]types.ChainStep{"ci": types.Needs("test")})

	mb, from := (&Magus{}).chainMemoryMB(p, "ci")
	assert.Equal(t, 6144, mb)
	assert.Equal(t, "ci", from)

	bare := chainProject(".", nil, map[string][]types.ChainStep{"ci": types.Needs("lint")})
	mb, from = (&Magus{}).chainMemoryMB(bare, "ci")
	assert.Equal(t, 0, mb)
	assert.Empty(t, from)
}

// A chain that loops back must terminate rather than recurse forever. Cycles are
// rejected elsewhere, so this only has to not hang.
func TestChainMemoryTerminatesOnACycle(t *testing.T) {
	p := chainProject(".",
		map[string]types.Target{"a": {MemoryMB: 2048}},
		map[string][]types.ChainStep{"a": types.Needs("b"), "b": types.Needs("a")})

	mb, _ := (&Magus{}).chainMemoryMB(p, "a")
	assert.Equal(t, 2048, mb)
}
