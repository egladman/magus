package magus

import (
	"context"

	"github.com/egladman/magus/internal/sys/mem"
	"github.com/egladman/magus/types"
)

// hostUsableBytes is the memory this process may commit, read once. buildStep
// consults it per target, and the answer cannot change while the process runs.
//
// Usable rather than total: in a memory-limited container the machine's own figure
// is memory this build can never have, and both readers below size real work
// against it.
func (m *Magus) hostUsableBytes() int64 {
	m.hostMemOnce.Do(func() { m.hostMemBytes = mem.UsableBytes(context.Background()) })
	return m.hostMemBytes
}

// chainMemoryMB folds a target's ctx.needs chain into the figure both halves of
// admission read. See types.ChainMemoryMB; this only supplies the workspace's
// cross-project lookup.
//
// The claim is held for the whole step, including the phases that do not need it,
// which is conservative in the direction the machine survives: the alternative is
// admitting the chain and refusing it twenty minutes in, when the work already done
// is wasted.
func (m *Magus) chainMemoryMB(p *types.Project, target string) (mb int, declaredBy string) {
	return types.ChainMemoryMB(p, target, m.Get)
}

// slotsForPolicy resolves a target's declared policy to the number of concurrency
// slots it holds.
//
// Slots and MemoryMB are two spellings of one claim, so both land on the limiter and a
// target's author states whichever they know. See types.Target.MemoryMB for why memory
// is usually that one. The larger spelling wins when both are given: they describe the
// same resource, and the safe reading of a disagreement is the more conservative one.
//
// The MACHINE budget reads the megabytes directly rather than this conversion, which
// divides by a per-process budget and so cannot be inverted.
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
