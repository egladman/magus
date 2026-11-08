package magus

import (
	"github.com/egladman/magus/types/diff"
	"fmt"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/types"
)

type TouchedFunctionsSuite struct {
	suite.Suite
	defs   map[string]symbolDef
	ids    map[string]string
	byPath map[string]*diff.File
}

func TestTouchedFunctionsSuite(t *testing.T) { suite.Run(t, new(TouchedFunctionsSuite)) }

const touchedNS = "gomod example.com/m `example.com/m/internal/trail`/"

func touchedTestSymbol(key, label, kind, path string, line, end int) types.KnowledgeSymbol {
	return types.KnowledgeSymbol{
		Key: touchedNS + key, Label: label, Language: "go", SymbolKind: kind, Namespace: touchedNS,
		Source: fmt.Sprintf("%s:%d", path, line), DefEndLine: end, Defs: []string{path},
		BodyDigest: "d-" + label,
	}
}

func (s *TouchedFunctionsSuite) SetupTest() {
	const file = "internal/trail/trail.go"
	syms := []types.KnowledgeSymbol{
		touchedTestSymbol("Add().", "Add", "Function", file, 10, 14),
		touchedTestSymbol("Resign().", "Resign", "Method", file, 20, 22),
		touchedTestSymbol("Grow().", "Grow", "Function", file, 30, 40),
		touchedTestSymbol("Same().", "Same", "Function", file, 50, 52),
		touchedTestSymbol("Shape#", "Shape", "Struct", file, 60, 64),
		touchedTestSymbol("TestGrow().", "TestGrow", "Function", "internal/trail/trail_test.go", 5, 9),
		touchedTestSymbol("Made().", "Made", "Function", "internal/trail/gen.go", 5, 9),
	}
	g := knowledge.NewGraph()
	for _, sh := range knowledge.AssembleShards(knowledge.Inputs{
		Graph:   types.TargetGraphOutput{Projects: []types.TargetGraphProject{{Path: "."}}},
		Symbols: map[string][]types.KnowledgeSymbol{".": syms},
	}) {
		g.Merge(sh.Nodes, sh.Edges)
	}
	s.defs = definedSymbols(g.Nodes())
	s.ids = map[string]string{}
	for id, def := range s.defs {
		s.ids[def.node.Label] = id
	}
	s.byPath = map[string]*diff.File{
		file:                           {Path: file, Project: "."},
		"internal/trail/trail_test.go": {Path: "internal/trail/trail_test.go", Project: "."},
		"internal/trail/gen.go":        {Path: "internal/trail/gen.go", Project: ".", Role: diff.RoleOutput},
	}
}

func (s *TouchedFunctionsSuite) baseline() conformanceInput {
	return conformanceInput{changes: map[string]string{
		s.ids["Add"]:      diff.ChangeAdded,
		s.ids["Resign"]:   diff.ChangeSignature,
		s.ids["Grow"]:     diff.ChangeBody,
		s.ids["Shape"]:    diff.ChangeBody,
		s.ids["TestGrow"]: diff.ChangeBody,
		s.ids["Made"]:     diff.ChangeBody,
	}}
}

func (s *TouchedFunctionsSuite) labels(fns []touchedFunction) []string {
	var out []string
	for _, fn := range fns {
		out = append(out, fmt.Sprintf("%s:%s", fn.Label, fn.Change))
	}
	return out
}

func (s *TouchedFunctionsSuite) TestBaselineNamesAddedResignedAndEditedFunctionsOnly() {
	got := touchedFunctions(s.defs, s.byPath, s.baseline())

	s.ElementsMatch([]string{"Add:added", "Resign:signature", "Grow:body"}, s.labels(got))
}

func (s *TouchedFunctionsSuite) TestFunctionsComeInSymbolIDOrder() {
	got := touchedFunctions(s.defs, s.byPath, s.baseline())

	s.Equal([]string{"Add:added", "Grow:body", "Resign:signature"}, s.labels(got))
}

func (s *TouchedFunctionsSuite) TestAFunctionCarriesItsExtentDigestAndLanguage() {
	got := touchedFunctions(s.defs, s.byPath, s.baseline())

	s.Equal(touchedFunction{
		Symbol: s.ids["Grow"], Label: "Grow", Path: "internal/trail/trail.go", Language: "go",
		Digest: "d-Grow", Change: diff.ChangeBody, Line: 30, EndLine: 40,
	}, got[1])
}

func (s *TouchedFunctionsSuite) TestAnUnchangedFunctionIsNotTouched() {
	got := touchedFunctions(s.defs, s.byPath, s.baseline())

	s.NotContains(s.labels(got), "Same:body")
	s.NotContains(s.labels(got), "Same:added")
}

func (s *TouchedFunctionsSuite) TestATypeIsNotAFunction() {
	got := touchedFunctions(s.defs, s.byPath, s.baseline())

	s.NotContains(s.labels(got), "Shape:body")
}

func (s *TouchedFunctionsSuite) TestATestFunctionIsNotTouched() {
	got := touchedFunctions(s.defs, s.byPath, s.baseline())

	s.NotContains(s.labels(got), "TestGrow:body")
}

func (s *TouchedFunctionsSuite) TestAFunctionInGeneratedOutputIsNotTouched() {
	got := touchedFunctions(s.defs, s.byPath, s.baseline())

	s.NotContains(s.labels(got), "Made:body")
}

func (s *TouchedFunctionsSuite) TestAFileOutsideTheReviewTouchesNothing() {
	delete(s.byPath, "internal/trail/trail.go")

	got := touchedFunctions(s.defs, s.byPath, s.baseline())

	s.Empty(got)
}

func (s *TouchedFunctionsSuite) TestWithoutABaselineAnAddedLineMakesAFunctionNew() {
	patch := "--- a/internal/trail/trail.go\n+++ b/internal/trail/trail.go\n@@ -9,0 +10,5 @@\n+func Add() {\n+\tx()\n+}\n+\n+// end\n"

	got := touchedFunctions(s.defs, s.byPath, conformanceInput{patch: patch})

	s.Equal([]string{"Add:added"}, s.labels(got))
}

func (s *TouchedFunctionsSuite) TestWithoutABaselineARemovedLineNamingItMeansItMovedNotAdded() {
	patch := "--- a/internal/trail/trail.go\n+++ b/internal/trail/trail.go\n@@ -9,1 +10,1 @@\n-func Add(old int) {\n+func Add() {\n"

	got := touchedFunctions(s.defs, s.byPath, conformanceInput{patch: patch})

	s.Empty(got)
}

func (s *TouchedFunctionsSuite) TestClassesSortSubjectsFromTouched() {
	c := classifyChanges(s.defs, s.byPath, s.baseline())

	s.Equal(
		[]string{"Add", "Resign", "Grow"},
		[]string{s.defs[c.subjects[0]].node.Label, s.defs[c.subjects[1]].node.Label, s.defs[c.touched[0]].node.Label})
	s.Equal(map[string]bool{s.ids["Add"]: true}, c.introduced())
}
