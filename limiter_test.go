package magus

import (
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/ci/forecast"
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

// gbTest is the host total these tests pin. slotsForPolicy takes it as an
// argument, so nothing here depends on the machine running the suite: the same
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

// One ctx.needs call runs its members together, so their declarations add; a second
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
			mb, sizing := sizeMemoryClaim(tc.declaredMB, tc.same, tc.all)
			assert.Equal(t, tc.wantMB, mb)
			assert.Equal(t, tc.wantSizing, sizing)
		})
	}
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
	sizing := m.sizeClaim(&step, p, "ci", newClaimSizer(&x, shape))
	// test is sized to 1.25 x 4000; lint has no runs, so its declaration stands.
	want := types.MemorySizing{Samples: 3, Slots: types.SlotSizing{Source: types.SlotsUnmeasured}}
	assert.Equal(t, cache.Step{MemoryMB: 5000 + 1024, MemoryDeclaredBy: "test", MemorySizing: want, Slots: 1}, step)
	assert.Equal(t, want, sizing)

	assert.Nil(t, newClaimSizer(nil, shape), "no history, no sizer: declarations fold as written")
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
		m.sizeClaim(&step, chainProject(".", map[string]types.Target{"scip": policy}, nil), "scip", newClaimSizer(history(runs), shape))
		return step
	}

	measuredSlots := types.SlotSizing{Source: types.SlotsMeasured, Samples: minMeasuredRuns}
	assert.Equal(t, cache.Step{MemoryMB: 2000, MemoryDeclaredBy: "scip", MemorySizing: types.MemorySizing{Samples: minMeasuredRuns, Slots: measuredSlots}, Slots: 3},
		claim(types.Target{}, minMeasuredRuns), "ceil(1.25 x 1600) MiB and three slots")
	assert.Equal(t, cache.Step{MemorySizing: types.MemorySizing{Slots: types.SlotSizing{Source: types.SlotsUnmeasured}}},
		claim(types.Target{}, minMeasuredRuns-1), "too few runs claim no memory and one slot")
	assert.Equal(t, cache.Step{MemoryMB: 2000, MemoryDeclaredBy: "scip", MemorySizing: types.MemorySizing{Samples: minMeasuredRuns, Slots: types.SlotSizing{Source: types.SlotsDeclared}}, Slots: 1},
		claim(types.Target{Slots: 1}, minMeasuredRuns), "declared slots beat the measured width")
	assert.Equal(t, cache.Step{MemoryMB: 1024, MemoryDeclaredBy: "scip", MemorySizing: types.MemorySizing{Slots: measuredSlots}, Slots: 3},
		claim(types.Target{MemoryMB: 1024}, minMeasuredRuns), "declared memory caps the measured peak")
}

// A memory claim worth more slots than the target's own count says so, since a reader
// sent to its slots or its runs would find nothing that explains the figure.
func TestSizeClaimNamesSlotsTakenForMemory(t *testing.T) {
	m := &Magus{}
	m.limOnce.Do(func() { m.lim = cache.NewLimiter(8) })
	m.hostMemOnce.Do(func() { m.hostMemBytes = 8 << 30 }) // 1 GiB a slot
	var step cache.Step
	sizing := m.sizeClaim(&step, chainProject(".", map[string]types.Target{"test": {MemoryMB: 3072, Slots: 2}}, nil), "test", nil)
	assert.Equal(t, 3, step.Slots)
	assert.Equal(t, types.SlotSizing{Source: types.SlotsForMemory}, sizing.Slots)
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
		m.sizeClaim(&step, chainProject(".", map[string]types.Target{"test": {}}, nil), "test", newClaimSizer(&x, shape))
		return step.Slots
	}
	assert.Equal(t, 2, slots(1), "one slot kept busy claims two")
	assert.Equal(t, 8, slots(4), "four slots kept busy claim eight")
	assert.Equal(t, 8, slots(8), "the budget caps the doubling")
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
