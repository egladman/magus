package job

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// Existing selector/parser tests use a declared Go workspace instead of target
// spelling as their premise. The fixture's names are not production dispatch rules.
func goCheckWorkspace(project string) Workspace {
	if project == "" {
		project = "."
	}
	goSpell := spells.NewSpell("go", spells.WithTargets("go-test", "go-vet", "lint", "format"), spells.WithOps(map[string]spells.Op{
		"go-test": {Command: spells.Command{TestSelection: "go"}},
		"go-vet":  {}, "lint": {}, "format": {},
	}))
	return Workspace{Projects: []*types.Project{{Path: project, ResolvedSpells: []*spells.Spell{goSpell},
		MagusfileTargets: []string{"test"}, DispatchOnlyTargets: []string{"test"},
		TargetSpellOps: map[string][]types.TargetSpellUse{"test": {{Spell: "go", Ops: []string{"go-test"}}}},
	}}}
}
func goCheckSelection(check types.LeaseCheck) (string, bool) {
	return resolvedTestSelection(goCheckWorkspace(check.Project), check)
}

type testSelectionSuite struct{ suite.Suite }

func TestTestSelection(t *testing.T) { suite.Run(t, new(testSelectionSuite)) }

func (s *testSelectionSuite) TestNonGoTargetNamedTestDoesNotReadGoSelectors() {
	ws := goCheckWorkspace(".")
	ws.Projects[0].ResolvedSpells = []*spells.Spell{spells.NewSpell("node", spells.WithOps(map[string]spells.Op{"unit": {}}))}
	ws.Projects[0].TargetSpellOps["test"] = []types.TargetSpellUse{{Spell: "node", Ops: []string{"unit"}}}
	dialect, known := resolvedTestSelection(ws, types.LeaseCheck{Target: "test", Project: "."})
	s.True(known)
	s.Empty(dialect)
	root := testRoot(s.T()).Root()
	store := tempStore(s.T(), root).InWorkspace(ws)
	s.Empty(narrowedTestProblems(store, types.Job{ID: "node", WritePaths: []string{"pkg/a.go"}, Check: &types.LeaseCheck{Target: "test", Project: ".", Args: []string{"-run", "Alpha"}}}))
}
func (s *testSelectionSuite) TestRenamedWrapperInheritsDeclaredDialect() {
	ws := goCheckWorkspace(".")
	p := ws.Projects[0]
	p.MagusfileTargets = []string{"check-cases"}
	p.DispatchOnlyTargets = []string{"check-cases"}
	p.TargetSpellOps = map[string][]types.TargetSpellUse{"check-cases": {{Spell: "go", Ops: []string{"go-test"}}}}
	root := testRoot(s.T()).Root()
	store := tempStore(s.T(), root).InWorkspace(ws)
	problems := narrowedTestProblems(store, types.Job{ID: "renamed", WritePaths: []string{"pkg/a.go"}, Check: &types.LeaseCheck{Target: "check-cases", Project: ".", Args: []string{"-run", "Alpha"}}})
	s.Require().Len(problems, 1)
	s.Contains(problems[0].Message, "TestBeta")
}
func (s *testSelectionSuite) TestQualifiedOpReadsMetadataDespiteShadowing() {
	ws := goCheckWorkspace(".")
	ws.Projects[0].MagusfileTargets = append(ws.Projects[0].MagusfileTargets, "go-test")
	dialect, known := resolvedTestSelection(ws, types.LeaseCheck{Target: "go::go-test", Project: "."})
	s.True(known)
	s.Equal("go", dialect)
}
func (s *testSelectionSuite) TestRawRenamedOpReadsMetadata() {
	ws := goCheckWorkspace(".")
	ws.Projects[0].ResolvedSpells = []*spells.Spell{spells.NewSpell("cases", spells.WithOps(map[string]spells.Op{"exercise": {Command: spells.Command{TestSelection: "go"}}}))}
	dialect, known := resolvedTestSelection(ws, types.LeaseCheck{Target: "exercise", Project: "."})
	s.True(known)
	s.Equal("go", dialect)
}
func (s *testSelectionSuite) TestCharmDoesNotChangeDialect() {
	dialect, known := resolvedTestSelection(goCheckWorkspace("."), types.LeaseCheck{Target: "test:extended,rw", Project: "."})
	s.True(known)
	s.Equal("go", dialect)
}
func (s *testSelectionSuite) TestScriptIsNeverATargetSelector() {
	dialect, known := resolvedTestSelection(Workspace{}, types.LeaseCheck{Script: "test.buzz", Target: "test", Args: []string{"-run", "Alpha"}})
	s.True(known)
	s.Empty(dialect)
}
func (s *testSelectionSuite) TestExplicitArgsWrapperIsUnknown() {
	ws := goCheckWorkspace(".")
	ws.Projects[0].DispatchOnlyTargets = nil
	check := types.LeaseCheck{Target: "test", Project: ".", Args: []string{"-run", "Alpha"}}
	dialect, known := resolvedTestSelection(ws, check)
	s.False(known)
	s.Empty(dialect)
	notes := uncheckedTestSelection(ws, types.Job{Check: &check}, nil)
	s.Require().Len(notes, 1)
	s.Contains(notes[0], "recorded evidence arguments must match exactly")
	selection := tempStore(s.T(), s.T().TempDir()).InWorkspace(ws).testSelection()
	s.False(scopedRun(check, types.JobAttempt{ExtraArgs: []string{"-run", "Beta"}}, selection))
}
func (s *testSelectionSuite) TestForwardingTargetWithNeedsReadsDeclaredDialect() {
	ws := goCheckWorkspace(".")
	p := ws.Projects[0]
	p.MagusfileTargets = []string{"go-test"}
	p.DispatchOnlyTargets = nil
	p.ForwardingTargets = []string{"go-test"}
	p.TargetSpellOps = map[string][]types.TargetSpellUse{"go-test": {{Spell: "go", Ops: []string{"go-test"}}}}
	p.TargetChains = map[string][]types.ChainStep{"go-test": {{Target: "build"}}}
	dialect, known := resolvedTestSelection(ws, types.LeaseCheck{Target: "go-test", Project: "."})
	s.True(known)
	s.Equal("go", dialect)
	root := testRoot(s.T()).Root()
	store := tempStore(s.T(), root).InWorkspace(ws)
	s.Empty(narrowedTestProblems(store, types.Job{ID: "scoped", WritePaths: []string{"pkg/a.go"},
		Check: &types.LeaseCheck{Target: "go-test", Project: ".", Args: []string{"./pkg", "-run", "Alpha"}}}), "scoped to the written package")
	s.Len(narrowedTestProblems(store, types.Job{ID: "unscoped", WritePaths: []string{"pkg/a.go"},
		Check: &types.LeaseCheck{Target: "go-test", Project: ".", Args: []string{"-run", "Alpha"}}}), 1, "-run alone still leaves tests out")
}
func (s *testSelectionSuite) TestNeedsCompositionDoesNotInheritSelector() {
	ws := goCheckWorkspace(".")
	p := ws.Projects[0]
	p.MagusfileTargets = append(p.MagusfileTargets, "all")
	p.TargetChains = map[string][]types.ChainStep{"all": {{Target: "test"}}}
	dialect, known := resolvedTestSelection(ws, types.LeaseCheck{Target: "all", Project: "."})
	s.False(known)
	s.Empty(dialect)
}
func (s *testSelectionSuite) TestMissingWorkspaceKeepsEvidenceStrict() {
	store := tempStore(s.T(), s.T().TempDir())
	check := types.LeaseCheck{Target: "test", Project: "."}
	attempt := types.JobAttempt{Project: ".", Target: "test", ExtraArgs: []string{"-run", "Alpha"}}
	s.False(ranCheck(check, attempt, nil, store.testSelection()))
	s.Empty(narrowedTestProblems(store, types.Job{Check: &check, WritePaths: []string{"pkg/a.go"}}))
}
func (s *testSelectionSuite) TestUnavailableWorkspaceKeepsEvidenceStrict() {
	store := tempStore(s.T(), s.T().TempDir()).InWorkspace(Workspace{Unavailable: "load failed"})
	check := types.LeaseCheck{Target: "go::go-test", Project: "."}
	s.False(ranCheck(check, types.JobAttempt{Project: ".", Target: "go-test", Spell: "go", ExtraArgs: []string{"-run", "Alpha"}}, nil, store.testSelection()))
}
func (s *testSelectionSuite) TestUnknownProjectDoesNotBorrowAnotherProjectDialect() {
	dialect, known := resolvedTestSelection(goCheckWorkspace("."), types.LeaseCheck{Target: "test", Project: "docs"})
	s.False(known)
	s.Empty(dialect)
}
func (s *testSelectionSuite) TestStoreValidationReportsUnavailableSelection() {
	store := tempStore(s.T(), testRoot(s.T()).Root())
	validation, err := ValidateFork(context.Background(), store, "unknown", func(row *types.Job) {
		row.WritePaths = []string{"pkg/a.go"}
		row.Check = &types.LeaseCheck{Target: "test", Project: ".", Args: []string{"-run", "Alpha"}}
	}, config.Jobs{}, nil)
	s.Require().NoError(err)
	s.Contains(validation.Unchecked, "the check's project, target and test-selection semantics: this door opened the job store without a workspace")
}

func scopedSpell(name, ext string, scopes map[string][]string) *spells.Spell {
	ops := map[string]spells.Op{"vet": {}}
	targets := []string{"vet"}
	for op, scope := range scopes {
		ops[op] = spells.Op{Command: spells.Command{TestScope: scope}}
		targets = append(targets, op)
	}
	return spells.NewSpell(name, spells.WithTargets(targets...), spells.WithOps(ops), spells.WithLanguageExtensions([]string{ext}))
}

type narrowTestScopeSuite struct {
	suite.Suite
	checkout string
}

func TestNarrowTestScope(t *testing.T) { suite.Run(t, new(narrowTestScopeSuite)) }

func (s *narrowTestScopeSuite) SetupTest() {
	s.checkout = s.T().TempDir()
	for _, dir := range []string{"pkg/a", "libs/x/sub"} {
		s.Require().NoError(os.MkdirAll(filepath.Join(s.checkout, dir), 0o755))
	}
}

func (s *narrowTestScopeSuite) project(path string, bound ...*spells.Spell) *types.Project {
	return &types.Project{Path: path, ResolvedSpells: bound}
}

func (s *narrowTestScopeSuite) goSpell() *spells.Spell {
	return scopedSpell("go", ".go", map[string][]string{"go-test": {"./{dir}"}})
}

func (s *narrowTestScopeSuite) TestFilesInOneDirectoryShareOneOperand() {
	got := NarrowTestScope([]*types.Project{s.project(".", s.goSpell())}, []string{"pkg/a/x.go", "pkg/a/y.go"}, s.checkout)
	s.Equal([]TestScope{{Project: ".", Spell: "go", Op: "go::go-test", Files: []string{"pkg/a/x.go", "pkg/a/y.go"}, Operands: []string{"./pkg/a"}}}, got)
}

func (s *narrowTestScopeSuite) TestANestedProjectExpandsItsOwnRelativePath() {
	projects := []*types.Project{s.project(".", s.goSpell()), s.project("libs/x", s.goSpell())}
	got := NarrowTestScope(projects, []string{"libs/x/sub/z.go"}, s.checkout)
	s.Equal([]TestScope{{Project: "libs/x", Spell: "go", Op: "go::go-test", Files: []string{"libs/x/sub/z.go"}, Operands: []string{"./sub"}}}, got)
}

func (s *narrowTestScopeSuite) TestAnOperandMissingFromTheCheckoutIsDroppedAndSaysSo() {
	buzz := scopedSpell("buzz", ".buzz", map[string][]string{"buzz-test": {"{file}", "{dir}/{stem}_test.buzz"}})
	s.Require().NoError(os.WriteFile(filepath.Join(s.checkout, "pkg/a/m.buzz"), nil, 0o644))
	got := NarrowTestScope([]*types.Project{s.project(".", buzz)}, []string{"pkg/a/m.buzz", "pkg/a/gone.buzz"}, s.checkout)
	s.Equal([]TestScope{{Project: ".", Spell: "buzz", Op: "buzz::buzz-test", Files: []string{"pkg/a/m.buzz", "pkg/a/gone.buzz"}, Operands: []string{"pkg/a/m.buzz"}}}, got)
}

func (s *narrowTestScopeSuite) TestNoExistingOperandRunsTheWholeProject() {
	buzz := scopedSpell("buzz", ".buzz", map[string][]string{"buzz-test": {"{file}"}})
	got := NarrowTestScope([]*types.Project{s.project(".", buzz)}, []string{"pkg/a/gone.buzz"}, s.checkout)
	s.Equal([]TestScope{{Project: ".", Spell: "buzz", Op: "buzz::buzz-test", Files: []string{"pkg/a/gone.buzz"},
		Whole: "nothing buzz::buzz-test's testScope expands these files to exists in the checkout"}}, got)
}

func (s *narrowTestScopeSuite) TestASpellWithoutTestScopeRunsItsWholeProject() {
	py := scopedSpell("python", ".py", nil)
	got := NarrowTestScope([]*types.Project{s.project(".", py)}, []string{"pkg/a/m.py"}, s.checkout)
	s.Equal([]TestScope{{Project: ".", Spell: "python", Files: []string{"pkg/a/m.py"}, Whole: "spell python declares no testScope, so its whole project runs"}}, got)
}

func (s *narrowTestScopeSuite) TestTwoScopedOpsNameNoTestOp() {
	two := scopedSpell("go", ".go", map[string][]string{"go-test": {"./{dir}"}, "go-race": {"./{dir}"}})
	got := NarrowTestScope([]*types.Project{s.project(".", two)}, []string{"pkg/a/x.go"}, s.checkout)
	s.Equal([]TestScope{{Project: ".", Spell: "go", Files: []string{"pkg/a/x.go"},
		Whole: "spell go declares testScope on ops go-race, go-test, so which one is its test op is unknown"}}, got)
}

func (s *narrowTestScopeSuite) TestAFileNoSpellClaimsSaysSo() {
	got := NarrowTestScope([]*types.Project{s.project(".", s.goSpell())}, []string{"docs/a.md"}, s.checkout)
	s.Equal([]TestScope{{Project: ".", Files: []string{"docs/a.md"}, Whole: `no spell bound to . claims its extension ".md"`}}, got)
}

func (s *narrowTestScopeSuite) TestAFileOutsideEveryProjectSaysSo() {
	got := NarrowTestScope([]*types.Project{s.project("libs/x", s.goSpell())}, []string{"pkg/a/x.go"}, s.checkout)
	s.Equal([]TestScope{{Files: []string{"pkg/a/x.go"}, Whole: "no project contains it"}}, got)
}

func (s *narrowTestScopeSuite) TestAGlobIsNotExpanded() {
	got := NarrowTestScope([]*types.Project{s.project(".", s.goSpell())}, []string{"pkg/**/*.go"}, s.checkout)
	s.Equal([]TestScope{{Project: ".", Spell: "go", Op: "go::go-test", Files: []string{"pkg/**/*.go"}, Whole: "a glob names no one file to expand"}}, got)
}
