package knowledge

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
)

const thresholdFile = "internal/a/a.go"

const (
	pastLines = "`Frobnicate` measures 10 for func-lines source, past the configured anchor of 8"
	pastClone = "`Frobnicate` measures 90 for clone-tokens source, past the configured anchor of 80"
	grewLines = "`Frobnicate` measures 64 for func-lines source, up from 60 at the base, past the configured anchor of 52"
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

func thresholdOptions(funcLines int) types.ConformanceOptions {
	return types.ConformanceOptions{
		Limits: map[types.ConformanceKey]types.ConformanceLimit{
			{Metric: types.ConformanceFuncLines, Population: types.ConformancePopSource}:   {Percentile: 95, Anchor: funcLines},
			{Metric: types.ConformanceCloneTokens, Population: types.ConformancePopSource}: {Percentile: 50, Anchor: 80},
		},
		MinSample: 1,
		Step:      0.05,
		Gates:     testGates(),
	}
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

type ThresholdSuite struct {
	suite.Suite
}

func TestThresholdSuite(t *testing.T) {
	suite.Run(t, new(ThresholdSuite))
}

func (s *ThresholdSuite) messages(checks []types.Check) []string {
	var out []string
	for _, c := range checks {
		s.Require().Equal(checkThreshold, c.Name)
		s.Require().Equal(types.CheckAdvice, c.Status)
		out = append(out, c.Message)
	}
	return out
}

// grown is what the check says of Frobnicate grown from was to now lines under a 52-line
// anchor, for the change c names.
func (s *ThresholdSuite) grown(was, now int, c ConformanceChange) map[string][]types.Check {
	head, _ := thresholdGrowthGraph(now)
	base, _ := thresholdGrowthGraph(was)
	c.Base, c.Conformance = base, thresholdOptions(52)
	return head.ConformanceChecks(c)
}

func (s *ThresholdSuite) TestReportsASubjectPastItsAnchor() {
	g, id := thresholdFixture()
	got := g.ConformanceChecks(ConformanceChange{Subjects: []string{id["Frobnicate"], id["Hobnob"]}, Conformance: thresholdOptions(8)})
	s.Equal([]string{pastLines, pastClone}, s.messages(got[id["Frobnicate"]]))
	s.NotContains(got, id["Hobnob"], "three lines and no shared body is past nothing")
}

func (s *ThresholdSuite) TestIsSilentWithNoPolicy() {
	g, id := thresholdFixture()
	s.Empty(g.ConformanceChecks(ConformanceChange{Subjects: []string{id["Frobnicate"]}, Conformance: testConformance()}))
}

func (s *ThresholdSuite) TestHonorsTheFindingCap() {
	g, id := thresholdFixture()
	o := thresholdOptions(8)
	o.Gates.MaxFindings = 1
	got := g.ConformanceChecks(ConformanceChange{Subjects: []string{id["Frobnicate"]}, Conformance: o})
	s.Equal([]string{pastLines}, s.messages(got[id["Frobnicate"]]))
}

func (s *ThresholdSuite) TestReportsATouchedBodyTheChangeGrew() {
	_, id := thresholdGrowthGraph(64)
	got := s.grown(60, 64, ConformanceChange{Subjects: []string{id["Hobnob"]}, Touched: []string{id["Frobnicate"]}})
	s.Equal([]string{grewLines}, s.messages(got[id["Frobnicate"]]))
}

func (s *ThresholdSuite) TestReportsABodyOnlyChangeThatGrew() {
	_, id := thresholdGrowthGraph(64)
	got := s.grown(60, 64, ConformanceChange{Touched: []string{id["Frobnicate"]}})
	s.Equal([]string{grewLines}, s.messages(got[id["Frobnicate"]]))
}

func (s *ThresholdSuite) TestReportsAReSignedSubjectThatGrew() {
	_, id := thresholdGrowthGraph(64)
	got := s.grown(60, 64, ConformanceChange{Subjects: []string{id["Frobnicate"]}})
	s.Equal([]string{grewLines}, s.messages(got[id["Frobnicate"]]))
}

func (s *ThresholdSuite) TestLeavesAnItemTheChangeDidNotTouch() {
	_, id := thresholdGrowthGraph(64)
	s.Empty(s.grown(60, 64, ConformanceChange{Subjects: []string{id["Hobnob"]}}),
		"an item the change leaves alone is nobody's to answer for")
}

func (s *ThresholdSuite) TestIgnoresTouchedBodiesWithoutABaseGraph() {
	head, id := thresholdGrowthGraph(64)
	s.Empty(head.ConformanceChecks(ConformanceChange{
		Subjects: []string{id["Hobnob"]}, Touched: []string{id["Frobnicate"]}, Conformance: thresholdOptions(52),
	}))
}

func (s *ThresholdSuite) TestPassesAnItemThatShrank() {
	_, id := thresholdGrowthGraph(60)
	s.Empty(s.grown(64, 60, ConformanceChange{Touched: []string{id["Frobnicate"]}}))
}

func (s *ThresholdSuite) TestPassesAnItemPastByAsMuchAtTheBase() {
	_, id := thresholdGrowthGraph(64)
	s.Empty(s.grown(64, 64, ConformanceChange{Touched: []string{id["Frobnicate"]}}))
}

func (s *ThresholdSuite) TestReportsATouchedItemNewlyPast() {
	_, id := thresholdGrowthGraph(64)
	got := s.grown(40, 64, ConformanceChange{Touched: []string{id["Frobnicate"]}})
	s.Equal([]string{"`Frobnicate` measures 64 for func-lines source, past the configured anchor of 52"}, s.messages(got[id["Frobnicate"]]))
}

func (s *ThresholdSuite) TestPastLeavesAMetricWithItsOwnCheckToThatCheck() {
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
	s.Require().Len(hits, 1)
	s.Equal(types.ConformanceFuncLines, hits[0].key.Metric)
}
