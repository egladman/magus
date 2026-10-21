package magus

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/sys/mem"
	"github.com/egladman/magus/types"
)

// slotWaitFloor is the least slot wait worth a nudge. It is the cadence at which magus
// already treats a wait as worth telling the reader about (the machine budget and the
// project lock both heartbeat at 15s); below it the win from a wider pool is inside the
// variance of an ordinary run.
const slotWaitFloor = 15 * time.Second

// slotPressure is what a run knows, once it ends, about whether its width held it back.
type slotPressure struct {
	waited              time.Duration
	width               int
	aggressiveWidth     int
	explicitConcurrency bool
	profile             types.ConcurrencyProfile
}

// nudge returns the one line suggesting the aggressive profile, or "" when the run
// has no case for it.
func (p slotPressure) nudge() string {
	switch {
	case p.explicitConcurrency, p.profile == types.ProfileAggressive:
		// The user chose a width; second-guessing that choice is noise.
		return ""
	case p.aggressiveWidth <= p.width, p.waited < slotWaitFloor:
		return ""
	}
	return fmt.Sprintf("this run waited %s for slots; concurrency_profile: aggressive would have given it %d",
		p.waited.Round(time.Second), p.aggressiveWidth)
}

// ConcurrencyNudge returns a one-line suggestion to raise concurrency_profile when this
// Magus's targets queued for slots while cores the aggressive profile would use sat
// idle, and "" otherwise. The caller decides whether and how often to print it.
//
// Silent when concurrency was set explicitly and when the profile is already
// aggressive. A caller whose limiter spans other invocations' runs (the server's) does
// not ask: a wait there is not this invocation's profile.
func (m *Magus) ConcurrencyNudge() string {
	if m.cache == nil {
		return ""
	}
	aggressiveWidth, _ := cache.ClampConcurrency(cache.ProfileConcurrency(types.ProfileAggressive))
	return slotPressure{
		waited:              m.slotWaits.Waited(),
		width:               m.limiter().Capacity(),
		aggressiveWidth:     aggressiveWidth,
		explicitConcurrency: m.cfg.Concurrency > 0,
		profile:             m.cfg.ConcurrencyProfile,
	}.nudge()
}

// hostUsableBytes is the memory this process may commit, read once. buildStep
// consults it per target, and the answer cannot change while the process runs.
//
// Usable rather than total: in a memory-limited container the machine's own figure
// is memory this build can never have, and sizeClaim sizes real work against it.
func (m *Magus) hostUsableBytes() int64 {
	m.hostMemOnce.Do(func() { m.hostMemBytes = mem.UsableBytes(context.Background()) })
	return m.hostMemBytes
}

// sizeClaim sets on step what it claims from both halves of admission: its memory, the
// target that figure came from, its slots, and how each was sized. It returns that
// sizing.
//
// A declaration always wins. Memory folds the target's ctx.needs chain (see
// types.SizedChainMemory, supplied the workspace's cross-project lookup), each
// declaration narrowed by what its runs measured and each member declaring nothing
// sized from its measured peak. Slots are the target's declaration,
// else its measured width, else one. With no machine broker they are raised to what
// the memory is worth in the local pool; a broker holds memory in megabytes, so under
// it they stay CPU (see slotsForPolicy).
//
// The claim is held for the whole step, including the phases that do not need it,
// which is conservative in the direction the machine survives: the alternative is
// admitting the chain and refusing it twenty minutes in, when the work already done
// is wasted.
//
// size is nil to claim the declarations as written, or newClaimSizer's answer to claim
// what this shape of run has measured.
func (m *Magus) sizeClaim(step *cache.Step, p *types.Project, target string, size *claimSizer) types.MemorySizing {
	var sizeDeclared types.MemorySizer
	if size != nil {
		sizeDeclared = size.memory
	}
	c := types.SizedChainMemory(p, target, m.Get, sizeDeclared)
	slots, slotSizing := p.TargetPolicies[target].Slots, types.SlotSizing{Source: types.SlotsDeclared}
	if slots == 0 {
		slotSizing = types.SlotSizing{Source: types.SlotsUnmeasured}
		if size != nil {
			slots, slotSizing = size.slots(p, target, m.limiter().Capacity())
		}
	}
	budgetMB := mem.BudgetMB(m.hostUsableBytes(), m.cfg.ConcurrencyProfile)
	step.Slots = slotsForPolicy(slots, c.MB, m.limiter().Capacity(), budgetMB, m.cache.MachineArbitrated())
	if step.Slots > max(slots, 1) {
		slotSizing = types.SlotSizing{Source: types.SlotsForMemory}
	}
	c.Sizing.Slots = slotSizing
	step.MemoryMB, step.MemoryDeclaredBy, step.MemorySizing = c.MB, c.DeclaredBy, c.Sizing
	return c.Sizing
}

const (
	// peakMargin is the headroom a measured claim carries over the highest peak seen.
	// A recorded peak is a floor, sampled every 200ms (see internal/proc/spawn), and
	// 1.25 is the same ratio doctor's MGS1030 treats as a material under-declaration,
	// so a claim sized here is never one that check would flag.
	peakMargin = 1.25
	// minMeasuredRuns is how many successful measured runs a claim needs before it is
	// sized from them: the floor forecast already sets before trusting a duration
	// percentile. One or two runs may both be narrow or warm-cached ones; a third that
	// agrees makes the figure worth sizing a machine against.
	minMeasuredRuns = 3
)

// claimSizer sizes claims from the measured runs of one shape: the charms and forwarded
// args an invocation runs every target with. reads says which of those charms a target's
// chain reads; each target is sized with only those, so a charm it never reads does not
// split its runs.
type claimSizer struct {
	runs  *forecast.MeasuredRuns
	shape forecast.Shape
	reads func(p *types.Project, target, charm string) bool
}

// newClaimSizer is nil when there is no history to read, which leaves every claim at
// its declaration.
func newClaimSizer(runs *forecast.MeasuredRuns, shape forecast.Shape, reads func(p *types.Project, target, charm string) bool) *claimSizer {
	if runs == nil {
		return nil
	}
	return &claimSizer{runs: runs, shape: shape, reads: reads}
}

// shapeFor is the sizer's shape keeping the charms target's chain reads. The predicate
// also filters every recorded run's charms, so each answer is walked once.
func (s *claimSizer) shapeFor(proj *types.Project, target string) forecast.Shape {
	read := map[string]bool{}
	return s.shape.Reading(func(charm string) bool {
		r, ok := read[charm]
		if !ok {
			r = s.reads(proj, target, charm)
			read[charm] = r
		}
		return r
	})
}

// memory sizes target's memory claim; declaredMB is 0 when it declares none. See
// sizeMemoryClaim.
func (s *claimSizer) memory(proj *types.Project, target string, declaredMB int) (int, types.MemorySizing) {
	shape := s.shapeFor(proj, target)
	same, all := s.runs.Peak(proj.Path, target, shape)
	var full forecast.Measurement
	if shape.Narrowed {
		full, _, _ = trustedMeasurement(s.runs.Peak(proj.Path, target, shape.Full()))
	}
	return sizeMemoryClaim(declaredMB, same, all, full)
}

// slots sizes the slot claim of a target that declares none. See sizeSlotClaim.
func (s *claimSizer) slots(proj *types.Project, target string, capacity int) (int, types.SlotSizing) {
	same, all := s.runs.Width(proj.Path, target, s.shapeFor(proj, target))
	return sizeSlotClaim(same, all, capacity)
}

// trustedMeasurement picks what a claim is sized from: the runs of this shape, else
// every run of the target, and neither when fewer than minMeasuredRuns measured it.
func trustedMeasurement(sameShape, anyShape forecast.Measurement) (forecast.Measurement, types.MemorySizing, bool) {
	switch {
	case sameShape.Runs >= minMeasuredRuns:
		return sameShape, types.MemorySizing{Samples: sameShape.Runs}, true
	case anyShape.Runs >= minMeasuredRuns:
		return anyShape, types.MemorySizing{Samples: anyShape.Runs, AnyShape: true}, true
	}
	return forecast.Measurement{}, types.MemorySizing{}, false
}

// sizeMemoryClaim is ceil(peakMargin x the measured peak) in MiB, capped at declaredMB
// when the target declares one (declaredMB > 0). Too few measured runs claim the
// declaration, which is 0 for a target that declares none. The declaration stays the
// ceiling: it is a human's statement of the worst case, and a measurement only ever
// narrows it.
//
// fullPeak is the trusted peak of full runs when the shape is narrowed, and zero
// otherwise. Narrowed runs pool across args only to lower a claim, so a narrowed shape's
// own peak is capped by it; a narrowed shape sized from every run already includes it.
func sizeMemoryClaim(declaredMB int, sameShape, anyShape, fullPeak forecast.Measurement) (int, types.MemorySizing) {
	peak, sizing, ok := trustedMeasurement(sameShape, anyShape)
	if !ok {
		return declaredMB, types.MemorySizing{}
	}
	if !sizing.AnyShape && fullPeak.Runs > 0 {
		peak.Value = min(peak.Value, fullPeak.Value)
	}
	mb := int(math.Ceil(float64(peak.Value) * peakMargin / (1 << 20)))
	if declaredMB > 0 && mb >= declaredMB {
		return declaredMB, types.MemorySizing{}
	}
	return mb, sizing
}

// sizeSlotClaim is the measured width (forecast.MeasuredRuns.Width), capped at capacity
// when the budget is limited, or 0, which takes one slot, when too few runs measured
// it. A run whose claim capped its width doubles the claim, so a Go-heavy target that
// runs only as wide as its slots (spawn.SeatJobserver) still measures its way wider.
func sizeSlotClaim(sameShape, anyShape forecast.Measurement, capacity int) (int, types.SlotSizing) {
	width, measured, ok := trustedMeasurement(sameShape, anyShape)
	if !ok {
		return 0, types.SlotSizing{Source: types.SlotsUnmeasured}
	}
	sizing := types.SlotSizing{Source: types.SlotsMeasured, Samples: measured.Samples, AnyShape: measured.AnyShape}
	if capacity > 0 {
		return min(int(width.Value), capacity), sizing
	}
	return int(width.Value), sizing
}

// slotsForPolicy resolves a target's declared policy to the number of concurrency
// slots it holds.
//
// With no broker the local pool is the only arbiter, so memory is converted at
// budgetMB (mem.BudgetMB) over the pool's width per slot and the larger spelling wins:
// they describe the same resource, and the safe reading of a disagreement is the more
// conservative one. See types.Target.MemoryMB for why memory is usually the one known.
//
// A brokered claim keeps slots as given: the broker holds megabytes and slots apart,
// and converting would count the memory twice in two units.
//
// Returns slots unchanged when memory is undeclared, when the host is
// unmeasurable (budgetMB 0), or when the slot budget is unlimited.
func slotsForPolicy(slots, memoryMB, slotBudget, budgetMB int, brokered bool) int {
	if brokered || memoryMB <= 0 || slotBudget <= 0 || budgetMB <= 0 {
		return slots
	}
	perSlotMB := budgetMB / slotBudget
	if perSlotMB <= 0 {
		return slots
	}
	// Round up: rounding down would admit a peer against memory this target has
	// already claimed.
	need := (memoryMB + perSlotMB - 1) / perSlotMB
	if need > slots {
		return need
	}
	return slots
}
