package magus

import "github.com/egladman/magus/internal/hostmem"

// slotsForPolicy resolves a target's declared execution policy to the number of
// concurrency slots it should hold.
//
// Two spellings, one budget. Slots is the direct form; MemoryMB is the portable
// one, because an author knows a race-enabled test suite wants 8GB but cannot know
// how many slots that is on a machine they have never seen - the answer differs
// between a 16GB CI runner and a 64GB workstation. Converting here means the
// existing limiter enforces both, so there is a single admission path rather than
// two budgets that can disagree about whether a target may start.
//
// The share is total memory divided by the slot budget: with 16GB and 4 slots each
// slot is worth 4GB, so a target declaring 8GB holds 2 and only two such targets
// can run together. The larger of the two spellings wins when both are given -
// they are claims about the same thing, and the safe reading of a disagreement is
// the more conservative one.
//
// Returns 0 ("no opinion, take one slot") when memory is undeclared, when the host
// cannot be measured, or when the budget is unlimited. Each of those is the
// behaviour that existed before memory was declarable, which is what makes this
// safe to apply everywhere rather than only where it was asked for.
func slotsForPolicy(slots, memoryMB, slotBudget int) int {
	if memoryMB <= 0 || slotBudget <= 0 {
		return slots
	}
	total := hostmem.Total()
	if total <= 0 {
		return slots
	}
	perSlotMB := total / int64(slotBudget) / (1 << 20)
	if perSlotMB <= 0 {
		return slots
	}
	// Round UP: a target needing 5GB where a slot is worth 4GB holds 2, not 1.
	// Rounding down would admit a peer against memory this target has already
	// claimed, which is the exact over-subscription the declaration exists to stop.
	need := (int64(memoryMB) + perSlotMB - 1) / perSlotMB
	if need < 1 {
		need = 1
	}
	if int(need) > slots {
		return int(need)
	}
	return slots
}
