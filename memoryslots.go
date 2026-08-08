package magus

import "github.com/egladman/magus/internal/hostmem"

// slotsForPolicy resolves a target's declared policy to the number of concurrency
// slots it holds.
//
// Slots and MemoryMB are two spellings of one claim, so both land on the existing
// limiter and there is a single admission path. See types.Target.MemoryMB for why
// memory is the one an author can state. The larger spelling wins when both are
// given.
//
// Returns slots unchanged when memory is undeclared, the host is unmeasurable, or
// the budget is unlimited.
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
	// Round up: rounding down would admit a peer against memory this target has
	// already claimed.
	need := (int64(memoryMB) + perSlotMB - 1) / perSlotMB
	if need < 1 {
		need = 1
	}
	if int(need) > slots {
		return int(need)
	}
	return slots
}
