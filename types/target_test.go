package types

import (
	"context"
	"testing"
	"time"

	"github.com/egladman/magus/spells"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTargetString(t *testing.T) {
	assert.Equal(t, "api:build", Target{Path: "api", Name: "build"}.String())
	// An empty path (all-projects target) still renders the leading colon.
	assert.Equal(t, ":test", Target{Name: "test"}.String())
}

func TestParseTarget(t *testing.T) {
	got, err := ParseTarget("build") // bare target
	require.NoError(t, err)
	assert.Equal(t, Target{Name: "build"}, got)

	got, err = ParseTarget("lint:read")
	require.NoError(t, err)
	assert.Equal(t, Target{Name: "lint", Charms: []string{"read"}}, got)

	got, err = ParseTarget("lint:read,strict")
	require.NoError(t, err)
	assert.Equal(t, Target{Name: "lint", Charms: []string{"read", "strict"}}, got)

	got, err = ParseTarget("format:write")
	require.NoError(t, err)
	assert.Equal(t, Target{Name: "format", Charms: []string{"write"}}, got)

	// Project is positional now; this is target=api charm=build.
	got, err = ParseTarget("api:build")
	require.NoError(t, err)
	assert.Equal(t, Target{Name: "api", Charms: []string{"build"}}, got)
}

// Declared records the raw spelling ONLY when normalization rewrote it, so a
// caller can react to a non-canonical name without the parser printing anything.
func TestParseTarget_NormalizesName(t *testing.T) {
	canonical, err := ParseTarget("go-build")
	require.NoError(t, err)
	assert.Equal(t, Target{Name: "go-build"}, canonical, "canonical input carries no provenance")

	for _, in := range []string{"go_build", "goBuild"} {
		got, err := ParseTarget(in)
		require.NoErrorf(t, err, "ParseTarget(%q)", in)
		assert.Equalf(t, Target{Name: "go-build", Declared: in}, got, "ParseTarget(%q)", in)
	}

	// Charms carry the same provenance, and only the rewritten ones.
	mixed, err := ParseTarget("lint:rw,NoCache")
	require.NoError(t, err)
	assert.Equal(t, Target{
		Name:           "lint",
		Charms:         []string{"rw", "no-cache"},
		DeclaredCharms: []string{"NoCache"},
	}, mixed, "only the charm that was rewritten is recorded")
}

func TestParseTarget_Errors(t *testing.T) {
	invalid := []string{
		"lint:",           // empty charm
		"lint:read,",      // empty charm in list
		"web/studio:test", // '/' not allowed in target
		"go::lint",        // '::' not a target char (spell filter stripped earlier)
		"",                // empty string
	}
	for _, in := range invalid {
		_, err := ParseTarget(in)
		assert.Errorf(t, err, "ParseTarget(%q) should error", in)
	}
}

func TestValidateTargetName(t *testing.T) {
	for _, n := range []string{"build", "test", "lint-fix", "gen_2", "ABC123", "a"} {
		require.NoErrorf(t, ValidateTargetName(n), "ValidateTargetName(%q)", n)
	}
	for _, n := range []string{"", "lint:read", "go::lint", "foo@bar", "web/studio", "build prod", "test.unit"} {
		assert.Errorf(t, ValidateTargetName(n), "ValidateTargetName(%q) should error", n)
	}
}

// Normalize is the one canonicalizer for every entity kind, so the same spellings
// must converge whether they name a target, a charm or a spell op.
//
// These cases ARE the table published in docs/concepts/targets.md ("Name
// normalization"). Documented behavior that nothing asserts is documented
// intent, not documented behavior: if a row here changes, that page is wrong
// and this test is the thing that says so.
func TestNormalize(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"go-build", "go-build"},      // already canonical
		{"go_build", "go-build"},      // '_' is a delimiter
		{"goBuild", "go-build"},       // camelCase boundary
		{"GoBuild", "go-build"},       // PascalCase boundary
		{"Go_Build", "go-build"},      // both at once
		{"HTTPServer", "http-server"}, // acronym breaks before its last letter
		{"build2", "build-2"},         // letter/digit boundary
		{"go--build", "go-build"},     // delimiter runs collapse
		{"build", "build"},            // single word untouched
		{"image_build_static", "image-build-static"},
		{"no_cache", "no-cache"}, // the charm cases
		{"NoCache", "no-cache"},
		{"WRITE", "write"},
	} {
		assert.Equalf(t, c.want, Normalize(c.in), "Normalize(%q)", c.in)
	}
}

func TestKebabCase(t *testing.T) {
	cases := []struct{ in, want string }{
		{"FooBar", "foo-bar"},
		{"fooBarBaz", "foo-bar-baz"},
		{"HTTPServer", "http-server"},
		{"fmt", "fmt"},
		{"build2", "build-2"},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, kebabCase(c.in), "kebabCase(%q)", c.in)
	}
}

func TestValidateCharmName(t *testing.T) {
	for _, n := range []string{"read", "write", "strict", "ci-only", "x"} {
		require.NoErrorf(t, ValidateCharmName(n), "ValidateCharmName(%q)", n)
	}
	for _, n := range []string{"", "read:write", "a b", "fast@v2"} {
		assert.Errorf(t, ValidateCharmName(n), "ValidateCharmName(%q) should error", n)
	}
}

// expiredCeiling is a context already past its deadline, which is the only state
// CeilingExceededError reports on.
func expiredCeiling(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithDeadline(WithDependencyWait(context.Background()), time.Now().Add(-time.Second))
	t.Cleanup(cancel)
	return ctx
}

// The split is the whole point of the message: elapsed time alone reads as "this target is
// slow", which is wrong exactly when it matters most, for a target queued behind a
// serialization upstream while doing seconds of its own work.
func TestCeilingExceededErrorSplitsWaitingFromOwnWork(t *testing.T) {
	ctx := expiredCeiling(t)
	DependencyWaitFromContext(ctx).Add(14 * time.Minute)

	err := CeilingExceededError(ctx, nil, "ci", 15*time.Minute, 15*time.Minute+52*time.Second)

	require.Error(t, err)
	assert.Contains(t, err.Error(), `target "ci" exceeded its declared timeout of 15m0s after 15m52s`)
	assert.Contains(t, err.Error(), "14m0s of it on the targets it composes and 1m52s on its own work")
}

// A leaf target composes nothing, so there is no split to report and the clause is omitted
// rather than printed as a pair of zeroes.
//
// Asserted as one contiguous span, not as NotContains on the clause's wording: a negative
// assertion on emitted prose goes vacuous the moment that prose is reworded, and this has to
// fail whatever a wrongly-emitted split would have said.
func TestCeilingExceededErrorOmitsTheSplitForALeaf(t *testing.T) {
	err := CeilingExceededError(expiredCeiling(t), nil, "build", time.Minute, 90*time.Second)

	require.Error(t, err)
	assert.Contains(t, err.Error(),
		`target "build" exceeded its declared timeout of 1m0s after 1m30s; its process tree was killed`)
}

// TestChainMemoryMBPeaksOverOneCall: members of one ctx.needs call run together, and with
// no curves their declarations add; a later call runs after, so it competes with the sum rather than
// joining it.
func TestChainMemoryMBPeaksOverOneCall(t *testing.T) {
	t.Parallel()
	p := &Project{
		Path: ".",
		TargetChains: map[string][]ChainStep{
			"ci":   Needs("lint", "test").Needs("bench"),
			"test": Needs("gen"),
		},
		TargetPolicies: map[string]Target{
			"lint":  {MemoryMB: 1000},
			"test":  {MemoryMB: 2000},
			"gen":   {MemoryMB: 500},
			"bench": {MemoryMB: 2500},
		},
	}
	mb, by := ChainMemoryMB(p, "ci", nil)
	assert.Equal(t, 3000, mb, "lint and test share a call; bench runs after them and alone")
	assert.Equal(t, "test", by, "the largest contributor to the peak call")
}

// TestSizedChainMemoryFoldsSizedFigures: each declaration is sized before it folds, so
// the sums and the peak are taken over what the members will claim, and the sizing
// reported is the largest contributor's.
func TestSizedChainMemoryFoldsSizedFigures(t *testing.T) {
	t.Parallel()
	p := &Project{
		Path: ".",
		TargetChains: map[string][]ChainStep{
			"ci": Needs("lint", "test").Needs("bench"),
		},
		TargetPolicies: map[string]Target{
			"lint":  {MemoryMB: 1000},
			"test":  {MemoryMB: 10000},
			"bench": {MemoryMB: 4000},
		},
	}
	var sized []string
	size := func(proj *Project, target string, declaredMB int) (int, MemorySizing, MemoryProfile) {
		sized = append(sized, target)
		if target == "test" {
			return 2000, MemorySizing{Samples: 7}, MemoryProfile{}
		}
		return declaredMB, MemorySizing{}, MemoryProfile{}
	}

	got := SizedChainMemory(p, "ci", nil, size)
	assert.Equal(t, ChainMemory{MB: 4000, DeclaredBy: "bench"}, got,
		"lint+test sized to 3000 now loses to bench, which it beat as declared")
	assert.ElementsMatch(t, []string{"ci", "lint", "test", "bench"}, sized, "ci declares nothing and is sized too")

	p.TargetPolicies["bench"] = Target{MemoryMB: 1500}
	fallback := "lint: no memory curve; summed the peaks of its ctx.needs call"
	assert.Equal(t, ChainMemory{MB: 3000, DeclaredBy: "test", Sizing: MemorySizing{Samples: 7, Fallback: fallback}},
		SizedChainMemory(p, "ci", nil, size))

	mb, by := ChainMemoryMB(p, "ci", nil)
	assert.Equal(t, 11000, mb, "ChainMemoryMB folds the declarations as written")
	assert.Equal(t, "test", by)
}

// TestSizedChainMemorySizesAnUndeclaredMember: a member declaring nothing is sized from
// 0, so a composed step claims its measured peak beside its peers' declarations, and an
// unmeasured one still contributes nothing.
func TestSizedChainMemorySizesAnUndeclaredMember(t *testing.T) {
	t.Parallel()
	p := &Project{
		Path:           ".",
		TargetChains:   map[string][]ChainStep{"ci": Needs("lint", "scip")},
		TargetPolicies: map[string]Target{"lint": {MemoryMB: 1000}},
	}
	measured := map[string]int{"scip": 2000}
	size := func(proj *Project, target string, declaredMB int) (int, MemorySizing, MemoryProfile) {
		if mb, ok := measured[target]; ok {
			return mb, MemorySizing{Samples: 3}, MemoryProfile{}
		}
		return declaredMB, MemorySizing{}, MemoryProfile{}
	}

	fallback := "lint: no memory curve; summed the peaks of its ctx.needs call"
	assert.Equal(t, ChainMemory{MB: 3000, DeclaredBy: "scip", Sizing: MemorySizing{Samples: 3, Fallback: fallback}},
		SizedChainMemory(p, "ci", nil, size), "scip's measured peak sums with lint's declaration")

	delete(measured, "scip")
	assert.Equal(t, ChainMemory{MB: 1000, DeclaredBy: "lint"}, SizedChainMemory(p, "ci", nil, size))
}

// halves is a profile over a fixed 100s run holding early for its first half and late
// for its second.
func halves(early, late uint32, d10Ms int64) MemoryProfile {
	p := MemoryProfile{D10Ms: d10Ms, D90Ms: 100_000, Runs: 3}
	for i := range p.Curve {
		p.Curve[i] = early
		if i >= MemoryCurveBuckets/2 {
			p.Curve[i] = late
		}
	}
	return p
}

// curveSizer claims each target's curve peak with its curve, as the limiter's sizer
// does once a shape has enough runs.
func curveSizer(profiles map[string]MemoryProfile) MemorySizer {
	return func(proj *Project, target string, declaredMB int) (int, MemorySizing, MemoryProfile) {
		p, ok := profiles[target]
		if !ok {
			return declaredMB, MemorySizing{}, MemoryProfile{}
		}
		return p.Curve.PeakMB(), MemorySizing{Samples: p.Runs}, p
	}
}

// TestChainCurveSumsOneCallOverTime: one call's members whose peaks fall in different
// halves of the run claim the peak of their sum, not the sum of their peaks; calls in
// turn still take the larger.
func TestChainCurveSumsOneCallOverTime(t *testing.T) {
	t.Parallel()
	p := &Project{
		Path:         ".",
		TargetChains: map[string][]ChainStep{"ci": Needs("test", "lint").Needs("bench")},
	}
	size := curveSizer(map[string]MemoryProfile{
		"test":  halves(500, 7000, 100_000),
		"lint":  halves(4000, 300, 100_000),
		"bench": halves(1000, 1000, 100_000),
	})
	assert.Equal(t, ChainMemory{MB: 7300, DeclaredBy: "test", Sizing: MemorySizing{Samples: 3}},
		SizedChainMemory(p, "ci", nil, size), "test's late 7000 overlaps lint's late 300, not its early 4000")

	size = curveSizer(map[string]MemoryProfile{
		"test":  halves(500, 7000, 100_000),
		"lint":  halves(4000, 300, 100_000),
		"bench": halves(9000, 9000, 100_000),
	})
	got := SizedChainMemory(p, "ci", nil, size)
	assert.Equal(t, 9000, got.MB, "the call after runs alone and is larger")
	assert.Equal(t, "bench", got.DeclaredBy)
	assert.Equal(t, MemoryProfile{}, got.Profile, "two calls in turn are not one run's curve")

	alone := &Project{Path: ".", TargetChains: map[string][]ChainStep{"ci": Needs("bench")}}
	assert.Equal(t, halves(9000, 9000, 100_000), SizedChainMemory(alone, "ci", nil, size).Profile,
		"a lone member's call carries its curve")

	missing := &Project{
		Path:           ".",
		TargetChains:   map[string][]ChainStep{"ci": Needs("test", "lint")},
		TargetPolicies: map[string]Target{"lint": {MemoryMB: 4000}},
	}
	got = SizedChainMemory(missing, "ci", nil, curveSizer(map[string]MemoryProfile{"test": halves(500, 7000, 100_000)}))
	assert.Equal(t, 11000, got.MB, "lint has no curve, so the call sums its peaks")
	assert.Equal(t, "lint: no memory curve; summed the peaks of its ctx.needs call", got.Sizing.Fallback)
	assert.Equal(t, MemoryProfile{}, got.Profile, "a folded claim carries no single run's curve")
}

// TestChainCurveNeverExceedsSummedPeak: a wide duration band lets peaks overlap, which
// the fold claims in full, and a curve above its member's claim is capped at that claim,
// so the fold is never above the summed peaks.
func TestChainCurveNeverExceedsSummedPeak(t *testing.T) {
	t.Parallel()
	p := &Project{Path: ".", TargetChains: map[string][]ChainStep{"ci": Needs("test", "lint")}}
	size := curveSizer(map[string]MemoryProfile{
		"test": halves(500, 7000, 50_000),
		"lint": halves(4000, 300, 50_000),
	})
	assert.Equal(t, 11000, SizedChainMemory(p, "ci", nil, size).MB,
		"with D10 at half of D90, test may be late while lint is still early")

	capped := func(proj *Project, target string, declaredMB int) (int, MemorySizing, MemoryProfile) {
		return 1000, MemorySizing{Samples: 3}, halves(5000, 5000, 100_000)
	}
	assert.Equal(t, 2000, SizedChainMemory(p, "ci", nil, capped).MB, "each curve is capped at its member's 1000")
}

func TestMemorySizingString(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "declared", MemorySizing{}.String())
	assert.Equal(t, "measured over 54 runs", MemorySizing{Samples: 54}.String())
	assert.Equal(t, "measured over 9 runs of any shape", MemorySizing{Samples: 9, AnyShape: true}.String())
}

func TestSlotSizingString(t *testing.T) {
	t.Parallel()
	assert.Empty(t, SlotSizing{}.String(), "nothing recorded says nothing")
	assert.Equal(t, "declared", SlotSizing{Source: SlotsDeclared}.String())
	assert.Equal(t, "unmeasured", SlotSizing{Source: SlotsUnmeasured}.String())
	assert.Equal(t, "for its memory", SlotSizing{Source: SlotsForMemory}.String())
	assert.Equal(t, "measured over 4 runs", SlotSizing{Source: SlotsMeasured, Samples: 4}.String())
	assert.Equal(t, "measured over 7 runs of any shape", SlotSizing{Source: SlotsMeasured, Samples: 7, AnyShape: true}.String())
}

// TestChainBuilderReadsLikeTheBody: Needs(a, b).Needs(c) is `ctx.needs(a, b); ctx.needs(c)`,
// with a cross-project step spelled the way ChainStep.Ref prints one.
func TestChainBuilderReadsLikeTheBody(t *testing.T) {
	t.Parallel()
	assert.Equal(t, Chain{
		{Target: "generate"}, {Project: "libs/gopherbuzz", Target: "build"},
		{Target: "test", CallIndex: 1},
	}, Needs("generate", "libs/gopherbuzz:build").Needs("test"))
	assert.Equal(t, "libs/gopherbuzz:build", Needs("libs/gopherbuzz:build")[0].Ref(), "and prints back the same way")
}

// The two units of work must keep identical Key signatures so a future consumer can
// declare one interface over both. Asserted from the test rather than by an exported
// interface: nothing consumes them yet, and Go declares an interface where it is used.
//
// It matters more now that they live in separate packages: spells.Op and types.Target
// can no longer drift into each other's shape by accident, and nothing else holds them
// to a signature a single interface could still cover.
func TestKeySignaturesMatch(t *testing.T) {
	t.Parallel()
	var (
		_ interface{ Key() []string } = spells.Op{}
		_ interface{ Key() []string } = Target{}
	)
}

// A composer that declares its inputs keys only on the gate artifacts those inputs can
// read, and a gate whose artifacts all fall outside is not one it must run ahead.
func TestChainSkipCacheOutputsHonorsAnExplicitFootprint(t *testing.T) {
	p := &Project{
		Path: ".",
		TargetPolicies: map[string]Target{
			"graph": {SkipCache: true},
			"index": {SkipCache: true},
		},
		TargetOutputs: map[string][]OutputRef{
			"graph": {{Glob: "gen/graph.json"}},
			"index": {{Glob: "MAGUS.md"}},
		},
		TargetChains: map[string][]ChainStep{"test": {{Target: "graph"}, {Target: "index"}}},
	}
	assert.Equal(t, MustParseGlobs("MAGUS.md", "gen/graph.json"), ChainSkipCacheOutputs(p, "test", nil),
		"no declared footprint keys every artifact")

	p.TargetInputs = map[string][]InputRef{"test": {{Glob: "**/*.go"}, {Glob: "MAGUS.md"}}}
	assert.Equal(t, MustParseGlobs("MAGUS.md"), ChainSkipCacheOutputs(p, "test", nil))
	assert.Equal(t, []ChainStep{{Project: ".", Target: "index"}}, ChainSkipCacheSteps(p, "test", nil))
}
