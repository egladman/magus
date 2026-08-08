package magus

import (
	"context"

	"github.com/egladman/magus/internal/hostmem"
)

// hostTotalBytes is the machine's memory, read once. buildStep consults it per
// target, and the answer cannot change while the process runs.
func (m *Magus) hostTotalBytes() int64 {
	m.hostMemOnce.Do(func() { m.hostMemBytes = hostmem.TotalBytes(context.Background()) })
	return m.hostMemBytes
}

// slotsForPolicy resolves a target's declared policy to the number of concurrency
// slots it holds.
//
// Slots and MemoryMB are two spellings of one claim, so both land on the existing
// limiter and there is a single admission path. See types.Target.MemoryMB for why
// memory is the one an author can state. The larger spelling wins when both are
// given: they describe the same resource, and the safe reading of a disagreement
// is the more conservative one.
//
// Pure, with hostTotalBytes passed in. It reads /proc on Linux and forks sysctl on
// darwin, and buildStep calls this per target, so a describe over a large
// workspace would otherwise pay that per target for a machine constant.
//
// Returns slots unchanged when memory is undeclared, when the host is
// unmeasurable, or when the budget is unlimited.
func slotsForPolicy(slots, memoryMB, slotBudget int, hostTotalBytes int64) int {
	if memoryMB <= 0 || slotBudget <= 0 || hostTotalBytes <= 0 {
		return slots
	}
	perSlotMB := hostTotalBytes / int64(slotBudget) / (1 << 20)
	if perSlotMB <= 0 {
		return slots
	}
	// Round up: rounding down would admit a peer against memory this target has
	// already claimed.
	need := int((int64(memoryMB) + perSlotMB - 1) / perSlotMB)
	if need > slots {
		return need
	}
	return slots
}
