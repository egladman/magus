package magus

import (
	"context"
	"math"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/ci/forecast"
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

// claimMemory folds a target's ctx.needs chain into the figure both halves of
// admission read, sets it and the slots it is worth on step, and returns how the
// figure was sized. See types.SizedChainMemory; this supplies the workspace's
// cross-project lookup.
//
// The claim is held for the whole step, including the phases that do not need it,
// which is conservative in the direction the machine survives: the alternative is
// admitting the chain and refusing it twenty minutes in, when the work already done
// is wasted.
//
// size is nil to claim the declarations as written, or memorySizer's answer to claim
// what this shape of run has measured.
func (m *Magus) claimMemory(step *cache.Step, p *types.Project, target string, size types.MemorySizer) types.MemorySizing {
	c := types.SizedChainMemory(p, target, m.Get, size)
	step.MemoryMB, step.MemoryDeclaredBy = c.MB, c.DeclaredBy
	step.Slots = slotsForPolicy(p.TargetPolicies[target].Slots, step.MemoryMB, m.limiter().Capacity(), m.hostUsableBytes())
	return c.Sizing
}

const (
	// peakMargin is the headroom a measured claim carries over the highest peak seen.
	// A recorded peak is a floor, sampled every 200ms (see internal/proc/run), and
	// 1.25 is the same ratio doctor's MGS1030 treats as a material under-declaration,
	// so a claim sized here is never one that check would flag.
	peakMargin = 1.25
	// minPeakRuns is how many successful measured runs a claim needs before it
	// replaces the declaration: the floor forecast already sets before trusting a
	// duration percentile. One or two runs may both be narrow or warm-cached ones; a
	// third that agrees makes the maximum worth sizing a machine against.
	minPeakRuns = 3
)

// memorySizer sizes each declaration from the measured peaks of runs shaped like this
// one, and nil when there is no history to read. See sizeMemoryClaim.
func memorySizer(peaks *forecast.PeakIndex, shape forecast.Shape) types.MemorySizer {
	if peaks == nil {
		return nil
	}
	return func(proj *types.Project, target string, declaredMB int) (int, types.MemorySizing) {
		same, all := peaks.Peak(proj.Path, target, shape)
		return sizeMemoryClaim(declaredMB, same, all)
	}
}

// sizeMemoryClaim is min(declaredMB, ceil(peakMargin x the measured maximum)), taking
// the maximum over runs of the same shape, else over every run of the target, else
// claiming the declaration. The declaration stays the ceiling: it is a human's
// statement of the worst case, and a measurement only ever narrows it.
func sizeMemoryClaim(declaredMB int, sameShape, anyShape forecast.MeasuredPeak) (int, types.MemorySizing) {
	peak, sizing := sameShape, types.MemorySizing{Samples: sameShape.Runs}
	if sameShape.Runs < minPeakRuns {
		peak, sizing = anyShape, types.MemorySizing{Samples: anyShape.Runs, AnyShape: true}
	}
	if peak.Runs < minPeakRuns {
		return declaredMB, types.MemorySizing{}
	}
	mb := int(math.Ceil(float64(peak.MaxBytes) * peakMargin / (1 << 20)))
	if mb >= declaredMB {
		return declaredMB, types.MemorySizing{}
	}
	return mb, sizing
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
