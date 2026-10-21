package magus

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/broker"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/sys/mem"
	"github.com/egladman/magus/types"
)

// TestSlotPressureNudge pins every reason the nudge stays silent, beside the one case
// where it speaks.
func TestSlotPressureNudge(t *testing.T) {
	pressured := slotPressure{
		waited: 41*time.Second + 300*time.Millisecond, width: 8, aggressiveWidth: 16,
		profile: types.ProfileBalanced,
	}
	with := func(edit func(*slotPressure)) slotPressure {
		p := pressured
		edit(&p)
		return p
	}
	got := map[string]string{
		"pressured":        pressured.nudge(),
		"explicit":         with(func(p *slotPressure) { p.explicitConcurrency = true }).nudge(),
		"aggressive":       with(func(p *slotPressure) { p.profile = types.ProfileAggressive }).nudge(),
		"no idle cores":    with(func(p *slotPressure) { p.aggressiveWidth = 8 }).nudge(),
		"under the floor":  with(func(p *slotPressure) { p.waited = slotWaitFloor - time.Millisecond }).nudge(),
		"at the floor":     with(func(p *slotPressure) { p.waited = slotWaitFloor }).nudge(),
		"conservative too": with(func(p *slotPressure) { p.profile = types.ProfileConservative; p.width = 4 }).nudge(),
	}
	assert.Equal(t, map[string]string{
		"pressured":        "this run waited 41s for slots; concurrency_profile: aggressive would have given it 16",
		"explicit":         "",
		"aggressive":       "",
		"no idle cores":    "",
		"under the floor":  "",
		"at the floor":     "this run waited 15s for slots; concurrency_profile: aggressive would have given it 16",
		"conservative too": "this run waited 41s for slots; concurrency_profile: aggressive would have given it 16",
	}, got)
}

// The memory budget and slot width these tests pin. slotsForPolicy takes both as
// arguments, so nothing here depends on the machine running the suite: 16384 MiB over
// 4 slots is 4096 MiB a slot.
const (
	testBudget   = 4
	testBudgetMB = 16384
	perSlotMB    = 4096
)

// The cases that must behave exactly as they did before memory was declarable.
// This is the whole safety argument for converting in buildStep rather than only
// where someone opted in: an undeclared target, an unmeasurable host, and an
// unlimited budget all keep the caller's own Slots untouched.
func TestSlotsForPolicyIsInertWithoutADeclaration(t *testing.T) {
	assert.Equal(t, 0, slotsForPolicy(0, 0, testBudget, testBudgetMB, false), "nothing declared")
	assert.Equal(t, 3, slotsForPolicy(3, 0, testBudget, testBudgetMB, false), "slots declared, memory not")
	assert.Equal(t, 2, slotsForPolicy(2, 8192, 0, testBudgetMB, false), "unlimited budget has nothing to throttle against")
	assert.Equal(t, 2, slotsForPolicy(2, -1, testBudget, testBudgetMB, false), "a negative declaration is not a claim")
	assert.Equal(t, 2, slotsForPolicy(2, 8192, testBudget, 0, false), "an unmeasurable host is not a budget")
}

// The broker holds memory in megabytes beside its own slots, so a brokered claim's
// slots are its CPU and never grow with its memory.
func TestSlotsForPolicyLeavesSlotsAloneUnderTheBroker(t *testing.T) {
	assert.Equal(t, 0, slotsForPolicy(0, perSlotMB*3, testBudget, testBudgetMB, true), "memory alone takes one slot")
	assert.Equal(t, 2, slotsForPolicy(2, 160_000, testBudget, testBudgetMB, true), "declared slots stand")
}

// One slot is worth the memory budget over the width, not the host over the width:
// 24 GiB usable is an 18432 MiB balanced budget, 2304 MiB a slot at width 8, where the
// host would make it 3072.
func TestSlotsForPolicyDividesTheBudgetNotTheHost(t *testing.T) {
	budgetMB := mem.BudgetMB(24<<30, types.ProfileBalanced)
	assert.Equal(t, 2, slotsForPolicy(0, 3072, 8, budgetMB, false))
	assert.Equal(t, 1, slotsForPolicy(0, 2304, 8, budgetMB, false))
}

// The conversion itself, against a pinned budget.
func TestSlotsForPolicyConvertsMemoryToShares(t *testing.T) {
	assert.Equal(t, 1, slotsForPolicy(0, perSlotMB, testBudget, testBudgetMB, false), "exactly one share")
	assert.Equal(t, 2, slotsForPolicy(0, perSlotMB*2, testBudget, testBudgetMB, false), "exactly two shares")
	assert.Equal(t, 3, slotsForPolicy(0, perSlotMB*2+1, testBudget, testBudgetMB, false),
		"a share and a megabyte must round UP: rounding down admits a peer against memory already claimed")
	assert.Equal(t, 1, slotsForPolicy(0, 1, testBudget, testBudgetMB, false), "a small claim is still one slot")
}

// Both spellings given: the larger wins, because they are claims about the same
// resource and the safe reading of a disagreement is the conservative one.
func TestSlotsForPolicyTakesTheLargerClaim(t *testing.T) {
	assert.Equal(t, 3, slotsForPolicy(3, perSlotMB, testBudget, testBudgetMB, false), "slots=3 beats memory worth 1")
	assert.Equal(t, 3, slotsForPolicy(1, perSlotMB*3, testBudget, testBudgetMB, false), "memory worth 3 beats slots=1")
}

// A declaration larger than the whole budget resolves to more slots than exist.
// Deliberately not clamped here: the limiter already clamps to its capacity, and
// clamping twice would hide a target that cannot fit at all.
func TestSlotsForPolicyDoesNotClampAnImpossibleClaim(t *testing.T) {
	assert.Greater(t, slotsForPolicy(0, 160_000, testBudget, testBudgetMB, false), testBudget,
		"a claim bigger than the budget must not silently look satisfiable")
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

	mb, from := declaredClaim(p, "ci")
	assert.Equal(t, 10240, mb)
	assert.Equal(t, "test", from, "the refusal must name the target that wrote the figure")
}

// One ctx.needs call runs its members together, and with no curve to fold by their
// declarations add; a second
// call runs after the first, so the peak is the larger call. Summing across calls would
// refuse work that fits, and taking the maximum within one would admit work that does
// not.
func TestChainMemorySumsOneCallAndPeaksAcrossCalls(t *testing.T) {
	policies := map[string]types.Target{"test": {MemoryMB: 10240}, "build": {MemoryMB: 4096}}
	together := chainProject(".", policies,
		map[string][]types.ChainStep{"ci": types.Needs("build", "test")})
	mb, _ := declaredClaim(together, "ci")
	assert.Equal(t, 14336, mb, "ctx.needs(build, test) runs both at once")

	inTurn := chainProject(".", policies,
		map[string][]types.ChainStep{"ci": types.Needs("build").Needs("test")})
	mb, _ = declaredClaim(inTurn, "ci")
	assert.Equal(t, 10240, mb, "ctx.needs(build); ctx.needs(test) runs them in turn")
}

func TestChainMemoryFollowsNestedChains(t *testing.T) {
	p := chainProject(".",
		map[string]types.Target{"test": {MemoryMB: 8192}},
		map[string][]types.ChainStep{
			"ci":     types.Needs("verify"),
			"verify": types.Needs("test"),
		})

	mb, from := declaredClaim(p, "ci")
	assert.Equal(t, 8192, mb)
	assert.Equal(t, "test", from)
}

// A target's own declaration wins over a lighter chain, and an undeclared target
// with an undeclared chain still claims nothing.
func TestChainMemoryKeepsTheTargetsOwnFigure(t *testing.T) {
	p := chainProject(".",
		map[string]types.Target{"ci": {MemoryMB: 6144}, "test": {MemoryMB: 1024}},
		map[string][]types.ChainStep{"ci": types.Needs("test")})

	mb, from := declaredClaim(p, "ci")
	assert.Equal(t, 6144, mb)
	assert.Equal(t, "ci", from)

	bare := chainProject(".", nil, map[string][]types.ChainStep{"ci": types.Needs("lint")})
	mb, from = declaredClaim(bare, "ci")
	assert.Equal(t, 0, mb)
	assert.Empty(t, from)
}

// A chain that loops back must terminate rather than recurse forever. Cycles are
// rejected elsewhere, so this only has to not hang.
func TestChainMemoryTerminatesOnACycle(t *testing.T) {
	p := chainProject(".",
		map[string]types.Target{"a": {MemoryMB: 2048}},
		map[string][]types.ChainStep{"a": types.Needs("b"), "b": types.Needs("a")})

	mb, _ := declaredClaim(p, "a")
	assert.Equal(t, 2048, mb)
}

// declaredClaim is the claim buildStep gives a step before a run sizes it.
func declaredClaim(p *types.Project, target string) (int, string) {
	var step cache.Step
	(&Magus{}).sizeClaim(&step, p, target, nil)
	return step.MemoryMB, step.MemoryDeclaredBy
}

const mib = int64(1) << 20

func TestSizeMemoryClaim(t *testing.T) {
	cases := []struct {
		name       string
		declaredMB int
		same, all  forecast.Measurement
		wantMB     int
		wantSizing types.MemorySizing
	}{
		{
			name:       "measured runs of this shape undercut the declaration",
			declaredMB: 10240,
			same:       forecast.Measurement{Value: 4703 * mib, Runs: 54},
			all:        forecast.Measurement{Value: 5000 * mib, Runs: 80},
			wantMB:     5879, // ceil(1.25 x 4703)
			wantSizing: types.MemorySizing{Samples: 54},
		},
		{
			name:       "the declaration caps a measurement above it",
			declaredMB: 4096,
			same:       forecast.Measurement{Value: 4000 * mib, Runs: 10},
			wantMB:     4096,
		},
		{
			name:       "a shape never run falls back to every run of the target",
			declaredMB: 10240,
			all:        forecast.Measurement{Value: 4000 * mib, Runs: 10},
			wantMB:     5000,
			wantSizing: types.MemorySizing{Samples: 10, AnyShape: true},
		},
		{
			name:       "a shape with too few runs falls back too, even to a larger peak",
			declaredMB: 10240,
			same:       forecast.Measurement{Value: 100 * mib, Runs: minMeasuredRuns - 1},
			all:        forecast.Measurement{Value: 4000 * mib, Runs: 10},
			wantMB:     5000,
			wantSizing: types.MemorySizing{Samples: 10, AnyShape: true},
		},
		{
			name:       "too few runs of any shape keeps the declaration",
			declaredMB: 10240,
			same:       forecast.Measurement{Value: 100 * mib, Runs: 1},
			all:        forecast.Measurement{Value: 100 * mib, Runs: minMeasuredRuns - 1},
			wantMB:     10240,
		},
		{
			name:       "no history keeps the declaration",
			declaredMB: 10240,
			wantMB:     10240,
		},
		{
			name:       "an undeclared target claims its measured peak, with no ceiling",
			same:       forecast.Measurement{Value: 9000 * mib, Runs: minMeasuredRuns},
			wantMB:     11250,
			wantSizing: types.MemorySizing{Samples: minMeasuredRuns},
		},
		{
			name: "an undeclared target with too few runs claims nothing",
			same: forecast.Measurement{Value: 9000 * mib, Runs: minMeasuredRuns - 1},
			all:  forecast.Measurement{Value: 9000 * mib, Runs: minMeasuredRuns - 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			curve := types.MemoryProfile{Runs: minMeasuredRuns}
			mb, sizing, _ := sizeMemoryClaim("test", tc.declaredMB, tc.same, tc.all, forecast.Measurement{}, curve)
			assert.Equal(t, tc.wantMB, mb)
			assert.Equal(t, tc.wantSizing, sizing)
		})
	}
}

// A measured claim whose shape has too few curves is its peak held flat, and says so
// in the words a refusal prints; one with enough carries its curve, every bucket under
// the same margin and declaration ceiling as the peak.
func TestSizeMemoryClaimSaysItFellBackToPeak(t *testing.T) {
	measured := forecast.Measurement{Value: 5788 * mib, Runs: 13}
	short := types.MemoryProfile{Runs: minMeasuredRuns - 1}
	mb, sizing, profile := sizeMemoryClaim("test", 0, measured, measured, forecast.Measurement{}, short)
	assert.Equal(t, 7235, mb, "ceil(1.25 x 5788)")
	assert.Equal(t, types.MemoryProfile{}, profile)
	assert.Equal(t, "test: no memory curve for this shape (2 of 3 runs); claimed the 7.1 GiB peak", sizing.Fallback)
	assert.Equal(t, "measured over 13 runs; test: no memory curve for this shape (2 of 3 runs); claimed the 7.1 GiB peak", sizing.String())

	curve := types.MemoryProfile{D10Ms: 40_000, D90Ms: 60_000, Runs: minMeasuredRuns}
	curve.Curve[0], curve.Curve[19] = 1000, 5000
	mb, sizing, profile = sizeMemoryClaim("test", 6000, measured, measured, forecast.Measurement{}, curve)
	assert.Equal(t, 6000, mb, "the declaration caps the peak")
	assert.Equal(t, types.MemorySizing{}, sizing, "nothing fell back")
	want := curve
	want.Curve[0], want.Curve[19] = 1250, 6000
	assert.Equal(t, want, profile, "1.25 x each bucket, the last capped at the 6000 MiB declaration")
}

// The whole path a run takes: recorded outcomes, the snapshot, and the chain fold. `ci`
// declares nothing and composes `test`, so the step claims test's SIZED figure, and the
// failed run that peaked higher is not part of it.
func TestSizeClaimFoldsSizedFigures(t *testing.T) {
	shape := forecast.NewShape([]string{"rw"}, nil)
	outcome := func(result forecast.OutcomeResult, mb int64) forecast.Outcome {
		return forecast.Outcome{Result: result, MaxRSSBytes: mb * mib, Charms: shape.Charms, ArgsHash: shape.ArgsHash}
	}
	h := forecast.History{Projects: map[string]map[string]forecast.Stats{
		".": {"magusfile/test": {RecentOutcomes: []forecast.Outcome{
			outcome(forecast.OutcomePass, 3000),
			outcome(forecast.OutcomeVolatile, 4000),
			outcome(forecast.OutcomePass, 3500),
			outcome(forecast.OutcomeFail, 9000),
		}}},
	}}
	x := h.MeasuredRuns()
	p := chainProject(".",
		map[string]types.Target{"test": {MemoryMB: 10240}, "lint": {MemoryMB: 1024}},
		map[string][]types.ChainStep{"ci": types.Needs("lint", "test")})

	// A host with memory to spare per slot, so the memory claim adds no slots of its own.
	m := &Magus{}
	m.limOnce.Do(func() { m.lim = cache.NewLimiter(8) })
	m.hostMemOnce.Do(func() { m.hostMemBytes = 1 << 40 })
	var step cache.Step
	sizing := m.sizeClaim(&step, p, "ci", newClaimSizer(&x, shape, m.chainReadsCharm))
	// test is sized to 1.25 x 4000; lint has no runs, so its declaration stands, and
	// with no curve on either the call sums their peaks.
	want := types.MemorySizing{Samples: 3, Slots: types.SlotSizing{Source: types.SlotsUnmeasured},
		Fallback: "lint: no memory curve; summed the peaks of its ctx.needs call"}
	assert.Equal(t, cache.Step{MemoryMB: 5000 + 1024, MemoryDeclaredBy: "test", MemorySizing: want, Slots: 1}, step)
	assert.Equal(t, want, sizing)

	assert.Nil(t, newClaimSizer(nil, shape, m.chainReadsCharm), "no history, no sizer: declarations fold as written")
}

// A target that declares neither memory nor slots claims what its runs measured once it
// has minMeasuredRuns of them, and nothing before; a declaration on either axis wins
// over the measurement on that axis.
func TestSizeClaimMeasuresAnUndeclaredTarget(t *testing.T) {
	shape := forecast.NewShape(nil, nil)
	// 1.6 GiB peaks and three of four claimed cores busy over wall time, the shape of a
	// scip build.
	run := forecast.Outcome{Result: forecast.OutcomePass, MaxRSSBytes: 1600 * mib, CPUMs: 3000, DurationMs: 1000, Slots: 4, Charms: shape.Charms, ArgsHash: shape.ArgsHash}
	history := func(runs int) *forecast.MeasuredRuns {
		h := forecast.History{Projects: map[string]map[string]forecast.Stats{
			".": {"magusfile/scip": {RecentOutcomes: slices.Repeat([]forecast.Outcome{run}, runs)}},
		}}
		x := h.MeasuredRuns()
		return &x
	}
	// A host with memory to spare per slot, so the memory claim adds no slots of its own.
	m := &Magus{}
	m.limOnce.Do(func() { m.lim = cache.NewLimiter(8) })
	m.hostMemOnce.Do(func() { m.hostMemBytes = 1 << 40 })
	claim := func(policy types.Target, runs int) cache.Step {
		var step cache.Step
		m.sizeClaim(&step, chainProject(".", map[string]types.Target{"scip": policy}, nil), "scip", newClaimSizer(history(runs), shape, m.chainReadsCharm))
		return step
	}

	measuredSlots := types.SlotSizing{Source: types.SlotsMeasured, Samples: minMeasuredRuns}
	flat := "scip: no memory curve for this shape (0 of 3 runs); claimed the 2.0 GiB peak"
	assert.Equal(t, cache.Step{MemoryMB: 2000, MemoryDeclaredBy: "scip", MemorySizing: types.MemorySizing{Samples: minMeasuredRuns, Slots: measuredSlots, Fallback: flat}, Slots: 3},
		claim(types.Target{}, minMeasuredRuns), "ceil(1.25 x 1600) MiB and three slots")
	assert.Equal(t, cache.Step{MemorySizing: types.MemorySizing{Slots: types.SlotSizing{Source: types.SlotsUnmeasured}}},
		claim(types.Target{}, minMeasuredRuns-1), "too few runs claim no memory and one slot")
	assert.Equal(t, cache.Step{MemoryMB: 2000, MemoryDeclaredBy: "scip", MemorySizing: types.MemorySizing{Samples: minMeasuredRuns, Slots: types.SlotSizing{Source: types.SlotsDeclared}, Fallback: flat}, Slots: 1},
		claim(types.Target{Slots: 1}, minMeasuredRuns), "declared slots beat the measured width")
	capped := "scip: no memory curve for this shape (0 of 3 runs); claimed the 1.0 GiB peak"
	assert.Equal(t, cache.Step{MemoryMB: 1024, MemoryDeclaredBy: "scip", MemorySizing: types.MemorySizing{Slots: measuredSlots, Fallback: capped}, Slots: 3},
		claim(types.Target{MemoryMB: 1024}, minMeasuredRuns), "declared memory caps the measured peak")
}

// A memory claim worth more slots than the target's own count says so, since a reader
// sent to its slots or its runs would find nothing that explains the figure. Under the
// broker the claim keeps its declared slots, so there is nothing to name.
func TestSizeClaimNamesSlotsTakenForMemory(t *testing.T) {
	m := &Magus{}
	m.limOnce.Do(func() { m.lim = cache.NewLimiter(8) })
	m.hostMemOnce.Do(func() { m.hostMemBytes = 12 << 30 }) // a 9216 MiB budget, 1152 MiB a slot
	p := chainProject(".", map[string]types.Target{"test": {MemoryMB: 3072, Slots: 2}}, nil)
	var step cache.Step
	sizing := m.sizeClaim(&step, p, "test", nil)
	assert.Equal(t, 3, step.Slots)
	assert.Equal(t, types.SlotSizing{Source: types.SlotsForMemory}, sizing.Slots)

	c, err := cache.Open(t.Context(), filepath.Join(t.TempDir(), ".magus"),
		cache.WithMachineAdmission(broker.NewClient("unix:///nonexistent/broker.sock")))
	require.NoError(t, err)
	m.cache = c
	step = cache.Step{}
	sizing = m.sizeClaim(&step, p, "test", nil)
	assert.Equal(t, 2, step.Slots)
	assert.Equal(t, types.SlotSizing{Source: types.SlotsDeclared}, sizing.Slots)
}

// A Go target runs only as wide as its claim, so a claim its runs saturate doubles, up
// to the budget, rather than settling at what the cap let them measure.
func TestSizeClaimGrowsASaturatedClaim(t *testing.T) {
	shape := forecast.NewShape(nil, nil)
	m := &Magus{}
	m.limOnce.Do(func() { m.lim = cache.NewLimiter(8) })
	m.hostMemOnce.Do(func() { m.hostMemBytes = 1 << 40 })
	slots := func(claimed int) int {
		run := forecast.Outcome{Result: forecast.OutcomePass, CPUMs: int64(claimed) * 1000, DurationMs: 1000, Slots: claimed, Charms: shape.Charms, ArgsHash: shape.ArgsHash}
		h := forecast.History{Projects: map[string]map[string]forecast.Stats{
			".": {"go/test": {RecentOutcomes: slices.Repeat([]forecast.Outcome{run}, minMeasuredRuns)}},
		}}
		x := h.MeasuredRuns()
		var step cache.Step
		m.sizeClaim(&step, chainProject(".", map[string]types.Target{"test": {}}, nil), "test", newClaimSizer(&x, shape, m.chainReadsCharm))
		return step.Slots
	}
	assert.Equal(t, 2, slots(1), "one slot kept busy claims two")
	assert.Equal(t, 8, slots(4), "four slots kept busy claim eight")
	assert.Equal(t, 8, slots(8), "the budget caps the doubling")
}

// shapedRun is a passing run of shape that peaked at peakMB.
func shapedRun(shape forecast.Shape, peakMB int64) forecast.Outcome {
	return forecast.Outcome{Result: forecast.OutcomePass, MaxRSSBytes: peakMB * mib, Charms: shape.Charms, ArgsHash: shape.ArgsHash}
}

// readsNoCharm is a chain that no charm changes.
func readsNoCharm(*types.Project, string, string) bool { return false }

// Narrowed runs of any args pool to size a narrowed run, and only ever lower its claim:
// the full runs' trusted peak caps it, and with too few full runs to trust, the
// narrowed peak stands.
func TestNarrowedClaimIsCappedByTheFullRunPeak(t *testing.T) {
	p := chainProject(".", map[string]types.Target{"test": {}}, nil)
	narrowed := func(arg string) forecast.Shape {
		return forecast.NewShape([]string{types.CharmReadWrite}, []string{"-run", arg})
	}
	full := forecast.NewShape(nil, nil)
	claim := func(runs ...forecast.Outcome) (int, types.MemorySizing) {
		h := forecast.History{Projects: map[string]map[string]forecast.Stats{".": {"go/test": {RecentOutcomes: runs}}}}
		x := h.MeasuredRuns()
		mb, sizing, _ := newClaimSizer(&x, narrowed("X"), readsNoCharm).memory(p, "test", 0)
		return mb, sizing
	}

	mb, sizing := claim(shapedRun(narrowed("A"), 6000), shapedRun(narrowed("B"), 6000), shapedRun(narrowed("C"), 6000),
		shapedRun(full, 4000), shapedRun(full, 4000), shapedRun(full, 4000))
	assert.Equal(t, 5000, mb, "ceil(1.25 x the 4000 MiB full-run peak), not the 6000 MiB narrowed one")
	assert.Equal(t, 3, sizing.Samples, "sized from the narrowed runs, across three arg hashes")
	assert.False(t, sizing.AnyShape)

	mb, _ = claim(shapedRun(narrowed("A"), 2000), shapedRun(narrowed("B"), 2000), shapedRun(narrowed("C"), 2000),
		shapedRun(full, 4000), shapedRun(full, 4000), shapedRun(full, 4000))
	assert.Equal(t, 2500, mb, "a narrowed peak below the full one lowers the claim")

	mb, _ = claim(shapedRun(narrowed("A"), 6000), shapedRun(narrowed("B"), 6000), shapedRun(narrowed("C"), 6000),
		shapedRun(full, 4000), shapedRun(full, 4000))
	assert.Equal(t, 7500, mb, "two full runs are too few to cap anything")
}

// A full run is sized from full runs only. Narrowed runs, however many, neither make its
// own shape trusted nor reach its any-shape fallback.
func TestNarrowedRunsNeverSizeAFullRun(t *testing.T) {
	p := chainProject(".", map[string]types.Target{"test": {MemoryMB: 10240}}, nil)
	full := forecast.NewShape(nil, nil)
	narrowed := forecast.NewShape(nil, []string{"-run", "TestOne"})
	h := forecast.History{Projects: map[string]map[string]forecast.Stats{".": {"go/test": {RecentOutcomes: []forecast.Outcome{
		shapedRun(narrowed, 300), shapedRun(narrowed, 300), shapedRun(narrowed, 300), shapedRun(narrowed, 300),
		shapedRun(full, 4000), shapedRun(full, 4000),
	}}}}}
	x := h.MeasuredRuns()

	mb, sizing, _ := newClaimSizer(&x, full, readsNoCharm).memory(p, "test", 10240)
	assert.Equal(t, 10240, mb, "two full runs are too few, and the narrowed runs do not make up the count")
	assert.Equal(t, types.MemorySizing{}, sizing)
}

func TestSizeSlotClaim(t *testing.T) {
	cases := []struct {
		name      string
		same, all forecast.Measurement
		capacity  int
		want      int
	}{
		{name: "runs of this shape", same: forecast.Measurement{Value: 3, Runs: minMeasuredRuns}, all: forecast.Measurement{Value: 6, Runs: 10}, capacity: 8, want: 3},
		{name: "too few of this shape falls back to every run", same: forecast.Measurement{Value: 1, Runs: minMeasuredRuns - 1}, all: forecast.Measurement{Value: 6, Runs: 10}, capacity: 8, want: 6},
		{name: "capped at the budget", same: forecast.Measurement{Value: 12, Runs: minMeasuredRuns}, capacity: 8, want: 8},
		{name: "an unlimited budget caps nothing", same: forecast.Measurement{Value: 12, Runs: minMeasuredRuns}, want: 12},
		{name: "too few runs claim no width", all: forecast.Measurement{Value: 6, Runs: minMeasuredRuns - 1}, capacity: 8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := sizeSlotClaim(tc.same, tc.all, tc.capacity)
			assert.Equal(t, tc.want, got)
		})
	}
}
