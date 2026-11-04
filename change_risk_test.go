package magus

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/coverage"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

type ChangeRiskSuite struct {
	suite.Suite
	files   map[string]string
	spell   *spells.Spell
	report  keptReport
	reason  string
	decided map[string]int
}

func TestChangeRiskSuite(t *testing.T) { suite.Run(t, new(ChangeRiskSuite)) }

// riskBody holds F on lines 3 to 8; one if and one && make complexity 3.
const riskBody = "package a\n\n" +
	"func F(x int) int {\n" +
	"\tif x > 0 && x < 9 {\n" +
	"\t\treturn 1 // if\n" +
	"\t}\n" +
	"\treturn 0\n" +
	"}\n"

func riskSyntax() *spells.Syntax {
	return &spells.Syntax{
		Comments:  &spells.CommentSyntax{LineComments: []string{"//"}, Quotes: []spells.Quote{{Open: `"`, Close: `"`}}},
		Operators: []string{"&&", "||"},
		Decisions: &spells.DecisionSyntax{Keywords: []string{"if", "for", "case"}, Operators: []string{"&&", "||"}},
	}
}

func riskCoverageOp() map[string]spells.Op {
	cov := &spells.Coverage{Format: spells.CoverageFormatGoProfile, File: "cover.out", Test: "go-test"}
	return map[string]spells.Op{spells.CoverageOp: {Kind: spells.OpKindCoverage, Coverage: cov}}
}

func (s *ChangeRiskSuite) SetupTest() {
	s.files = map[string]string{"lib/a.go": riskBody}
	s.spell = spells.NewSpell("go", spells.WithLanguageExtensions([]string{".go"}),
		spells.WithSyntax(riskSyntax()), spells.WithOps(riskCoverageOp()))
	s.report = keptReport{project: "lib", files: []coverage.File{{Path: "lib/a.go", Blocks: []coverage.Block{
		{StartLine: 3, EndLine: 4, Units: 1, Hits: 1},
		{StartLine: 5, EndLine: 5, Units: 1},
		{StartLine: 7, EndLine: 7, Units: 2, Hits: 1},
	}}}}
	s.reason = ""
	s.decided = map[string]int{}
}

func (s *ChangeRiskSuite) scorer() riskScorer {
	return riskScorer{
		read: func(p string) (string, bool) {
			src, ok := s.files[p]
			return src, ok
		},
		claimant: func(p string) *spells.Spell {
			if p == "lib/a.go" {
				return s.spell
			}
			return nil
		},
		owner: func(string) string { return "lib" },
		indexed: func(id string) (int, bool) {
			n, ok := s.decided[id]
			return n, ok
		},
		report: func(string, spells.Coverage) (keptReport, string) { return s.report, s.reason },
	}
}

func scoredFunction() touchedFunction {
	return touchedFunction{Symbol: "sym F", Label: "F", Path: "lib/a.go", Language: "go", Digest: "d", Change: types.DiffChangeBody, Line: 3, EndLine: 8}
}

func (s *ChangeRiskSuite) gap(complexity int, reason string) types.ChangeRisk {
	return types.ChangeRisk{Definition: riskDefinition, Gaps: []types.FunctionGap{{
		Symbol: "sym F", Label: "F", Path: "lib/a.go", Language: "go", Line: 3, Complexity: complexity, Reason: reason,
	}}}
}

func (s *ChangeRiskSuite) TestScoresAMeasuredFunction() {
	got := s.scorer().scoreAll([]touchedFunction{scoredFunction()})

	s.Equal(types.ChangeRisk{Definition: riskDefinition, Functions: []types.ChangeRiskFunction{{
		Symbol: "sym F", Label: "F", Path: "lib/a.go", Language: "go", Digest: "d",
		Line: 3, EndLine: 8, Complexity: 3, Covered: 3, Coverable: 4, Score: types.ChangeRiskScore(3, 0.75),
	}}}, got)
}

func (s *ChangeRiskSuite) TestAFunctionWithNoExtentIsAGap() {
	fn := scoredFunction()
	fn.EndLine = 0

	s.Equal(s.gap(0, "the go indexer records no body extent"), s.scorer().scoreAll([]touchedFunction{fn}))
}

func (s *ChangeRiskSuite) TestAFileNoSpellClaimsIsAGap() {
	fn := scoredFunction()
	fn.Path = "lib/a.zig"

	want := s.gap(0, "no spell bound to lib claims .zig files")
	want.Gaps[0].Path = "lib/a.zig"
	s.Equal(want, s.scorer().scoreAll([]touchedFunction{fn}))
}

func (s *ChangeRiskSuite) TestAnUnreadableFileIsAGap() {
	delete(s.files, "lib/a.go")

	s.Equal(s.gap(0, "lib/a.go lines 3-8 could not be read"), s.scorer().scoreAll([]touchedFunction{scoredFunction()}))
}

func (s *ChangeRiskSuite) TestASyntaxThatCannotCountIsAGap() {
	syn := riskSyntax()
	syn.Comments = nil
	s.spell = spells.NewSpell("go", spells.WithLanguageExtensions([]string{".go"}), spells.WithSyntax(syn))

	s.Equal(s.gap(0, "spell go: the syntax declares no comments, so a token in a comment or string would count as code"),
		s.scorer().scoreAll([]touchedFunction{scoredFunction()}))
}

func (s *ChangeRiskSuite) TestIndexerComplexityStandsInForADeclaredSyntax() {
	s.spell = spells.NewSpell("go", spells.WithLanguageExtensions([]string{".go"}), spells.WithOps(riskCoverageOp()))
	s.decided["sym F"] = 5

	got := s.scorer().scoreAll([]touchedFunction{scoredFunction()})

	s.Require().Len(got.Functions, 1)
	s.Equal(5, got.Functions[0].Complexity)
}

func (s *ChangeRiskSuite) TestNoDecisionsAnywhereIsAGap() {
	s.spell = spells.NewSpell("go", spells.WithLanguageExtensions([]string{".go"}))

	s.Equal(s.gap(0, "spell go declares no decisions and its indexer counted none"),
		s.scorer().scoreAll([]touchedFunction{scoredFunction()}))
}

func (s *ChangeRiskSuite) TestASpellWithNoCoverageIsAGapThatKeepsItsComplexity() {
	s.spell = spells.NewSpell("go", spells.WithLanguageExtensions([]string{".go"}), spells.WithSyntax(riskSyntax()))

	s.Equal(s.gap(3, "spell go declares no coverage (mgs_getCoverage)"), s.scorer().scoreAll([]touchedFunction{scoredFunction()}))
}

func (s *ChangeRiskSuite) TestNoKeptReportIsAGap() {
	s.reason = "no coverage report is kept for lib: run `./magus run coverage lib`"

	s.Equal(s.gap(3, s.reason), s.scorer().scoreAll([]touchedFunction{scoredFunction()}))
}

func (s *ChangeRiskSuite) TestAFileTheReportLeavesOutIsAGap() {
	s.report.files = nil

	s.Equal(s.gap(3, "the coverage report for lib instruments no line of lib/a.go"),
		s.scorer().scoreAll([]touchedFunction{scoredFunction()}))
}

func (s *ChangeRiskSuite) TestASpanWithNoInstrumentedLineIsAGap() {
	s.report.files[0].Blocks = []coverage.Block{{StartLine: 20, EndLine: 21, Units: 1, Hits: 1}}

	s.Equal(s.gap(3, "the coverage report for lib instruments no line in lib/a.go lines 3-8"),
		s.scorer().scoreAll([]touchedFunction{scoredFunction()}))
}

func (s *ChangeRiskSuite) TestBodyLinesCutsTheInclusiveRange() {
	body, ok := bodyLines("a\nb\nc\nd\n", 2, 3)
	s.True(ok)
	s.Equal("b\nc", body)
}

func (s *ChangeRiskSuite) TestBodyLinesRefusesARangePastTheEnd() {
	_, ok := bodyLines("a\nb\n", 2, 9)
	s.False(ok)
}

func (s *ChangeRiskSuite) TestForwardLineShiftsPastAnEarlierHunk() {
	moved, ok := forwardLine(10, []changeset.Hunk{{OldStart: 2, OldCount: 1, NewStart: 2, NewCount: 3}})
	s.True(ok)
	s.Equal(12, moved)
}

func (s *ChangeRiskSuite) TestForwardLineKeepsALineBeforeEveryHunk() {
	moved, ok := forwardLine(4, []changeset.Hunk{{OldStart: 5, OldCount: 2, NewStart: 5, NewCount: 0}})
	s.True(ok)
	s.Equal(4, moved)
}

func (s *ChangeRiskSuite) TestForwardLineDropsARewrittenLine() {
	_, ok := forwardLine(6, []changeset.Hunk{{OldStart: 5, OldCount: 2, NewStart: 5, NewCount: 1}})
	s.False(ok)
}

func (s *ChangeRiskSuite) TestForwardLineKeepsTheLineAnInsertionFollows() {
	moved, ok := forwardLine(5, []changeset.Hunk{{OldStart: 5, OldCount: 0, NewStart: 6, NewCount: 2}})
	s.True(ok)
	s.Equal(5, moved)
}

const riskCommitted = "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -10,1 +10,1 @@\n-\treturn 0\n+\treturn 1\n"

const riskWorking = "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -2,0 +3,2 @@\n+// one\n+// two\n"

func (s *ChangeRiskSuite) TestChangedLinesCarriesCommittedLinesThroughTheWorkingTree() {
	s.Equal(map[string][]int{"a.go": {3, 4, 12}}, changedLines(riskCommitted, riskWorking))
}

func (s *ChangeRiskSuite) TestSpansTouchedMarksADefinitionAChangedLineFallsIn() {
	defs := map[string]symbolDef{
		"in":  {path: "a.go", node: types.KnowledgeNode{Source: "a.go:10", Attrs: map[string]string{attrDefEndLine: "14"}}},
		"out": {path: "a.go", node: types.KnowledgeNode{Source: "a.go:20", Attrs: map[string]string{attrDefEndLine: "24"}}},
		"one": {path: "a.go", node: types.KnowledgeNode{Source: "a.go:30"}},
	}

	s.Equal(map[string]string{"in": types.DiffChangeBody, "one": types.DiffChangeBody},
		spansTouched(defs, sinceBase{lines: map[string][]int{"a.go": {12, 30}}}, func(string) (string, bool) { return "", false }))
}

func (s *ChangeRiskSuite) TestClaimingSpellMatchesTheExtension() {
	ts := spells.NewSpell("typescript", spells.WithLanguageExtensions([]string{".ts"}))

	s.Same(s.spell, claimingSpell([]*spells.Spell{ts, s.spell}, "lib/A.GO"))
}

func (s *ChangeRiskSuite) TestClaimingSpellIsNilForAnUnclaimedExtension() {
	s.Nil(claimingSpell([]*spells.Spell{s.spell}, "lib/a.rs"))
}

func (s *ChangeRiskSuite) TestIndexedComplexityReadsTheAttribute() {
	defs := map[string]symbolDef{"f": {node: types.KnowledgeNode{Attrs: map[string]string{knowledge.AttrComplexity: "1"}}}}

	n, counted := indexedComplexity(defs)("f")

	s.Equal([2]any{1, true}, [2]any{n, counted})
}

func (s *ChangeRiskSuite) TestIndexedComplexityWithoutTheAttributeCountedNothing() {
	_, counted := indexedComplexity(map[string]symbolDef{"f": {}})("f")
	s.False(counted)
}

func (s *ChangeRiskSuite) TestKeepReportsReadsEachReportOnce() {
	reads := 0
	read := oncePerProject(func(project string, _ spells.Coverage) (keptReport, string) {
		reads++
		return keptReport{project: project}, ""
	})

	read("lib", spells.Coverage{File: "cover.out"})
	got, reason := read("lib", spells.Coverage{File: "cover.out"})

	s.Equal([3]any{1, "lib", ""}, [3]any{reads, got.project, reason})
}

func (s *ChangeRiskSuite) TestSymbolChangesListsEachChangedSymbol() {
	files := []types.DiffFile{{Symbols: []types.DiffSymbol{{ID: "a", Change: types.DiffChangeBody}, {ID: "b"}}}}

	s.Equal(map[string]string{"a": types.DiffChangeBody}, symbolChanges(files))
}

func (s *ChangeRiskSuite) TestAttachChangeRiskChecksAddsAScoreAndAGap() {
	files := []types.DiffFile{{Symbols: []types.DiffSymbol{
		{ID: "f", Checks: []types.Check{{Name: "naming"}}}, {ID: "g"}, {ID: "h"},
	}}}
	risk := types.ChangeRisk{
		Functions: []types.ChangeRiskFunction{{Symbol: "f", Complexity: 3, Covered: 3, Coverable: 4, Score: 3.04}},
		Gaps:      []types.FunctionGap{{Symbol: "g", Reason: "spell go declares no coverage (mgs_getCoverage)"}},
	}

	attachChangeRiskChecks(files, risk)

	s.Equal([]types.DiffSymbol{
		{ID: "f", Checks: []types.Check{{Name: "naming"}, {
			Name: riskCheck, Status: types.CheckAdvice, Evidence: types.EvidenceMeasured,
			Message: "change risk 3.0: complexity 3, 3 of 4 coverable units covered",
		}}},
		{ID: "g", Checks: []types.Check{{
			Name: riskCheck, Status: types.CheckAdvice, Evidence: types.EvidenceUnknown,
			Message: "change risk unmeasured: spell go declares no coverage (mgs_getCoverage)",
		}}},
		{ID: "h"},
	}, files[0].Symbols)
}

type ChangeRiskWorkspaceSuite struct {
	suite.Suite
	m *Magus
}

func TestChangeRiskWorkspaceSuite(t *testing.T) { suite.Run(t, new(ChangeRiskWorkspaceSuite)) }

func (s *ChangeRiskWorkspaceSuite) SetupTest() {
	root := writeWorkspace(s.T(), map[string]string{
		"magusfile.buzz": "",
		"go.mod":         "module example.com/m\n\ngo 1.25\n",
		"a.go":           riskBody,
		"a_test.go":      "package a\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) { F(1) }\n",
	})
	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell("go"))
	m, err := Open(s.T().Context(), root, WithWorkspaceRegistry(reg), WithSpawnRule(TierCeiling))
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = m.Close() })
	s.m = m
}

func (s *ChangeRiskWorkspaceSuite) keep(name, report string) {
	dir := coverage.Dir(s.m.CacheDir(), ".")
	s.Require().NoError(os.MkdirAll(dir, 0o755))
	s.Require().NoError(os.WriteFile(filepath.Join(dir, name), []byte(report), 0o644))
}

func (s *ChangeRiskWorkspaceSuite) TestReadsAKeptGoProfile() {
	s.keep("cover.out", "mode: set\nexample.com/m/a.go:3.20,4.10 1 1\n")

	got, reason := s.m.readCoverageReport(".", spells.Coverage{Format: spells.CoverageFormatGoProfile, File: "cover.out"})

	s.Empty(reason)
	s.Equal([]coverage.File{{Path: "a.go", Covered: 1, Total: 1, Blocks: []coverage.Block{{StartLine: 3, EndLine: 4, Units: 1, Hits: 1}}}}, got.files)
}

func (s *ChangeRiskWorkspaceSuite) TestReadsAKeptLCOVTracefile() {
	s.keep("lcov.info", "SF:a.go\nDA:5,0\nend_of_record\n")

	got, reason := s.m.readCoverageReport(".", spells.Coverage{Format: spells.CoverageFormatLCOV, File: "lcov.info"})

	s.Empty(reason)
	s.Equal([]coverage.File{{Path: "a.go", Total: 1, Blocks: []coverage.Block{{StartLine: 5, EndLine: 5, Units: 1}}}}, got.files)
}

func (s *ChangeRiskWorkspaceSuite) TestNoKeptReportNamesTheCommand() {
	_, reason := s.m.readCoverageReport(".", spells.Coverage{Format: spells.CoverageFormatLCOV, File: "lcov.info"})

	s.Equal("no coverage report is kept for .: run `./magus run coverage .`", reason)
}

func (s *ChangeRiskWorkspaceSuite) TestAGoProfileOutsideAnyModuleIsRefused() {
	s.Require().NoError(os.Remove(filepath.Join(s.m.Root(), "go.mod")))
	s.keep("cover.out", "mode: set\n")

	_, reason := s.m.readCoverageReport(".", spells.Coverage{Format: spells.CoverageFormatGoProfile, File: "cover.out"})

	s.Equal("the coverage report for .: no go.mod at or above . names the module its profile's paths are in", reason)
}

func (s *ChangeRiskWorkspaceSuite) TestReadWorkspaceFileReadsFromTheRoot() {
	src, ok := s.m.readWorkspaceFile("a.go")
	s.Equal([2]any{riskBody, true}, [2]any{src, ok})
}

func (s *ChangeRiskWorkspaceSuite) TestSpellClaimingFindsTheBoundGoSpell() {
	sp := s.m.spellClaiming()("a.go")
	s.Require().NotNil(sp)
	s.Equal("go", sp.Name())
}

func (s *ChangeRiskWorkspaceSuite) TestEveryFunctionIsScoredOrAGap() {
	got, err := s.m.ChangeRisk(s.T().Context(), "")
	s.Require().NoError(err)

	var seen []string
	for _, f := range got.Functions {
		seen = append(seen, fmt.Sprintf("%s:%d", f.Path, f.Line))
	}
	for _, g := range got.Gaps {
		seen = append(seen, fmt.Sprintf("%s:%d", g.Path, g.Line))
	}
	s.Equal([3]any{riskDefinition, "", []string{"a.go:3"}}, [3]any{got.Definition, got.Base, seen})
}

func (s *ChangeRiskWorkspaceSuite) scoredLabels(base string) []string {
	got, err := s.m.ChangeRisk(s.T().Context(), base)
	s.Require().NoError(err)
	var labels []string
	for _, f := range got.Functions {
		labels = append(labels, f.Label)
	}
	return labels
}

func (s *ChangeRiskWorkspaceSuite) TestTheCoverageOpRunsBeforeScoring() {
	s.Equal([]string{"F"}, s.scoredLabels(""))
}

func (s *ChangeRiskWorkspaceSuite) TestAPrunedReportComesBackFromTheCache() {
	s.Require().Equal([]string{"F"}, s.scoredLabels(""))
	s.Require().NoError(os.RemoveAll(coverage.Dir(s.m.CacheDir(), ".")))

	s.Equal([]string{"F"}, s.scoredLabels(""))
}

func (s *ChangeRiskWorkspaceSuite) TestAReportThatDoesNotParseNamesItsFile() {
	s.keep("cover.out", "not a profile\n")

	_, reason := s.m.readCoverageReport(".", spells.Coverage{Format: spells.CoverageFormatGoProfile, File: "cover.out"})

	s.Contains(reason, "the coverage report for .: cover.out: ")
}

func (s *ChangeRiskWorkspaceSuite) TestABaseScoresOnlyWhatChangedSinceIt() {
	root := s.m.Root()
	gitRun(s.T(), root, "init", "-q")
	commitHistoryMetricsFiles(s.T(), root, "Ada", "2026-01-01T00:00:00Z", map[string]string{
		"a.go": riskBody + "\nfunc G() int {\n\treturn 2\n}\n",
	})
	s.Require().NoError(os.WriteFile(filepath.Join(root, "a.go"), []byte(riskBody+"\nfunc G() int {\n\treturn 3\n}\n"), 0o644))

	got, err := s.m.ChangeRisk(s.T().Context(), "HEAD")
	s.Require().NoError(err)

	var seen []string
	for _, g := range got.Gaps {
		seen = append(seen, g.Label)
	}
	for _, f := range got.Functions {
		seen = append(seen, f.Label)
	}
	s.Equal([2]any{"HEAD", []string{"G"}}, [2]any{got.Base, seen})
}

func (s *ChangeRiskWorkspaceSuite) TestAMovedFunctionIsNotTouched() {
	root := s.m.Root()
	gitRun(s.T(), root, "init", "-q")
	g := "func G() int {\n\treturn 2\n}\n"
	commitHistoryMetricsFiles(s.T(), root, "Ada", "2026-01-01T00:00:00Z", map[string]string{"a.go": fmt.Sprintf("%s\n%s", riskBody, g)})
	moved := fmt.Sprintf("package a\n\n%s\n%s", g, strings.TrimPrefix(riskBody, "package a\n\n"))
	s.Require().NoError(os.WriteFile(filepath.Join(root, "a.go"), []byte(moved), 0o644))

	got, err := s.m.ChangeRisk(s.T().Context(), "HEAD")
	s.Require().NoError(err)

	s.Equal([2]int{0, 0}, [2]int{len(got.Functions), len(got.Gaps)})
}

const riskPatch = "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -7,1 +7,1 @@\n-\treturn 1\n+\treturn 0\n"

func (s *ChangeRiskWorkspaceSuite) TestAttachChangeRiskChecksTheFunctionADiffChanged() {
	got, err := s.m.diff(s.T().Context(), []string{"a.go"}, diffConfig{patch: riskPatch, patchGiven: true})
	s.Require().NoError(err)

	var checks []string
	for _, f := range got.Files {
		for _, sym := range f.Symbols {
			for _, c := range sym.Checks {
				checks = append(checks, fmt.Sprintf("%s:%s", sym.Label, c.Name))
			}
		}
	}
	s.Equal([]string{"F:change-risk"}, checks)
}
