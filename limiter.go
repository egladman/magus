package magus

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/sys/mem"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
)

// Limiter is a weighted semaphore that caps concurrent spell executions.
// Obtain one with [NewLimiter] and share it across server workspaces via [WithLimiter].
type Limiter struct{ lim *cache.Limiter }

// NewLimiter creates a Limiter with capacity n. n ≤ 0 defaults to
// [DefaultConcurrency].
func NewLimiter(n int) *Limiter {
	if n <= 0 {
		n = cache.DefaultConcurrency()
	}
	return &Limiter{lim: cache.NewLimiter(n)}
}

// DefaultConcurrency returns the balanced profile's width: the concurrency cap used
// when no explicit cap is set, resolved by precedence: the MAGUS_CONCURRENCY env var
// if set to a positive int, then min(NumCPU, 8).
func DefaultConcurrency() int { return cache.DefaultConcurrency() }

// Capacity returns the configured concurrency cap.
func (l *Limiter) Capacity() int { return l.lim.Capacity() }

// WithLimiter injects a pre-built Limiter (e.g. shared across server workspaces).
// When omitted, Open constructs a private limiter from magus.yaml/Concurrency.
func WithLimiter(l *Limiter) Option {
	return func(o *workspace.Load) { o.Limiter = l.lim }
}

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
// is memory this build can never have, and both readers below size real work
// against it.
func (m *Magus) hostUsableBytes() int64 {
	m.hostMemOnce.Do(func() { m.hostMemBytes = mem.UsableBytes(context.Background()) })
	return m.hostMemBytes
}

// sizeClaim sets on step what it claims from both halves of admission: its memory, the
// target that figure came from and how it was sized, and its slots. It returns the
// memory's sizing.
//
// A declaration always wins. Memory folds the target's ctx.needs chain (see
// types.SizedChainMemory, supplied the workspace's cross-project lookup), each
// declaration narrowed by what its runs measured; only a chain that declares nothing
// claims the scheduled target's own measured peak. Slots are the target's declaration,
// else its measured width, else one, raised to what the memory is worth on this host
// (see slotsForPolicy).
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
	slots := p.TargetPolicies[target].Slots
	if size != nil {
		if c.MB == 0 {
			if mb, sizing := size.memory(p, target, 0); mb > 0 {
				c = types.ChainMemory{MB: mb, DeclaredBy: target, Sizing: sizing}
			}
		}
		if slots == 0 {
			slots = size.slots(p, target, m.limiter().Capacity())
		}
	}
	step.MemoryMB, step.MemoryDeclaredBy, step.MemorySizing = c.MB, c.DeclaredBy, c.Sizing
	step.Slots = slotsForPolicy(slots, step.MemoryMB, m.limiter().Capacity(), m.hostUsableBytes())
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
// args an invocation runs every target with.
type claimSizer struct {
	runs  *forecast.MeasuredRuns
	shape forecast.Shape
}

// newClaimSizer is nil when there is no history to read, which leaves every claim at
// its declaration.
func newClaimSizer(runs *forecast.MeasuredRuns, shape forecast.Shape) *claimSizer {
	if runs == nil {
		return nil
	}
	return &claimSizer{runs: runs, shape: shape}
}

// memory sizes target's memory claim; declaredMB is 0 when it declares none. See
// sizeMemoryClaim.
func (s *claimSizer) memory(proj *types.Project, target string, declaredMB int) (int, types.MemorySizing) {
	same, all := s.runs.Peak(proj.Path, target, s.shape)
	return sizeMemoryClaim(declaredMB, same, all)
}

// slots sizes the slot claim of a target that declares none. See sizeSlotClaim.
func (s *claimSizer) slots(proj *types.Project, target string, capacity int) int {
	same, all := s.runs.Width(proj.Path, target, s.shape)
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
func sizeMemoryClaim(declaredMB int, sameShape, anyShape forecast.Measurement) (int, types.MemorySizing) {
	peak, sizing, ok := trustedMeasurement(sameShape, anyShape)
	if !ok {
		return declaredMB, types.MemorySizing{}
	}
	mb := int(math.Ceil(float64(peak.Value) * peakMargin / (1 << 20)))
	if declaredMB > 0 && mb >= declaredMB {
		return declaredMB, types.MemorySizing{}
	}
	return mb, sizing
}

// sizeSlotClaim is the measured width (forecast.MeasuredRuns.Width), capped at capacity
// when the budget is limited, or 0, which takes one slot, when too few runs measured
// it.
//
// The width a run measures is bounded by the claim it ran under, since a step runs the
// Go toolchain only as wide as its slots (spawn.SeatJobserver). So a Go-heavy target
// sized here never measures its way wider; declaring `slots` is what widens it.
func sizeSlotClaim(sameShape, anyShape forecast.Measurement, capacity int) int {
	width, _, ok := trustedMeasurement(sameShape, anyShape)
	if !ok {
		return 0
	}
	if capacity > 0 {
		return min(int(width.Value), capacity)
	}
	return int(width.Value)
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
