package job

import (
	"context"
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
	notes := uncheckedTestSelection(ws, types.Job{Check: &check})
	s.Require().Len(notes, 1)
	s.Contains(notes[0], "recorded evidence arguments must match exactly")
	selection := tempStore(s.T(), s.T().TempDir()).InWorkspace(ws).testSelection()
	s.False(scopedRun(check, types.JobAttempt{ExtraArgs: []string{"-run", "Beta"}}, selection))
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
