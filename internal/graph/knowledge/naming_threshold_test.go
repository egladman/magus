package knowledge

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

const (
	thresholdFile = "internal/a/a.go"
	thresholdPath = "hack/policy/conformance.json"
)

// thresholdFixture is two functions sharing one 90-token body, the first ten lines long, and a
// short third.
func thresholdFixture() (g *Graph, subjects map[string]string) {
	const a, b = "/internal/a", "/internal/b"
	named := func(s types.KnowledgeSymbol, label string) types.KnowledgeSymbol {
		s.Label = label
		return s
	}
	syms := []types.KnowledgeSymbol{
		conformancePkg(a, []string{thresholdFile}),
		conformancePkg(b, []string{"internal/b/b.go"}),
		named(conformanceSym(a, "Frobnicate().", thresholdFile, 3, 12, 40, "S1", 90), "Frobnicate"),
		named(conformanceSym(a, "Hobnob().", thresholdFile, 14, 16, 40, "", 0), "Hobnob"),
		named(conformanceSym(b, "Gadget().", "internal/b/b.go", 3, 4, 20, "S1", 90), "Gadget"),
	}
	g = mergeAll([]Shard{assembleSymbols(".", syms, nil)})
	return g, map[string]string{
		"Frobnicate": symbolID(conformanceNS(a) + "Frobnicate()."),
		"Hobnob":     symbolID(conformanceNS(a) + "Hobnob()."),
	}
}

func thresholdOptions() types.ConformanceOptions {
	return types.ConformanceOptions{
		Limits: map[types.ConformanceKey]types.ConformanceLimit{
			{Metric: types.ConformanceFuncLines, Population: types.ConformancePopSource}:   {Percentile: 95, Anchor: 8},
			{Metric: types.ConformanceCloneTokens, Population: types.ConformancePopSource}: {Percentile: 50, Anchor: 80},
		},
		MinSample:   1,
		Step:        0.05,
		RatchetPath: thresholdPath,
		Ratchet:     types.ConformanceRatchet{Schema: 1},
		Gates:       testGates(),
	}
}

func thresholdMessages(t *testing.T, checks []types.Check) []string {
	t.Helper()
	var out []string
	for _, c := range checks {
		require.Equal(t, checkThreshold, c.Name)
		require.Equal(t, types.CheckAdvice, c.Status)
		out = append(out, c.Message)
	}
	return out
}

const (
	pastLines = "`Frobnicate` measures 10 for func-lines source, past the configured anchor of 8"
	pastClone = "`Frobnicate` measures 90 for clone-tokens source, past the configured anchor of 80"
)

func TestThresholdCheckReportsASubjectPastItsValue(t *testing.T) {
	g, id := thresholdFixture()

	got := g.ConformanceChecks(ConformanceChange{Subjects: []string{id["Frobnicate"], id["Hobnob"]}, Conformance: thresholdOptions()})

	assert.Equal(t, []string{pastLines, pastClone}, thresholdMessages(t, got[id["Frobnicate"]]))
	assert.NotContains(t, got, id["Hobnob"], "three lines and no shared body is past nothing")
}

func TestThresholdCheckEnforcesTheCommittedValueOverTheAnchor(t *testing.T) {
	g, id := thresholdFixture()
	o := thresholdOptions()
	o.Ratchet.Thresholds = []types.ConformanceThreshold{{
		ConformanceKey: types.ConformanceKey{Metric: types.ConformanceFuncLines, Population: types.ConformancePopSource}, Value: 6,
	}}

	got := g.ConformanceChecks(ConformanceChange{Subjects: []string{id["Frobnicate"]}, Conformance: o})

	assert.Equal(t, []string{
		"`Frobnicate` measures 10 for func-lines source, past the committed value of 6", pastClone,
	}, thresholdMessages(t, got[id["Frobnicate"]]))
}

func TestThresholdCheckIsSilentWithoutARatchet(t *testing.T) {
	g, id := thresholdFixture()
	o := thresholdOptions()
	o.RatchetPath = ""

	assert.Empty(t, g.ConformanceChecks(ConformanceChange{Subjects: []string{id["Frobnicate"]}, Conformance: o}))
	assert.Empty(t, g.ConformanceChecks(ConformanceChange{Subjects: []string{id["Frobnicate"]}, Conformance: testConformance()}), "no policy at all")
}

func TestThresholdCheckSkipsAKnownItem(t *testing.T) {
	g, id := thresholdFixture()
	o := thresholdOptions()
	o.Ratchet.Known = []types.ConformanceKnown{{
		Metric: types.ConformanceFuncLines, Items: []string{thresholdFile + "#Frobnicate"}, Reason: "split pending",
	}}

	got := g.ConformanceChecks(ConformanceChange{Subjects: []string{id["Frobnicate"]}, Conformance: o})

	assert.Equal(t, []string{pastClone}, thresholdMessages(t, got[id["Frobnicate"]]))
}

func TestThresholdCheckReportsASubjectThatPassesOnlyBecauseTheRatchetMoved(t *testing.T) {
	g, id := thresholdFixture()
	base := thresholdOptions().Ratchet

	t.Run("ceiling raised", func(t *testing.T) {
		o := thresholdOptions()
		o.Ratchet.Thresholds = []types.ConformanceThreshold{{
			ConformanceKey: types.ConformanceKey{Metric: types.ConformanceFuncLines, Population: types.ConformancePopSource}, Value: 20,
		}}
		got := g.ConformanceChecks(ConformanceChange{Subjects: []string{id["Frobnicate"]}, Conformance: o, RatchetBase: &base})

		assert.Equal(t, []string{
			"`Frobnicate` measures 10 for func-lines source, past the 8 the base enforced; this change raises the ceiling from 8 to 20",
			pastClone,
		}, thresholdMessages(t, got[id["Frobnicate"]]))
	})

	t.Run("listed as known", func(t *testing.T) {
		o := thresholdOptions()
		o.Ratchet.Known = []types.ConformanceKnown{{
			Metric: types.ConformanceFuncLines, Items: []string{thresholdFile + "#Frobnicate"}, Reason: "split pending",
		}}
		got := g.ConformanceChecks(ConformanceChange{Subjects: []string{id["Frobnicate"]}, Conformance: o, RatchetBase: &base})

		assert.Equal(t, []string{
			"`Frobnicate` measures 10 for func-lines source, past the 8 the base enforced; this change lists it as known",
			pastClone,
		}, thresholdMessages(t, got[id["Frobnicate"]]))
	})

	t.Run("unchanged ratchet", func(t *testing.T) {
		got := g.ConformanceChecks(ConformanceChange{Subjects: []string{id["Frobnicate"]}, Conformance: thresholdOptions(), RatchetBase: &base})

		assert.Equal(t, []string{pastLines, pastClone}, thresholdMessages(t, got[id["Frobnicate"]]),
			"already past in both, so nothing is reported as moved")
	})
}

func TestThresholdCheckHonorsTheFindingCap(t *testing.T) {
	g, id := thresholdFixture()

	o := thresholdOptions()
	o.Gates.MaxFindings = 1
	got := g.ConformanceChecks(ConformanceChange{Subjects: []string{id["Frobnicate"]}, Conformance: o})

	assert.Equal(t, []string{pastLines}, thresholdMessages(t, got[id["Frobnicate"]]))
}

func TestThresholdPastLeavesAMetricWithItsOwnCheckToThatCheck(t *testing.T) {
	const item = thresholdFile + "#Frobnicate"
	row := func(m types.ConformanceMetric) types.ConformanceRow {
		return types.ConformanceRow{
			Key:        types.ConformanceKey{Metric: m, Population: types.ConformancePopSource},
			Value:      1,
			Violations: []types.ConformanceItem{{ID: item, Measure: 9}},
		}
	}
	out := types.ConformanceOutput{Rows: []types.ConformanceRow{row(types.ConformanceFuncLines)}}
	for m := range ownCheck {
		out.Rows = append(out.Rows, row(m))
	}

	hits := thresholdPast(out, map[string]*namingDecl{item: {id: "frob", label: "Frobnicate"}})

	require.Len(t, hits, 1)
	assert.Equal(t, types.ConformanceFuncLines, hits[0].key.Metric)
}

// thresholdGrowthGraph is one function spanning lines 3 to 2+lines, and a short second function
// that serves as the change's subject.
func thresholdGrowthGraph(lines int) (g *Graph, ids map[string]string) {
	const a = "/internal/a"
	frob := conformanceSym(a, "Frobnicate().", thresholdFile, 3, 2+lines, lines, "", 0)
	frob.Label = "Frobnicate"
	hob := conformanceSym(a, "Hobnob().", thresholdFile, 100, 102, 3, "", 0)
	hob.Label = "Hobnob"
	g = mergeAll([]Shard{assembleSymbols(".", []types.KnowledgeSymbol{conformancePkg(a, []string{thresholdFile}), frob, hob}, nil)})
	return g, map[string]string{
		"Frobnicate": symbolID(conformanceNS(a) + "Frobnicate()."),
		"Hobnob":     symbolID(conformanceNS(a) + "Hobnob()."),
	}
}

func thresholdGrowthOptions(known bool) types.ConformanceOptions {
	o := thresholdOptions()
	o.Ratchet.Thresholds = []types.ConformanceThreshold{{
		ConformanceKey: types.ConformanceKey{Metric: types.ConformanceFuncLines, Population: types.ConformancePopSource}, Value: 52,
	}}
	if known {
		o.Ratchet.Known = []types.ConformanceKnown{{
			Metric: types.ConformanceFuncLines, Items: []string{thresholdFile + "#Frobnicate"}, Reason: "split pending",
		}}
	}
	return o
}

func TestThresholdCheckReportsAKnownItemTheChangeGrew(t *testing.T) {
	head, id := thresholdGrowthGraph(64)
	base, _ := thresholdGrowthGraph(60)
	const grew = "`Frobnicate` measures 64 for func-lines source, up from 60 at the base, past the committed value of 52"

	t.Run("touched body", func(t *testing.T) {
		got := head.ConformanceChecks(ConformanceChange{
			Subjects: []string{id["Hobnob"]}, Touched: []string{id["Frobnicate"]}, Base: base, Conformance: thresholdGrowthOptions(true),
		})

		assert.Equal(t, []string{grew}, thresholdMessages(t, got[id["Frobnicate"]]))
	})

	t.Run("body-only change", func(t *testing.T) {
		got := head.ConformanceChecks(ConformanceChange{
			Touched: []string{id["Frobnicate"]}, Base: base, Conformance: thresholdGrowthOptions(true),
		})

		assert.Equal(t, []string{grew}, thresholdMessages(t, got[id["Frobnicate"]]))
	})

	t.Run("re-signed subject", func(t *testing.T) {
		got := head.ConformanceChecks(ConformanceChange{
			Subjects: []string{id["Frobnicate"]}, Base: base, Conformance: thresholdGrowthOptions(true),
		})

		assert.Equal(t, []string{grew}, thresholdMessages(t, got[id["Frobnicate"]]))
	})

	t.Run("not touched", func(t *testing.T) {
		got := head.ConformanceChecks(ConformanceChange{
			Subjects: []string{id["Hobnob"]}, Base: base, Conformance: thresholdGrowthOptions(true),
		})

		assert.Empty(t, got, "a known item the change leaves alone is nobody's to answer for")
	})

	t.Run("without a base graph", func(t *testing.T) {
		got := head.ConformanceChecks(ConformanceChange{
			Subjects: []string{id["Hobnob"]}, Touched: []string{id["Frobnicate"]}, Conformance: thresholdGrowthOptions(true),
		})

		assert.Empty(t, got)
	})
}

func TestThresholdCheckIsSilentWhenAKnownItemDidNotGrow(t *testing.T) {
	for name, lines := range map[string][2]int{"shrank": {64, 60}, "unchanged": {64, 64}} {
		t.Run(name, func(t *testing.T) {
			head, id := thresholdGrowthGraph(lines[1])
			base, _ := thresholdGrowthGraph(lines[0])

			got := head.ConformanceChecks(ConformanceChange{
				Subjects: []string{id["Hobnob"]}, Touched: []string{id["Frobnicate"]}, Base: base, Conformance: thresholdGrowthOptions(true),
			})

			assert.Empty(t, got)
		})
	}
}

func TestThresholdCheckLeavesAnUnlistedGrowthToTheRatchet(t *testing.T) {
	head, id := thresholdGrowthGraph(64)
	base, _ := thresholdGrowthGraph(60)

	got := head.ConformanceChecks(ConformanceChange{
		Subjects: []string{id["Hobnob"]}, Touched: []string{id["Frobnicate"]}, Base: base, Conformance: thresholdGrowthOptions(false),
	})

	assert.Empty(t, got, "an unlisted item past its value is a violation lint reports, not growth")
}
