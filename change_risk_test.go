package magus

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/coverage"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/libs/testkit"
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

func riskCoverage() spells.Coverage {
	return spells.Coverage{Format: spells.CoverageFormatGoProfile, Profile: ".magus/coverage.out"}
}

func (s *ChangeRiskSuite) SetupTest() {
	s.files = map[string]string{"lib/a.go": riskBody}
	cov := riskCoverage()
	s.spell = spells.NewSpell("go", spells.WithLanguageExtensions([]string{".go"}),
		spells.WithSyntax(riskSyntax()), spells.WithCoverage(&cov))
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
		report: func(touchedFunction, spells.Coverage) (keptReport, string) { return s.report, s.reason },
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
	cov := riskCoverage()
	s.spell = spells.NewSpell("go", spells.WithLanguageExtensions([]string{".go"}), spells.WithCoverage(&cov))
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
	s.reason = "lib holds no coverage profile at .magus/coverage.out: run the target that writes it"

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

func (s *ChangeRiskSuite) TestForwardLineKeepsAContextRowsLine() {
	moved, ok := forwardLine(6, parsedHunks("@@ -5,3 +5,4 @@\n a\n b\n+x\n c\n"))
	s.Equal([2]any{6, true}, [2]any{moved, ok})
}

func (s *ChangeRiskSuite) TestBackLineMapsANewLineToTheOldSide() {
	hunks := parsedHunks("@@ -5,3 +5,4 @@\n a\n b\n+x\n c\n")
	ctx, okCtx := backLine(8, hunks)
	_, okAdded := backLine(7, hunks)
	after, okAfter := backLine(20, hunks)
	s.Equal([5]any{7, true, false, 19, true}, [5]any{ctx, okCtx, okAdded, after, okAfter})
}

// shrunk is F at base on lines 3-9, a line of its body removed since.
const shrunk = "@@ -3,7 +3,6 @@\n func F() {\n \ta()\n-\tb()\n \tc()\n \td()\n \te()\n }\n"

func (s *ChangeRiskSuite) TestOldSpanOfAShrunkFunctionHoldsItsRemovedLines() {
	lo, hi, ok := oldSpan(3, 8, parsedHunks(shrunk))
	s.Equal([3]any{3, 9, true}, [3]any{lo, hi, ok})
}

func (s *ChangeRiskSuite) TestOldSpanTakesTheLinesARewrittenFirstLineReplaced() {
	lo, hi, ok := oldSpan(3, 4, parsedHunks("@@ -2,3 +2,3 @@\n x\n-func F(a) {\n+func F(a, b) {\n }\n"))
	s.Equal([3]any{3, 4, true}, [3]any{lo, hi, ok})
}

func (s *ChangeRiskSuite) TestOldSpanLeavesAFunctionRemovedAboveToItself() {
	lo, hi, ok := oldSpan(2, 3, parsedHunks("@@ -1,5 +1,3 @@\n x\n-func G() {\n-}\n func F() {\n }\n"))
	s.Equal([3]any{4, 5, true}, [3]any{lo, hi, ok})
}

func (s *ChangeRiskSuite) TestOldSpanOfAnAddedFunctionIsNone() {
	_, _, ok := oldSpan(1, 2, parsedHunks("@@ -0,0 +1,2 @@\n+func F() {\n+}\n"))
	s.False(ok)
}

func (s *ChangeRiskSuite) TestBaseSpanGoesThroughTheWorkingTreeThenTheCommits() {
	since := sinceBase{
		working:   map[string][]changeset.Hunk{"a.go": parsedHunks("@@ -0,0 +1,1 @@\n+// top\n")},
		committed: map[string][]changeset.Hunk{"a.go": parsedHunks(shrunk)},
	}
	fn := scoredFunction()
	fn.Path, fn.Line, fn.EndLine = "a.go", 4, 9
	lo, hi, ok := since.baseSpan(fn)
	s.Equal([3]any{3, 9, true}, [3]any{lo, hi, ok})
	at, kept := since.headLine("a.go", 9)
	s.Equal([2]any{9, true}, [2]any{at, kept})
}

func (s *ChangeRiskSuite) TestAnAddedFunctionHasNoBaseSpan() {
	fn := scoredFunction()
	fn.Change = types.DiffChangeAdded
	_, _, ok := sinceBase{}.baseSpan(fn)
	s.False(ok)
}

func parsedHunks(body string) []changeset.Hunk {
	files := changeset.Parse("diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n" + body)
	return files[0].Hunks
}

func delRow(old int) changeset.Row {
	return changeset.Row{Kind: changeset.KindDel, OldLine: &old}
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

	read("lib", riskCoverage())
	got, reason := read("lib", riskCoverage())

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

// riskMagusfile's test target declares the Go spell's profile, and cover
// writes outProfile.
const riskMagusfile = `import "magus";
import "fs";
export fun test(ctx: magus\Context, args: [str]) > void !> any {
    ctx.writesFiles([".magus/coverage*.out"]);
}
export fun cover(ctx: magus\Context, args: [str]) > void !> any {
    ctx.writesFiles(["cover.out"]);
    fs\writeFile("cover.out", "mode: set\nexample.com/m/a.go:3.20,4.10 1 1\n");
}
export fun red(ctx: magus\Context, args: [str]) > void !> any {
    throw "a test failed";
}
`

func outSource() coverageSource {
	return coverageSource{project: ".", spell: "go", target: "cover",
		coverage: spells.Coverage{Format: spells.CoverageFormatGoProfile, Profile: "cover.out"}}
}

func (s *ChangeRiskWorkspaceSuite) SetupTest() {
	root := writeWorkspace(s.T(), map[string]string{
		"magusfile.buzz": riskMagusfile,
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
	file := filepath.Join(s.m.Root(), filepath.FromSlash(name))
	s.Require().NoError(os.MkdirAll(filepath.Dir(file), 0o755))
	s.Require().NoError(os.WriteFile(file, []byte(report), 0o644))
}

func goProfile() spells.Coverage {
	return spells.Coverage{Format: spells.CoverageFormatGoProfile, Profile: ".magus/coverage.out"}
}

func (s *ChangeRiskWorkspaceSuite) TestReadsTheGoProfileInTheTree() {
	s.keep(".magus/coverage.out", "mode: set\nexample.com/m/a.go:3.20,4.10 1 1\n")

	got, reason := s.m.readCoverageReport(".", goProfile())

	s.Empty(reason)
	s.Equal([]coverage.File{{Path: "a.go", Covered: 1, Total: 1, Blocks: []coverage.Block{{StartLine: 3, EndLine: 4, Units: 1, Hits: 1}}}}, got.files)
}

func (s *ChangeRiskWorkspaceSuite) TestReadsAnLCOVTracefileInTheTree() {
	s.keep("cov/lcov.info", "SF:a.go\nDA:5,0\nend_of_record\n")

	got, reason := s.m.readCoverageReport(".", spells.Coverage{Format: spells.CoverageFormatLCOV, Profile: "cov/lcov.info"})

	s.Empty(reason)
	s.Equal([]coverage.File{{Path: "a.go", Total: 1, Blocks: []coverage.Block{{StartLine: 5, EndLine: 5, Units: 1}}}}, got.files)
}

func (s *ChangeRiskWorkspaceSuite) TestNoProfileNamesWhatIsMissing() {
	_, reason := s.m.readCoverageReport(".", goProfile())

	s.Equal(". holds no coverage profile at .magus/coverage.out: run the target that writes it", reason)
}

func (s *ChangeRiskWorkspaceSuite) TestAGoProfileOutsideAnyModuleIsRefused() {
	s.Require().NoError(os.Remove(filepath.Join(s.m.Root(), "go.mod")))
	s.keep(".magus/coverage.out", "mode: set\n")

	_, reason := s.m.readCoverageReport(".", goProfile())

	s.Equal("the coverage profile of .: no go.mod at or above . names the module its profile's paths are in", reason)
}

func (s *ChangeRiskWorkspaceSuite) TestAProfileThatDoesNotParseNamesItsPath() {
	s.keep(".magus/coverage.out", "not a profile\n")

	_, reason := s.m.readCoverageReport(".", goProfile())

	s.Contains(reason, "the coverage profile of .: .magus/coverage.out: ")
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

func (s *ChangeRiskWorkspaceSuite) TestTheSourceIsTheTargetDeclaringTheProfile() {
	c, reason := s.m.coverageSource(".", "a.go", s.m.spellClaiming()("a.go"))

	s.Empty(reason)
	s.Equal(coverageSource{project: ".", spell: "go", target: "test", coverage: goProfile()}, c)
}

func (s *ChangeRiskWorkspaceSuite) touchedLabels(base string) []string {
	set, err := s.m.touchedSince(s.T().Context(), base)
	s.Require().NoError(err)
	var labels []string
	for _, fn := range set.fns {
		labels = append(labels, fn.Label)
	}
	return labels
}

func (s *ChangeRiskWorkspaceSuite) TestNoBaseTouchesEveryFunction() {
	s.Equal([]string{"F"}, s.touchedLabels(""))
}

func (s *ChangeRiskWorkspaceSuite) TestABaseScoresOnlyWhatChangedSinceIt() {
	root := s.m.Root()
	gitRun(s.T(), root, "init", "-q")
	commitHistoryMetricsFiles(s.T(), root, "Ada", "2026-01-01T00:00:00Z", map[string]string{
		"a.go": riskBody + "\nfunc G() int {\n\treturn 2\n}\n",
	})
	s.Require().NoError(os.WriteFile(filepath.Join(root, "a.go"), []byte(riskBody+"\nfunc G() int {\n\treturn 3\n}\n"), 0o644))

	set, err := s.m.touchedSince(s.T().Context(), "HEAD")
	s.Require().NoError(err)

	s.Equal([]string{"G"}, s.labelsTouched(set.fns))
	s.Equal([]string{"a.go"}, set.changed)
	lo, hi, ok := set.since.baseSpan(set.fns[0])
	s.Equal([3]any{10, 12, true}, [3]any{lo, hi, ok})
}

func (s *ChangeRiskWorkspaceSuite) labelsTouched(fns []touchedFunction) []string {
	var labels []string
	for _, fn := range fns {
		labels = append(labels, fn.Label)
	}
	return labels
}

func (s *ChangeRiskWorkspaceSuite) TestAMovedFunctionIsNotTouched() {
	root := s.m.Root()
	gitRun(s.T(), root, "init", "-q")
	g := "func G() int {\n\treturn 2\n}\n"
	commitHistoryMetricsFiles(s.T(), root, "Ada", "2026-01-01T00:00:00Z", map[string]string{"a.go": fmt.Sprintf("%s\n%s", riskBody, g)})
	moved := fmt.Sprintf("package a\n\n%s\n%s", g, strings.TrimPrefix(riskBody, "package a\n\n"))
	s.Require().NoError(os.WriteFile(filepath.Join(root, "a.go"), []byte(moved), 0o644))

	set, err := s.m.touchedSince(s.T().Context(), "HEAD")
	s.Require().NoError(err)

	s.Empty(set.fns)
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

// ProfileFreshenSuite runs a magusfile target that writes a profile, in a
// workspace whose projects magus discovers.
type ProfileFreshenSuite struct {
	suite.Suite
	m *Magus
}

func TestProfileFreshenSuite(t *testing.T) { suite.Run(t, new(ProfileFreshenSuite)) }

func (s *ProfileFreshenSuite) SetupTest() {
	w := testkit.NewWorkspace(s.T())
	w.Magusfile(riskMagusfile)
	w.Write("go.mod", "module example.com/m\n\ngo 1.25\n")
	w.Write("a.go", riskBody)
	m, err := Open(s.T().Context(), w.Root(), WithSpawnRule(TierCeiling))
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = m.Close() })
	s.m = m
}

func (s *ProfileFreshenSuite) TestTheHeadProfileIsWhatItsTargetWrote() {
	got, err := s.m.headProfile(s.T().Context(), outSource())

	s.Require().NoError(err)
	s.Equal(profileRead{files: []coverage.File{{Path: "a.go", Covered: 1, Total: 1,
		Blocks: []coverage.Block{{StartLine: 3, EndLine: 4, Units: 1, Hits: 1}}}}}, got)
}

func (s *ProfileFreshenSuite) TestAnEditedProfileIsNotCurrentAndRunsTheTargetAgain() {
	_, err := s.m.headProfile(s.T().Context(), outSource())
	s.Require().NoError(err)
	file := filepath.Join(s.m.Root(), "cover.out")
	s.Require().NoError(os.WriteFile(file, []byte("mode: set\n"), 0o644))

	got, err := s.m.headProfile(s.T().Context(), outSource())

	s.Require().NoError(err)
	s.Len(got.files, 1, "the target ran again and wrote its profile back")
}

func (s *ProfileFreshenSuite) TestAFailingTargetIsAReason() {
	c := outSource()
	c.target = "red"
	got, err := s.m.headProfile(s.T().Context(), c)

	s.Require().NoError(err)
	s.Contains(got.reason, "`magus run red .` left no current profile: ")
}

type ProfileTargetSuite struct{ suite.Suite }

func TestProfileTargetSuite(t *testing.T) { suite.Run(t, new(ProfileTargetSuite)) }

func outputsOf(targets map[string]string) *types.Project {
	p := &types.Project{Path: "lib", TargetOutputs: map[string][]types.OutputRef{}}
	for name, glob := range targets {
		p.TargetOutputs[name] = []types.OutputRef{{Glob: glob}}
	}
	return p
}

func (s *ProfileTargetSuite) TestTheOneTargetDeclaringTheProfileWritesIt() {
	target, reason := profileTarget(outputsOf(map[string]string{"test": "out/*.lcov", "build": "dist/**"}), "out/a.lcov")
	s.Equal([2]string{"test", ""}, [2]string{target, reason})
}

func (s *ProfileTargetSuite) TestNoTargetDeclaringTheProfileIsAReason() {
	_, reason := profileTarget(outputsOf(map[string]string{"build": "dist/**"}), "out/a.lcov")
	s.Equal("no target of lib declares its coverage profile out/a.lcov as an output", reason)
}

func (s *ProfileTargetSuite) TestTwoTargetsDeclaringTheProfileIsAReason() {
	_, reason := profileTarget(outputsOf(map[string]string{"test": "out/**", "cover": "out/a.lcov"}), "out/a.lcov")
	s.Equal("targets cover, test of lib each declare its coverage profile out/a.lcov, so which run wrote it is unknown", reason)
}

func (s *ProfileTargetSuite) TestASourceNamesWhatTheSpellLacks() {
	m := &Magus{}
	_, none := m.coverageSource("lib", "lib/a.go", nil)
	_, noCoverage := m.coverageSource("lib", "lib/a.go", spells.NewSpell("go"))
	s.Equal([2]string{"no spell bound to lib claims .go files", "spell go declares no coverage (mgs_getCoverage)"}, [2]string{none, noCoverage})
}

// riskBaseBody is F as the base held it, on lines 3 to 11: one if more, so
// complexity 4.
const riskBaseBody = "package a\n\n" +
	"func F(x int) int {\n" +
	"\tif x > 0 && x < 9 {\n" +
	"\t\treturn 1 // if\n" +
	"\t}\n" +
	"\tif x == 5 {\n" +
	"\t\treturn 5\n" +
	"\t}\n" +
	"\treturn 0\n" +
	"}\n"

// riskShrink removes the second if, base to HEAD.
const riskShrink = "@@ -6,6 +6,3 @@\n \t}\n-\tif x == 5 {\n-\t\treturn 5\n-\t}\n \treturn 0\n }\n"

// RiskMeterSuite measures with stub profile reads.
type RiskMeterSuite struct {
	suite.Suite
	scorer ChangeRiskSuite
	meter  riskMeter
	// mu guards read: head and base read at once.
	mu sync.Mutex
	// files and failed are each side's profile, keyed "head" and "base".
	files  map[string][]coverage.File
	failed map[string]bool
	read   []string
	// baseFile is where the base holds the file, when it was renamed.
	baseFile string
	readErr  error
}

func TestRiskMeterSuite(t *testing.T) { suite.Run(t, new(RiskMeterSuite)) }

func riskSource() coverageSource {
	return coverageSource{project: "lib", spell: "go", target: "test", coverage: riskCoverage()}
}

func (s *RiskMeterSuite) SetupTest() {
	s.scorer.SetT(s.T())
	s.scorer.SetupTest()
	s.failed, s.read, s.baseFile, s.readErr = map[string]bool{}, nil, "", nil
	s.files = map[string][]coverage.File{
		"head": {{Path: "lib/a.go", Blocks: []coverage.Block{
			{StartLine: 3, EndLine: 4, Units: 1, Hits: 1}, {StartLine: 5, EndLine: 5, Units: 1}, {StartLine: 7, EndLine: 7, Units: 2, Hits: 1},
		}}},
		"base": {{Path: "lib/a.go", Blocks: []coverage.Block{
			{StartLine: 3, EndLine: 4, Units: 1}, {StartLine: 8, EndLine: 8, Units: 1}, {StartLine: 10, EndLine: 10, Units: 1},
		}}},
	}
	s.meter = riskMeter{
		scorer: s.scorer.scorer(),
		source: func(string) (coverageSource, string) { return riskSource(), "" },
		covers: func(string) bool { return true },
		head:   s.side("head"),
		atBase: s.side("base"),
		readAt: func(p string) (string, bool) {
			if p != cmp.Or(s.baseFile, "lib/a.go") {
				return "", false
			}
			return riskBaseBody, true
		},
		done:   func() {},
		commit: "base",
		since: sinceBase{
			lines:     map[string][]int{"lib/a.go": {5}},
			committed: map[string][]changeset.Hunk{"lib/a.go": parsedHunks(riskShrink)},
		},
		changed: []string{"lib/a.go"},
	}
}

func (s *RiskMeterSuite) side(name string) func(context.Context, coverageSource) (profileRead, error) {
	return func(_ context.Context, c coverageSource) (profileRead, error) {
		s.mu.Lock()
		s.read = append(s.read, fmt.Sprintf("%s %s", name, c.command()))
		s.mu.Unlock()
		if s.readErr != nil {
			return profileRead{}, s.readErr
		}
		if s.failed[name] {
			return profileRead{reason: fmt.Sprintf("a test fails at %s: `%s`", name, c.command())}, nil
		}
		return profileRead{files: s.files[name]}, nil
	}
}

// readSorted is what was read, in order: head and base read at once.
func (s *RiskMeterSuite) readSorted() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Sorted(slices.Values(s.read))
}

func (s *RiskMeterSuite) measure() types.ChangeRisk {
	got, err := s.meter.measure(context.Background(), []touchedFunction{scoredFunction()})
	s.Require().NoError(err)
	return got
}

func (s *RiskMeterSuite) TestAShrunkFunctionIsScoredAgainstItselfAtBase() {
	got := s.measure()

	s.Require().Len(got.Functions, 1)
	f := got.Functions[0]
	s.Equal([5]any{"", 4, 0, 3, 20.0}, [5]any{f.BaseReason, f.BaseComplexity, f.BaseCovered, f.BaseCoverable, f.BaseScore})
	s.False(f.Charged(3), "past 3 at head, but better than at base")
	s.Equal([]string{"base magus run test lib", "head magus run test lib"}, s.readSorted())
}

func (s *RiskMeterSuite) TestAFunctionWorseThanAtBaseIsCharged() {
	s.files["base"] = []coverage.File{{Path: "lib/a.go", Blocks: []coverage.Block{
		{StartLine: 3, EndLine: 11, Units: 1, Hits: 1},
	}}}
	s.files["head"] = []coverage.File{{Path: "lib/a.go", Blocks: []coverage.Block{{StartLine: 3, EndLine: 8, Units: 2}}}}
	f := s.measure().Functions[0]
	s.Equal([2]float64{12, 4}, [2]float64{f.Score, f.BaseScore})
	s.True(f.Charged(3))
}

func (s *RiskMeterSuite) TestANewFunctionHasNoBase() {
	fn := scoredFunction()
	fn.Change = types.DiffChangeAdded
	got, err := s.meter.measure(context.Background(), []touchedFunction{fn})
	s.Require().NoError(err)
	s.Equal(newSinceBase, got.Functions[0].BaseReason)
}

func (s *RiskMeterSuite) TestNoCommitJudgesNoBase() {
	s.meter.commit = ""
	got := s.measure()
	s.Equal([3]any{noBase, []types.Finding(nil), []string{"head magus run test lib"}}, [3]any{got.Functions[0].BaseReason, got.Lines, s.read})
}

func (s *RiskMeterSuite) TestARedHeadIsAGapWithTheReason() {
	s.failed["head"] = true
	got := s.measure()
	reason := "a test fails at head: `magus run test lib`"
	s.Equal(types.ChangeRisk{
		Definition: riskDefinition,
		Gaps:       []types.FunctionGap{{Symbol: "sym F", Label: "F", Path: "lib/a.go", Language: "go", Line: 3, Complexity: 3, Reason: reason}},
		Lines: []types.Finding{{Path: "lib/a.go", Line: 1,
			Message: "the coverage of lib/a.go was not measured: " + reason,
			Fix:     "Make its project's tests pass, so its covered lines can be compared."}},
	}, got)
}

func (s *RiskMeterSuite) TestARedBaseLeavesTheHeadScoreAndSaysWhy() {
	s.failed["base"] = true
	f := s.measure().Functions[0]
	s.Equal("its base is unmeasured: a test fails at base: `magus run test lib`", f.BaseReason)
	s.True(f.Charged(3), "no base measure: the threshold alone judges")
}

func (s *RiskMeterSuite) TestAKeptLineThatLostCoverageAndAnUncoveredAddedLineAreFindings() {
	// Base line 10 maps to head line 7; base line 4 to head line 4.
	s.files["base"] = []coverage.File{{Path: "lib/a.go", Blocks: []coverage.Block{
		{StartLine: 4, EndLine: 4, Units: 1, Hits: 1}, {StartLine: 8, EndLine: 8, Units: 1, Hits: 1},
	}}}
	s.files["head"] = []coverage.File{{Path: "lib/a.go", Blocks: []coverage.Block{
		{StartLine: 4, EndLine: 5, Units: 1},
	}}}
	got := s.measure()
	s.Equal([]types.Finding{
		{Path: "lib/a.go", Line: 4, Message: "lib/a.go:4 was covered at base and no test of its package covers it now",
			Fix: "Restore the test that covered it, or delete the code no test needs."},
		{Path: "lib/a.go", Line: 5, Message: "lib/a.go:5 was added and no test of its package covers it",
			Fix: "Cover the added lines with a test."},
	}, got.Lines)
}

func (s *RiskMeterSuite) TestRemovedCoveredLinesAreNeverFindings() {
	s.files["base"] = []coverage.File{{Path: "lib/a.go", Blocks: []coverage.Block{{StartLine: 7, EndLine: 9, Units: 3, Hits: 1}}}}
	s.meter.since.lines = nil
	s.Empty(s.measure().Lines)
}

func (s *RiskMeterSuite) TestLineFindingsJoinConsecutiveLines() {
	got := lineFindings("a.go", []int{9, 3, 4, 5, 9}, "x", "f")
	s.Equal([]types.Finding{
		{Path: "a.go", Line: 3, Message: "a.go:3-5 x", Fix: "f"},
		{Path: "a.go", Line: 9, Message: "a.go:9 x", Fix: "f"},
	}, got)
}

func (s *RiskMeterSuite) TestAReadThatFailsIsAnErrorNotAGap() {
	s.readErr = fmt.Errorf("change risk: %w", errNoSeat)
	_, err := s.meter.measure(context.Background(), []touchedFunction{scoredFunction()})
	s.ErrorIs(err, errNoSeat)
}

func (s *RiskMeterSuite) TestAChangedFileWithNoSourceIsReported() {
	s.meter.source = func(p string) (coverageSource, string) {
		if p == "lib/b.go" {
			return coverageSource{}, "no target of lib declares its coverage profile .magus/coverage.out as an output"
		}
		return riskSource(), ""
	}
	s.meter.changed = []string{"lib/b.go"}
	got := s.measure()
	s.Equal([]types.Finding{{Path: "lib/b.go", Line: 1,
		Message: "the coverage of lib/b.go was not compared: no target of lib declares its coverage profile .magus/coverage.out as an output",
		Fix:     "Declare the coverage profile its spell names as an output of the target that writes it."}}, got.Lines)
}

func (s *RiskMeterSuite) TestFilesOfOneProjectShareOneRead() {
	s.meter.changed = []string{"lib/a.go", "lib/b/b.go", "lib/c/c.go"}
	s.measure()
	s.Equal([]string{"base magus run test lib", "head magus run test lib"}, s.readSorted())
}

func (s *RiskMeterSuite) TestARenamedFileIsReadAndComparedAtItsOldPath() {
	s.baseFile = "lib/old.go"
	s.files["base"] = []coverage.File{{Path: "lib/old.go", Blocks: s.files["base"][0].Blocks}}
	s.meter.since.committedOld = map[string]string{"lib/a.go": "lib/old.go"}

	got := s.measure()

	f := got.Functions[0]
	s.Equal([5]any{"", 4, 0, 3, 20.0}, [5]any{f.BaseReason, f.BaseComplexity, f.BaseCovered, f.BaseCoverable, f.BaseScore})
	for _, line := range got.Lines {
		s.NotContains(line.Message, "not measured")
	}
}

func (s *RiskMeterSuite) TestABasePathFollowsRenamesThroughBothPatches() {
	rename := func(from, to string) string {
		return fmt.Sprintf("diff --git a/%[1]s b/%[2]s\nsimilarity index 90%%\nrename from %[1]s\nrename to %[2]s\n", from, to)
	}
	since := sinceBase{committedOld: renamedPaths(rename("one.go", "two.go")), workingOld: renamedPaths(rename("two.go", "three.go"))}
	s.Equal([3]string{"one.go", "two.go", "plain.go"}, [3]string{since.basePath("three.go"), since.headPath("three.go"), since.basePath("plain.go")})
}
