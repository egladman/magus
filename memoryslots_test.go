package magus

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// gbTest is the host total these tests pin. slotsForPolicy takes it as an
// argument, so nothing here depends on the machine running the suite - the same
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
