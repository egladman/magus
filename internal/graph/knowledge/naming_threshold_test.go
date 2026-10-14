package knowledge

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

const (
	thresholdFile = "internal/a/a.go"
	thresholdPath = "hack/policy/conform.json"
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
		conformPkg(a, []string{thresholdFile}),
		conformPkg(b, []string{"internal/b/b.go"}),
		named(conformSym(a, "Frobnicate().", thresholdFile, 3, 12, 40, "S1", 90), "Frobnicate"),
		named(conformSym(a, "Hobnob().", thresholdFile, 14, 16, 40, "", 0), "Hobnob"),
		named(conformSym(b, "Gadget().", "internal/b/b.go", 3, 4, 20, "S1", 90), "Gadget"),
	}
	g = mergeAll([]Shard{assembleSymbols(".", syms, nil)})
	return g, map[string]string{
		"Frobnicate": symbolID(conformNS(a) + "Frobnicate()."),
		"Hobnob":     symbolID(conformNS(a) + "Hobnob()."),
	}
}

func thresholdOptions() types.ConformOptions {
	return types.ConformOptions{
		Limits: map[types.ConformKey]types.ConformLimit{
			{Metric: types.ConformFuncLines, Population: types.ConformPopSource}:   {Percentile: 95, Anchor: 8},
			{Metric: types.ConformCloneTokens, Population: types.ConformPopSource}: {Percentile: 50, Anchor: 80},
		},
		MinSample:   1,
		Step:        0.05,
		RatchetPath: thresholdPath,
		Ratchet:     types.ConformRatchet{Schema: 1},
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

	got := g.Conformance(ConformanceChange{Subjects: []string{id["Frobnicate"], id["Hobnob"]}, Conform: thresholdOptions()})

	assert.Equal(t, []string{pastLines, pastClone}, thresholdMessages(t, got[id["Frobnicate"]]))
	assert.NotContains(t, got, id["Hobnob"], "three lines and no shared body is past nothing")
}

func TestThresholdCheckEnforcesTheCommittedValueOverTheAnchor(t *testing.T) {
	g, id := thresholdFixture()
	o := thresholdOptions()
	o.Ratchet.Thresholds = []types.ConformThreshold{{
		ConformKey: types.ConformKey{Metric: types.ConformFuncLines, Population: types.ConformPopSource}, Value: 6,
	}}

	got := g.Conformance(ConformanceChange{Subjects: []string{id["Frobnicate"]}, Conform: o})

	assert.Equal(t, []string{
		"`Frobnicate` measures 10 for func-lines source, past the committed value of 6", pastClone,
	}, thresholdMessages(t, got[id["Frobnicate"]]))
}

func TestThresholdCheckIsSilentWithoutARatchet(t *testing.T) {
	g, id := thresholdFixture()
	o := thresholdOptions()
	o.RatchetPath = ""

	assert.Empty(t, g.Conformance(ConformanceChange{Subjects: []string{id["Frobnicate"]}, Conform: o}))
	assert.Empty(t, g.Conformance(ConformanceChange{Subjects: []string{id["Frobnicate"]}, Conform: testConform()}), "no policy at all")
}

func TestThresholdCheckSkipsAKnownItem(t *testing.T) {
	g, id := thresholdFixture()
	o := thresholdOptions()
	o.Ratchet.Known = []types.ConformKnown{{
		Metric: types.ConformFuncLines, Items: []string{thresholdFile + "#Frobnicate"}, Reason: "split pending",
	}}

	got := g.Conformance(ConformanceChange{Subjects: []string{id["Frobnicate"]}, Conform: o})

	assert.Equal(t, []string{pastClone}, thresholdMessages(t, got[id["Frobnicate"]]))
}

func TestThresholdCheckReportsASubjectThatPassesOnlyBecauseTheRatchetMoved(t *testing.T) {
	g, id := thresholdFixture()
	base := thresholdOptions().Ratchet

	t.Run("ceiling raised", func(t *testing.T) {
		o := thresholdOptions()
		o.Ratchet.Thresholds = []types.ConformThreshold{{
			ConformKey: types.ConformKey{Metric: types.ConformFuncLines, Population: types.ConformPopSource}, Value: 20,
		}}
		got := g.Conformance(ConformanceChange{Subjects: []string{id["Frobnicate"]}, Conform: o, RatchetBase: &base})

		assert.Equal(t, []string{
			"`Frobnicate` measures 10 for func-lines source, past the 8 the base enforced; this change raises the ceiling from 8 to 20",
			pastClone,
		}, thresholdMessages(t, got[id["Frobnicate"]]))
	})

	t.Run("listed as known", func(t *testing.T) {
		o := thresholdOptions()
		o.Ratchet.Known = []types.ConformKnown{{
			Metric: types.ConformFuncLines, Items: []string{thresholdFile + "#Frobnicate"}, Reason: "split pending",
		}}
		got := g.Conformance(ConformanceChange{Subjects: []string{id["Frobnicate"]}, Conform: o, RatchetBase: &base})

		assert.Equal(t, []string{
			"`Frobnicate` measures 10 for func-lines source, past the 8 the base enforced; this change lists it as known",
			pastClone,
		}, thresholdMessages(t, got[id["Frobnicate"]]))
	})

	t.Run("unchanged ratchet", func(t *testing.T) {
		got := g.Conformance(ConformanceChange{Subjects: []string{id["Frobnicate"]}, Conform: thresholdOptions(), RatchetBase: &base})

		assert.Equal(t, []string{pastLines, pastClone}, thresholdMessages(t, got[id["Frobnicate"]]),
			"already past in both, so nothing is reported as moved")
	})
}

func TestThresholdCheckHonorsTheFindingCap(t *testing.T) {
	g, id := thresholdFixture()

	o := thresholdOptions()
	o.Gates.MaxFindings = 1
	got := g.Conformance(ConformanceChange{Subjects: []string{id["Frobnicate"]}, Conform: o})

	assert.Equal(t, []string{pastLines}, thresholdMessages(t, got[id["Frobnicate"]]))
}
