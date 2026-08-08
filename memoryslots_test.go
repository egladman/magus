package magus

import (
	"testing"

	"github.com/egladman/magus/internal/hostmem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The cases that must behave exactly as they did before memory was declarable.
// This is the whole safety argument for converting in buildStep rather than only
// where someone opted in: an undeclared target, an unmeasurable host, and an
// unlimited budget all keep the caller's own Slots untouched.
func TestSlotsForPolicyIsInertWithoutADeclaration(t *testing.T) {
	assert.Equal(t, 0, slotsForPolicy(0, 0, 4), "nothing declared")
	assert.Equal(t, 3, slotsForPolicy(3, 0, 4), "slots declared, memory not")
	assert.Equal(t, 2, slotsForPolicy(2, 8192, 0), "unlimited budget has nothing to throttle against")
	assert.Equal(t, 2, slotsForPolicy(2, -1, 4), "a negative declaration is not a claim")
}

// The real conversion, expressed against the host this test runs on. A target
// asking for one slot's worth of memory takes one slot; asking for two shares
// takes two. Written in terms of the measured share rather than a fixed number so
// it asserts the arithmetic instead of the machine.
func TestSlotsForPolicyConvertsMemoryToShares(t *testing.T) {
	total := hostmem.Total()
	if total <= 0 {
		t.Skip("host memory is not measurable here; the conversion is inert by design")
	}
	const budget = 4
	perSlotMB := int(total / budget / (1 << 20))
	require.Positive(t, perSlotMB, "a measurable host must yield a positive per-slot share")

	assert.Equal(t, 1, slotsForPolicy(0, perSlotMB, budget), "exactly one share")
	assert.Equal(t, 2, slotsForPolicy(0, perSlotMB*2, budget), "exactly two shares")
	assert.Equal(t, 3, slotsForPolicy(0, perSlotMB*2+1, budget),
		"a share and a byte must round UP: rounding down admits a peer against memory already claimed")
}

// Both spellings given: the larger wins, because they are claims about the same
// resource and the safe reading of a disagreement is the conservative one.
func TestSlotsForPolicyTakesTheLargerClaim(t *testing.T) {
	total := hostmem.Total()
	if total <= 0 {
		t.Skip("host memory is not measurable here")
	}
	const budget = 4
	perSlotMB := int(total / budget / (1 << 20))

	assert.Equal(t, 3, slotsForPolicy(3, perSlotMB, budget), "slots=3 beats memory worth 1")
	assert.Equal(t, 3, slotsForPolicy(1, perSlotMB*3, budget), "memory worth 3 beats slots=1")
}

// A declaration larger than the whole machine resolves to more slots than exist.
// That is correct and deliberately NOT clamped here: the limiter already clamps to
// its capacity, and clamping twice would hide a target that cannot fit at all.
func TestSlotsForPolicyDoesNotClampAnImpossibleClaim(t *testing.T) {
	total := hostmem.Total()
	if total <= 0 {
		t.Skip("host memory is not measurable here")
	}
	const budget = 4
	huge := int(total/(1<<20)) * 10
	assert.Greater(t, slotsForPolicy(0, huge, budget), budget,
		"a claim bigger than the host must not silently look satisfiable")
}
