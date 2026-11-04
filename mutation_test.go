package magus

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/coverage"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/libs/testkit/register"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

const mutantFile = "calc/calc.toy"

// mutantSource is a toy language: `//` comments, `<` and `&&` the only swaps.
const mutantSource = `package calc

func Small(n) {
	return n < 3, n < 9
}

func Both(a, b) {
	// magus:mutant op=and-or reason=a and b are one flag
	return a && b
}
`

var toySyntax = spells.Syntax{
	Comments:  &spells.CommentSyntax{LineComments: []string{"//"}, Quotes: []spells.Quote{{Open: `"`, Close: `"`}}},
	Operators: []string{"<=", "&&", "||"},
	Mutations: &spells.MutationSyntax{Swaps: []spells.Swap{
		{Name: "lt-le", Find: "<", Replace: "<="},
		{Name: "and-or", Find: "&&", Replace: "||"},
	}},
}

// stubRunner judges the copy's file with verdict and records what it ran.
type stubRunner struct {
	mu      sync.Mutex
	verdict func(src string) bool
	hang    func(src string) bool
	broken  func(src string) bool
	cached  func(target string) bool
	err     error
	seen    []string
	targets []string
}

func (r *stubRunner) run(ctx context.Context, dir, target string, _ coverageScope) (testRun, error) {
	b, err := os.ReadFile(filepath.Join(dir, mutantFile))
	if err != nil {
		return testRun{}, err
	}
	r.mu.Lock()
	r.seen = append(r.seen, string(b))
	r.targets = append(r.targets, target)
	r.mu.Unlock()
	if string(b) == mutantSource {
		// A slow baseline keeps a fast mutant inside its timeout.
		time.Sleep(20 * time.Millisecond)
	}
	if r.broken != nil && r.broken(string(b)) {
		return testRun{}, fmt.Errorf("no result for %s, %w", target, errNoResult)
	}
	if r.hang != nil && r.hang(string(b)) {
		<-ctx.Done()
		return testRun{}, ctx.Err()
	}
	return testRun{passed: r.verdict(string(b)), cached: r.cached != nil && r.cached(target)}, r.err
}

// stubCopier writes the fixture into a fresh dir per call.
type stubCopier struct {
	mu     sync.Mutex
	base   string
	copies int
}

func (c *stubCopier) copy(context.Context) (string, error) {
	c.mu.Lock()
	c.copies++
	c.mu.Unlock()
	dir, err := os.MkdirTemp(c.base, "copy")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, mutantFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return dir, err
	}
	return dir, os.WriteFile(path, []byte(mutantSource), 0o644)
}

type MutantEngineSuite struct {
	suite.Suite
	root    string
	runner  *stubRunner
	copier  *stubCopier
	covered map[string][]int
	engine  mutationEngine
}

func TestMutantEngineSuite(t *testing.T) { suite.Run(t, new(MutantEngineSuite)) }

func (s *MutantEngineSuite) SetupTest() {
	s.root = s.T().TempDir()
	s.Require().NoError(os.MkdirAll(filepath.Join(s.root, "calc"), 0o755))
	s.Require().NoError(os.WriteFile(filepath.Join(s.root, mutantFile), []byte(mutantSource), 0o644))
	// The tests assert Small's first bound and nothing about Both.
	s.runner = &stubRunner{verdict: func(src string) bool { return !strings.Contains(src, "n <= 3") }}
	s.copier = &stubCopier{base: s.T().TempDir()}
	s.covered = map[string][]int{"Small": {4}, "Both": {9}}
	s.engine = mutationEngine{
		root: s.root, seats: 1, run: s.runner.run, copy: s.copier.copy,
		covered: func(_ string, sub mutationSubject) (map[int]bool, error) {
			lines := map[int]bool{}
			for _, l := range s.covered[sub.fn.Label] {
				lines[l] = true
			}
			return lines, nil
		},
		store:   newMutantStore(s.T().TempDir()),
		testKey: func(_ context.Context, sub mutationSubject) (string, error) { return "tk-" + sub.fn.Label, nil },
	}
}

func toySubject(label string, line, end int) mutationSubject {
	return mutationSubject{
		fn: touchedFunction{
			Symbol: "sym-" + label, Label: label, Path: mutantFile, Language: "toy",
			Digest: "d-" + label, Change: types.DiffChangeBody, Line: line, EndLine: end,
		},
		syntax:        toySyntax,
		coverageScope: coverageScope{project: ".", spell: "toy", coverage: spells.Coverage{Test: "test"}, scope: []string{"./calc"}},
	}
}

func toySmall() mutationSubject { return toySubject("Small", 3, 5) }
func toyBoth() mutationSubject  { return toySubject("Both", 7, 10) }

func smallMutant(ordinal, col int, outcome types.MutantOutcome) types.Mutant {
	return types.Mutant{
		ID: mutantID("d-Small", "lt-le", ordinal), Symbol: "sym-Small", Path: mutantFile, Operator: "lt-le",
		Find: "<", Replace: "<=", Line: 4, Col: col, Outcome: outcome,
	}
}

func (s *MutantEngineSuite) mutate(subjects ...mutationSubject) types.Mutants {
	got, err := s.engine.mutate(context.Background(), subjects)
	s.Require().NoError(err)
	for i := range got.Mutants {
		got.Mutants[i].Seconds = 0
	}
	return got
}

func (s *MutantEngineSuite) TestAFailingTestKillsAndAPassingOneSurvives() {
	s.Equal(types.Mutants{Mutants: []types.Mutant{
		smallMutant(0, 11, types.MutantKilled),
		smallMutant(1, 18, types.MutantSurvived),
	}}, s.mutate(toySmall()))
}

func (s *MutantEngineSuite) TestEachMutantRunsAloneOnTheRestoredFile() {
	s.mutate(toySmall())
	s.Len(s.runner.seen, 3)
	s.Equal(mutantSource, s.runner.seen[0], "the baseline runs unmutated")
	s.Contains(s.runner.seen[1], "return n <= 3, n < 9")
	s.Contains(s.runner.seen[2], "return n < 3, n <= 9")
}

func (s *MutantEngineSuite) TestTheBaselineIsTheCoverageOpAndMutantsMeetTheTestOp() {
	s.mutate(toySmall())
	s.Equal([]string{"toy::coverage", "toy::test", "toy::test"}, s.runner.targets)
}

func (s *MutantEngineSuite) TestABaselineReplayedFromTheCacheIsRefused() {
	s.runner.cached = func(target string) bool { return target == "toy::coverage" }
	_, err := s.engine.mutate(context.Background(), []mutationSubject{toySmall()})
	s.EqualError(err, "mutants: Small baseline: toy::coverage replayed from the cache, so it ran no test")
	s.Len(s.runner.seen, 1, "no mutant runs under a replayed baseline's timeout")
}

func (s *MutantEngineSuite) TestAMutantReplayedFromTheCacheIsRefused() {
	s.runner.cached = func(target string) bool { return target == "toy::test" }
	_, err := s.engine.mutate(context.Background(), []mutationSubject{toySmall()})
	s.ErrorContains(err, "toy::test replayed from the cache, so it ran no test")
	runs, err := s.engine.store.runs()
	s.Require().NoError(err)
	s.Empty(runs, "a replayed run is never kept")
}

func (s *MutantEngineSuite) TestARunThatReachedNoTestJudgesNothing() {
	s.runner.err = errors.New("no result for toy::test, so no test ran")
	_, err := s.engine.mutate(context.Background(), []mutationSubject{toySmall()})
	s.ErrorContains(err, "Small baseline: no result for toy::test")
}

func (s *MutantEngineSuite) TestAMutantThatRecordsNoResultAfterAGreenBaselineIsKilled() {
	s.runner.broken = func(src string) bool { return strings.Contains(src, "n <= 9") }
	got := s.mutate(toySmall())
	s.Equal([]types.Mutant{smallMutant(0, 11, types.MutantKilled), smallMutant(1, 18, types.MutantKilled)}, got.Mutants)
}

func (s *MutantEngineSuite) TestABaselineThatRecordsNoResultIsAnError() {
	s.runner.broken = func(src string) bool { return src == mutantSource }
	_, err := s.engine.mutate(context.Background(), []mutationSubject{toySmall()})
	s.ErrorIs(err, errNoResult)
}

func (s *MutantEngineSuite) TestBaseSurvivorsAreCountedOverTheBaseSpan() {
	// The base held Small one line lower; its second bound survives there too.
	s.Require().NoError(os.WriteFile(filepath.Join(s.root, mutantFile), []byte("\n"+mutantSource), 0o644))
	s.covered = map[string][]int{"Small": {5}}
	since := sinceBase{working: map[string][]changeset.Hunk{
		mutantFile: {{OldStart: 1, OldCount: 1, NewStart: 0, NewCount: 0, Rows: []changeset.Row{delRow(1)}}},
	}}

	got, err := s.engine.countBase(context.Background(), since, []mutationSubject{toySmall()})

	s.Require().NoError(err)
	s.Equal(map[string]baseCount{"sym-Small": {survived: 1}}, got)
}

func (s *MutantEngineSuite) TestANewFunctionHasNoBaseCount() {
	added := toySmall()
	added.fn.Change = types.DiffChangeAdded
	got, err := s.engine.countBase(context.Background(), sinceBase{}, []mutationSubject{added})
	s.Require().NoError(err)
	s.Equal(map[string]baseCount{"sym-Small": {reason: newSinceBase}}, got)
	s.Empty(s.runner.seen, "nothing ran")
}

func (s *MutantEngineSuite) TestBaseTestKeysMoveWithTheCommit() {
	a, err := baseTestKey("c1")(context.Background(), toySmall())
	s.Require().NoError(err)
	b, err := baseTestKey("c2")(context.Background(), toySmall())
	s.Require().NoError(err)
	s.NotEqual(a, b)
}

func (s *MutantEngineSuite) TestSurvivedNamesAFunctionWithASurvivor() {
	mutants := []types.Mutant{smallMutant(0, 11, types.MutantKilled), smallMutant(1, 18, types.MutantSurvived)}
	s.Equal([2]bool{true, false}, [2]bool{survived(mutants, "sym-Small"), survived(mutants, "sym-Both")})
	s.Equal(1, countSurvivors(mutants, "sym-Small"))
}

func (s *MutantEngineSuite) TestAMarkerExceptsTheSurvivorOnTheNextLine() {
	got := s.mutate(toyBoth())
	s.Empty(got.Unused)
	s.Equal([]types.Mutant{{
		ID: mutantID("d-Both", "and-or", 0), Symbol: "sym-Both", Path: mutantFile, Operator: "and-or",
		Find: "&&", Replace: "||", Line: 9, Col: 11, Outcome: types.MutantExcepted,
		Reason: "a and b are one flag",
	}}, got.Mutants)
}

func (s *MutantEngineSuite) TestAMarkerMatchingNoSurvivorIsUnused() {
	s.runner.verdict = func(src string) bool { return !strings.Contains(src, "||") }
	got := s.mutate(toyBoth())
	s.Equal(types.MutantKilled, got.Mutants[0].Outcome)
	s.Equal([]types.Finding{{
		Path: mutantFile, Line: 8, Col: 1, Rule: mutantRule,
		Message: "magus:mutant op=and-or matches no surviving mutant on its line or the next",
		Fix:     "delete the marker",
	}}, got.Unused)
}

func (s *MutantEngineSuite) TestAFailingBaselineIsAGapNeverAPass() {
	s.runner.verdict = func(string) bool { return false }
	s.Equal(types.Mutants{Gaps: []types.FunctionGap{{
		Symbol: "sym-Small", Label: "Small", Path: mutantFile, Language: "toy", Line: 3, Reason: failingBaseline,
	}}}, s.mutate(toySmall()))
	s.Len(s.runner.seen, 1, "no mutant runs past a red baseline")
}

func (s *MutantEngineSuite) TestABaselineThatLeftNoReportIsAGap() {
	s.engine.covered = func(string, mutationSubject) (map[int]bool, error) {
		return nil, errors.New("its coverage baseline left no report")
	}
	s.Equal(types.Mutants{Gaps: []types.FunctionGap{{
		Symbol: "sym-Small", Label: "Small", Path: mutantFile, Language: "toy", Line: 3,
		Reason: "its coverage baseline left no report",
	}}}, s.mutate(toySmall()))
}

func (s *MutantEngineSuite) TestAMutantThatOutrunsItsTimeoutIsKilled() {
	s.runner.hang = func(src string) bool { return strings.Contains(src, "n <= 9") }
	got := s.mutate(toySmall())
	s.Equal(types.MutantKilled, got.Mutants[1].Outcome)
}

func (s *MutantEngineSuite) TestARunnerThatCannotStartFails() {
	s.runner.err = errors.New("no magus")
	_, err := s.engine.mutate(context.Background(), []mutationSubject{toySmall()})
	s.ErrorContains(err, "Small baseline: no magus")
}

func (s *MutantEngineSuite) TestALineTheBaselineMissedYieldsNoMutant() {
	s.covered["Small"] = nil
	s.Equal(types.Mutants{}, s.mutate(toySmall()))
	s.Len(s.runner.seen, 1, "only the baseline runs")
}

func (s *MutantEngineSuite) TestAFunctionWithNoExtentIsAGap() {
	got := s.mutate(toySubject("Small", 3, 0))
	s.Equal([]types.FunctionGap{{
		Symbol: "sym-Small", Label: "Small", Path: mutantFile, Language: "toy", Line: 3,
		Reason: "its indexer emitted no extent, so its body cannot be cut",
	}}, got.Gaps)
	s.Zero(s.copier.copies, "nothing to run makes no copy")
}

func (s *MutantEngineSuite) TestARunIsKeptAndReplayedWithoutRunning() {
	first := s.mutate(toySmall())
	runs, err := s.engine.store.runs()
	s.Require().NoError(err)
	key := mutantKey(mutantID("d-Small", "lt-le", 0), "tk-Small")
	s.Equal(types.MutantKilled, runs[key].Outcome)
	s.Equal(s.root, runs[key].Checkout)

	s.runner.seen, s.copier.copies = nil, 0
	s.Equal(first, s.mutate(toySmall()))
	s.Empty(s.runner.seen)
	s.Zero(s.copier.copies)
}

func (s *MutantEngineSuite) TestAChangedTestKeyMissesTheKeptRun() {
	s.mutate(toySmall())
	s.engine.testKey = func(context.Context, mutationSubject) (string, error) { return "tk-new", nil }
	s.runner.seen = nil
	s.mutate(toySmall())
	s.Len(s.runner.seen, 3)
}

func (s *MutantEngineSuite) TestEachSeatTakesItsOwnCopy() {
	s.engine.seats = 4
	got := s.mutate(toySmall(), toyBoth())
	s.Len(got.Mutants, 3)
	s.Equal(2, s.copier.copies, "two subjects fill two of the four seats")
}

func (s *MutantEngineSuite) TestMovingAFunctionKeepsItsMutantIDs() {
	moved := toySubject("Small", 4, 6)
	src := "\n" + mutantSource
	before, err := planMutants(toySmall(), mutantSource, "tk")
	s.Require().NoError(err)
	after, err := planMutants(moved, src, "tk")
	s.Require().NoError(err)
	moved0, moved1 := smallMutant(0, 11, ""), smallMutant(1, 18, "")
	moved0.Line, moved1.Line = 5, 5
	key0, key1 := mutantKey(moved0.ID, "tk"), mutantKey(moved1.ID, "tk")
	s.Equal([]plannedMutant{
		{Mutant: moved0, key: key0, site: spells.MutationSite{Operator: "lt-le", Offset: 41, Line: 5, Col: 11, Find: "<", Replace: "<="}},
		{Mutant: moved1, key: key1, site: spells.MutationSite{Operator: "lt-le", Offset: 48, Line: 5, Col: 18, Find: "<", Replace: "<="}},
	}, after)
	s.Equal([]string{before[0].ID, before[1].ID}, []string{after[0].ID, after[1].ID})
}

func (s *MutantEngineSuite) TestAnEngineRunsThisMagusInCopiesOfTheWorkspace() {
	s.T().Setenv("XDG_CACHE_HOME", s.T().TempDir())
	root := s.T().TempDir()
	m := &Magus{ws: &types.Workspace{Root: root}}
	e, err := m.newMutationEngine(context.Background(), []string{"node_modules"})
	s.Require().NoError(err)
	store, err := config.StoreDir(root, m.cfg)
	s.Require().NoError(err)
	s.Equal(root, e.root)
	s.Equal(1, e.seats)
	s.Equal(filepath.Join(store, mutantStoreFile), e.store.l.Path)
	s.NotNil(e.run)
	s.NotNil(e.covered)
	s.NotNil(e.copy)
	s.NotNil(e.testKey)
}

func (s *MutantEngineSuite) TestABodyPastTheFileFails() {
	_, _, err := bodySpan("one\n", 3, 4)
	s.EqualError(err, "lines 3-4 are not in the file")
}

func (s *MutantEngineSuite) TestNoSubjectRunsNothing() {
	m := &Magus{ws: &types.Workspace{Root: s.T().TempDir()}}
	got, err := m.mutateSubjects(context.Background(), nil)
	s.Require().NoError(err)
	s.Equal(types.Mutants{}, got)
}

func (s *MutantEngineSuite) TestMutantsRefusesNoBase() {
	_, err := (&Magus{}).Mutants(context.Background(), "")
	s.EqualError(err, "mutants: name a base, since mutating every function of a workspace is not offered")
}

type MutantMarkerFindingSuite struct{ suite.Suite }

func TestMutantMarkerFindingSuite(t *testing.T) { suite.Run(t, new(MutantMarkerFindingSuite)) }

func (s *MutantMarkerFindingSuite) TestAMarkerWithNoReasonIsUnused() {
	src := strings.Replace(mutantSource, " reason=a and b are one flag", "", 1)
	p := &subjectPlan{subject: toyBoth(), src: src, mutants: []plannedMutant{
		{Mutant: types.Mutant{Operator: "and-or", Line: 9, Outcome: types.MutantSurvived}},
	}}
	got := exceptSurvivors(p)
	s.Equal([]types.Finding{{
		Path: mutantFile, Line: 8, Col: 1, Rule: mutantRule,
		Message: `magus:mutant op="and-or" carries no op or no reason`,
		Fix:     "name the swap and why its mutant is equivalent: // magus:mutant op=<swap> reason=<why>",
	}}, got)
	s.Equal(types.MutantSurvived, p.mutants[0].Outcome)
}

func (s *MutantMarkerFindingSuite) TestAMarkerOutsideTheFunctionIsNotJudged() {
	p := &subjectPlan{subject: toySmall(), src: mutantSource}
	s.Empty(exceptSurvivors(p))
}

// MutantSubjectScopeSuite pins how a test op's TestScope becomes a
// subject's scope.
type MutantSubjectScopeSuite struct {
	suite.Suite
	root string
}

func TestMutantSubjectScopeSuite(t *testing.T) { suite.Run(t, new(MutantSubjectScopeSuite)) }

func (s *MutantSubjectScopeSuite) SetupTest() {
	s.root = s.T().TempDir()
	s.Require().NoError(os.MkdirAll(filepath.Join(s.root, "web", "calc"), 0o755))
	s.Require().NoError(os.WriteFile(filepath.Join(s.root, "web", "calc", "calc.toy"), nil, 0o644))
}

func (s *MutantSubjectScopeSuite) TestAnEntryNamingNoFileIsDropped() {
	test := spells.Command{TestScope: []string{"./{dir}", "{dir}/{stem}_test.toy", "{file}"}}
	scope, reason := subjectScope(s.root, "web", "web/calc/calc.toy", test)
	s.Empty(reason)
	s.Equal([]string{"./calc", "calc/calc.toy"}, scope)
}

func (s *MutantSubjectScopeSuite) TestADeclaredScopeNamingNothingIsAReason() {
	test := spells.Command{TestScope: []string{"{dir}/{stem}_test.toy"}}
	scope, reason := subjectScope(s.root, "web", "web/calc/calc.toy", test)
	s.Nil(scope)
	s.Equal("no test_scope entry names a file for web/calc/calc.toy", reason)
}

func (s *MutantSubjectScopeSuite) TestNoDeclaredScopeRunsTheProject() {
	scope, reason := subjectScope(s.root, "web", "web/calc/calc.toy", spells.Command{})
	s.Empty(reason)
	s.Empty(scope)
}

func (s *MutantSubjectScopeSuite) TestTheRootProjectScopesByWorkspacePath() {
	test := spells.Command{TestScope: []string{"./{dir}"}}
	scope, reason := subjectScope(s.root, ".", "web/calc/calc.toy", test)
	s.Empty(reason)
	s.Equal([]string{"./web/calc"}, scope)
}

func (s *MutantSubjectScopeSuite) TestNoSpellIsAReason() {
	_, reason := newMutationSubject(s.root, touchedFunction{Path: "web/calc/calc.toy"}, "web", nil)
	s.Equal("no spell bound to web claims .toy files", reason)
}

func (s *MutantSubjectScopeSuite) TestLinksAreTheSpellsIgnoreDirsInEachProject() {
	a := mutationSubject{coverageScope: coverageScope{links: projectLinks("web", []string{"node_modules"})}}
	b := mutationSubject{coverageScope: coverageScope{links: projectLinks(".", []string{"node_modules", "vendor"})}}
	s.Equal([]string{"node_modules", "vendor", "web/node_modules"}, subjectLinks([]mutationSubject{a, b, a}))
}

// MutantCoverageSuite reads the report a baseline left in a scratch copy.
type MutantCoverageSuite struct{ suite.Suite }

func TestMutantCoverageSuite(t *testing.T) { suite.Run(t, new(MutantCoverageSuite)) }

func (s *MutantCoverageSuite) TestTheBaselinesHitLinesAreCovered() {
	dir := s.T().TempDir()
	reports := coverage.Dir(copyCacheDir(dir), ".")
	s.Require().NoError(os.MkdirAll(reports, 0o755))
	lcov := "SF:" + mutantFile + "\nDA:4,2\nDA:9,0\nend_of_record\n"
	s.Require().NoError(os.WriteFile(filepath.Join(reports, "lcov.info"), []byte(lcov), 0o644))
	sub := toySmall()
	sub.coverage = spells.Coverage{Format: spells.CoverageFormatLCOV, File: "lcov.info"}
	got, err := copyCoverage(dir, sub)
	s.Require().NoError(err)
	s.Equal(map[int]bool{4: true}, got)
}

func (s *MutantCoverageSuite) TestNoReportIsAnError() {
	sub := toySmall()
	sub.coverage = spells.Coverage{Format: spells.CoverageFormatLCOV, File: "lcov.info"}
	_, err := copyCoverage(s.T().TempDir(), sub)
	s.ErrorContains(err, "its coverage run left no report")
}

func (s *MutantCoverageSuite) TestTheCopysCacheDirIsPinnedInItsEnv() {
	s.Equal("MAGUS_CACHE_DIR="+filepath.Join("/copy", ".magus"), copyCacheEnv("/copy"))
}

// MutantRunnerSuite runs /bin/sh as the magus, so run names a script in dir.
type MutantRunnerSuite struct {
	suite.Suite
	dir string
}

func TestMutantRunnerSuite(t *testing.T) { suite.Run(t, new(MutantRunnerSuite)) }

func (s *MutantRunnerSuite) SetupTest() { s.dir = s.T().TempDir() }

func (s *MutantRunnerSuite) runWith(lines ...string) (testRun, error) {
	script := []byte(strings.Join(lines, "\n"))
	s.Require().NoError(os.WriteFile(filepath.Join(s.dir, "run"), script, 0o644))
	return magusRunner{bin: "/bin/sh"}.run(context.Background(), s.dir, "toy::test", toySmall().coverageScope)
}

func resultRecord(project, target, status string, hit bool) string {
	return fmt.Sprintf(`echo '{"schema":5,"type":"run.target.result","project":%q,"target":%q,"status":%q,"cache_hit":%t}'`,
		project, target, status, hit)
}

func (s *MutantRunnerSuite) TestARunBypassesTheCacheAndNamesTheSpell() {
	got, err := s.runWith(
		`[ "$*" = "toy::test --no-cache --no-volatility-retry -o jsonl . -- ./calc" ] || exit 2`,
		`case "$MAGUS_CACHE_DIR" in */.magus) ;; *) exit 2 ;; esac`,
		resultRecord(".", "test", "ok", false))
	s.Require().NoError(err)
	s.Equal(testRun{passed: true}, got)
}

func (s *MutantRunnerSuite) TestAFailedResultFails() {
	got, err := s.runWith(resultRecord(".", "test", "failed", false), "exit 1")
	s.Require().NoError(err)
	s.Equal(testRun{}, got)
}

func (s *MutantRunnerSuite) TestACachedResultSaysSo() {
	got, err := s.runWith(resultRecord(".", "test", "ok", true))
	s.Require().NoError(err)
	s.Equal(testRun{passed: true, cached: true}, got)
}

func (s *MutantRunnerSuite) TestAFailedRunThatReportsNoResultJudgesNothing() {
	_, err := s.runWith("echo 'workspace did not load' >&2", "exit 1")
	s.ErrorContains(err, "exited 1 with no result for toy::test, so no test ran: workspace did not load")
	s.ErrorIs(err, errNoResult)
}

func (s *MutantRunnerSuite) TestAnotherTargetsResultIsNotThisOnes() {
	_, err := s.runWith(resultRecord(".", "go-generate", "failed", false), resultRecord("web", "test", "failed", false), "exit 1")
	s.ErrorContains(err, "so no test ran")
}

func (s *MutantRunnerSuite) TestARunMagusCouldNotSeatJudgesNothing() {
	_, err := s.runWith("exit 75")
	s.ErrorContains(err, "exited 75")
}

type MutantTreeCopySuite struct{ suite.Suite }

func TestMutantTreeCopySuite(t *testing.T) { suite.Run(t, new(MutantTreeCopySuite)) }

// copyVCS answers the four calls treeCopier makes; any other call panics.
type copyVCS struct {
	types.VCSDriver
	repo  string
	head  map[string]string
	dirty []string
}

func (v copyVCS) FindCommit(context.Context, string, string) (types.Commit, error) {
	return types.Commit{ID: "c0ffee"}, nil
}

func (v copyVCS) ExportRevision(_ context.Context, _, rev, dst string) error {
	if rev != "c0ffee" {
		return errors.New("exported " + rev)
	}
	for rel, text := range v.head {
		if err := os.WriteFile(filepath.Join(dst, rel), []byte(text), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (v copyVCS) Root(context.Context, string) (string, error) { return v.repo, nil }

func (v copyVCS) DirtyFiles(context.Context, string, []string) ([]string, error) {
	return v.dirty, nil
}

func (s *MutantTreeCopySuite) TestACopyLandsInTheStoreWhenTheUserCacheIsDenied() {
	denied := filepath.Join(s.T().TempDir(), "denied")
	s.Require().NoError(os.Mkdir(denied, 0o500))
	s.T().Setenv("XDG_CACHE_HOME", denied)
	s.T().Setenv("HOME", denied)
	cfg := config.Defaults()
	cfg.Cache.Dir = filepath.Join(s.T().TempDir(), "granted")
	repo := testkit.NewRepository(s.T(), map[string]string{"calc.toy": "head"})
	s.Require().NoError(os.WriteFile(filepath.Join(repo.Root(), "calc.toy"), []byte("edit"), 0o644))
	m := &Magus{ws: &types.Workspace{Root: repo.Root()}, cfg: cfg}

	e, err := m.newMutationEngine(context.Background(), nil)
	s.Require().NoError(err)
	dir, err := e.copy(context.Background())
	if dir != "" {
		s.T().Cleanup(func() { _ = os.RemoveAll(dir) })
	}
	s.Require().NoError(err)

	store, err := config.StoreDir(repo.Root(), cfg)
	s.Require().NoError(err)
	s.Equal(filepath.Join(store, mutantTreesDir), filepath.Dir(dir))
	b, err := os.ReadFile(filepath.Join(dir, "calc.toy"))
	s.Require().NoError(err)
	s.Equal("edit", string(b))
	s.NoDirExists(filepath.Join(denied, "magus"))
}

func (s *MutantTreeCopySuite) TestACopyIsHeadWithTheWorkingTreesEdits() {
	repo := s.T().TempDir()
	root := filepath.Join(repo, "ws")
	s.Require().NoError(os.MkdirAll(filepath.Join(root, "node_modules"), 0o755))
	s.Require().NoError(os.WriteFile(filepath.Join(root, "edited.toy"), []byte("edit"), 0o644))
	c := treeCopier{root: root, trees: s.T().TempDir(), links: []string{"node_modules"}, vcs: copyVCS{
		repo: repo, dirty: []string{"ws/edited.toy", "ws/deleted.toy"},
		head: map[string]string{"edited.toy": "head", "deleted.toy": "head", "kept.toy": "head"},
	}}
	dir, err := c.copy(context.Background())
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = os.RemoveAll(dir) })
	entries, err := os.ReadDir(dir)
	s.Require().NoError(err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	s.Equal([]string{"edited.toy", "kept.toy", "node_modules"}, names)
	b, err := os.ReadFile(filepath.Join(dir, "edited.toy"))
	s.Require().NoError(err)
	s.Equal("edit", string(b))
}

func (s *MutantTreeCopySuite) TestACopyOfARevisionTakesNoEdits() {
	repo := s.T().TempDir()
	root := filepath.Join(repo, "ws")
	s.Require().NoError(os.MkdirAll(root, 0o755))
	s.Require().NoError(os.WriteFile(filepath.Join(root, "edited.toy"), []byte("edit"), 0o644))
	c := treeCopier{root: root, trees: s.T().TempDir(), rev: "c0ffee", vcs: copyVCS{
		repo: repo, dirty: []string{"ws/edited.toy"}, head: map[string]string{"edited.toy": "base"},
	}}
	dir, err := c.copy(context.Background())
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = os.RemoveAll(dir) })
	b, err := os.ReadFile(filepath.Join(dir, "edited.toy"))
	s.Require().NoError(err)
	s.Equal("base", string(b))
}

func (s *MutantTreeCopySuite) TestAnEditedFileIsCopiedAndADeletedOneRemoved() {
	root, dir := s.T().TempDir(), s.T().TempDir()
	s.Require().NoError(os.WriteFile(filepath.Join(root, "edited.toy"), []byte("new"), 0o640))
	s.Require().NoError(os.WriteFile(filepath.Join(dir, "gone.toy"), []byte("old"), 0o644))
	s.Require().NoError(overlayFile(root, dir, filepath.Join(root, "edited.toy")))
	s.Require().NoError(overlayFile(root, dir, filepath.Join(root, "gone.toy")))
	b, err := os.ReadFile(filepath.Join(dir, "edited.toy"))
	s.Require().NoError(err)
	s.Equal("new", string(b))
	_, err = os.Stat(filepath.Join(dir, "gone.toy"))
	s.ErrorIs(err, os.ErrNotExist)
}

func (s *MutantTreeCopySuite) TestAPathOutsideTheWorkspaceIsSkipped() {
	parent := s.T().TempDir()
	root, dir := filepath.Join(parent, "ws"), s.T().TempDir()
	s.Require().NoError(os.WriteFile(filepath.Join(parent, "outside.toy"), []byte("x"), 0o644))
	s.Require().NoError(overlayFile(root, dir, filepath.Join(parent, "outside.toy")))
	entries, err := os.ReadDir(dir)
	s.Require().NoError(err)
	s.Empty(entries)
}

func (s *MutantTreeCopySuite) TestALinkedDirPointsAtTheWorkspace() {
	root, dir := s.T().TempDir(), s.T().TempDir()
	s.Require().NoError(os.Mkdir(filepath.Join(root, "node_modules"), 0o755))
	s.Require().NoError(linkDirs(root, dir, []string{"node_modules", "vendor"}))
	target, err := os.Readlink(filepath.Join(dir, "node_modules"))
	s.Require().NoError(err)
	s.Equal(filepath.Join(root, "node_modules"), target)
	_, err = os.Lstat(filepath.Join(dir, "vendor"))
	s.ErrorIs(err, os.ErrNotExist)
}

func (s *MutantTreeCopySuite) TestATrackedDirIsKeptNotLinked() {
	root, dir := s.T().TempDir(), s.T().TempDir()
	s.Require().NoError(os.Mkdir(filepath.Join(root, "vendor"), 0o755))
	s.Require().NoError(os.Mkdir(filepath.Join(dir, "vendor"), 0o755))
	s.Require().NoError(linkDirs(root, dir, []string{"vendor"}))
	info, err := os.Lstat(filepath.Join(dir, "vendor"))
	s.Require().NoError(err)
	s.True(info.IsDir())
}

type MutantStoreSuite struct {
	suite.Suite
	dir   string
	store mutantStore
}

func TestMutantStoreSuite(t *testing.T) { suite.Run(t, new(MutantStoreSuite)) }

func (s *MutantStoreSuite) SetupTest() {
	s.dir = s.T().TempDir()
	s.store = newMutantStore(s.dir)
}

func (s *MutantStoreSuite) TestAnAbsentFileHoldsNoRuns() {
	runs, err := s.store.runs()
	s.Require().NoError(err)
	s.Empty(runs)
}

func (s *MutantStoreSuite) TestTheLastLinePerKeyWins() {
	first := types.MutantRun{Key: "k", ID: "a", Outcome: types.MutantSurvived, Seconds: 1, Checkout: "/w"}
	second := types.MutantRun{Key: "k", ID: "a", Outcome: types.MutantKilled, Seconds: 2, Checkout: "/w"}
	other := types.MutantRun{Key: "j", ID: "b", Outcome: types.MutantKilled, Seconds: 3, Checkout: "/w"}
	s.Require().NoError(s.store.record(context.Background(), []types.MutantRun{first, other}))
	s.Require().NoError(s.store.record(context.Background(), []types.MutantRun{second}))
	runs, err := s.store.runs()
	s.Require().NoError(err)
	s.Equal(map[string]types.MutantRun{"k": second, "j": other}, runs)
}

func (s *MutantStoreSuite) TestRecordingNothingWritesNoFile() {
	s.Require().NoError(s.store.record(context.Background(), nil))
	_, err := os.Stat(filepath.Join(s.dir, mutantStoreFile))
	s.ErrorIs(err, os.ErrNotExist)
}

func (s *MutantStoreSuite) TestAnUndecodableLineFails() {
	s.Require().NoError(os.WriteFile(filepath.Join(s.dir, mutantStoreFile), []byte(`{"key":"k"}`+"\n"), 0o600))
	_, err := s.store.runs()
	s.ErrorContains(err, "mutants.jsonl:1")
}

type ScopedTestKeySuite struct{ suite.Suite }

func TestScopedTestKeySuite(t *testing.T) { suite.Run(t, new(ScopedTestKeySuite)) }

func (s *ScopedTestKeySuite) TestSameNamedOpsOfTwoSpellsKeyApart() {
	first := register.Spell(s.T(), "zzz-key-first", spells.WithTargets("build"))
	second := register.Spell(s.T(), "zzz-key-second", spells.WithTargets("build"))
	w := testkit.NewWorkspace(s.T())
	w.Magusfile("")
	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell(first), WithSpell(second))
	m, err := Open(context.Background(), w.Root(), WithWorkspaceRegistry(reg), WithSpawnRule(TierCeiling))
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = m.Close() })

	keyOf := func(spell string) string {
		key, err := m.scopedTestKey(context.Background(), mutationSubject{coverageScope: coverageScope{project: ".", spell: spell, coverage: spells.Coverage{Test: "build"}, scope: []string{"./calc"}}})
		s.Require().NoError(err)
		return key
	}
	s.NotEqual(keyOf(first), keyOf(second))
}

func (s *MutantStoreSuite) TestTheKeyMovesWithTheTestKey() {
	s.NotEqual(mutantKey("id", "test-a"), mutantKey("id", "test-b"))
	s.Equal(mutantKey("id", "test-a"), mutantKey("id", "test-a"))
	s.Len(mutantKey("id", "test-a"), 64)
}
