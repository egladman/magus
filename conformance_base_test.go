package magus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/internal/symbols/buzzindex"
	"github.com/egladman/magus/internal/symbols/goindex"
	"github.com/egladman/magus/internal/symbols/shindex"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

type ConformanceBaseSuite struct {
	suite.Suite
	plan baseIndexPlan
}

func TestConformanceBaseSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(ConformanceBaseSuite))
}

func (s *ConformanceBaseSuite) SetupTest() {
	s.plan = baseIndexPlan{external: []baseExternal{
		{dir: "console", language: "typescript", extensions: []string{".ts", ".tsx"}},
	}}
}

func (s *ConformanceBaseSuite) TestAFileOfAnExternalLanguageIsUnmeasured() {
	lang, ok := s.plan.unmeasured("console/src/app.ts")
	s.True(ok)
	s.Equal("typescript", lang)
}

func (s *ConformanceBaseSuite) TestAnotherLanguageInTheSameProjectIsMeasured() {
	_, ok := s.plan.unmeasured("console/magusfile.buzz")
	s.False(ok)
}

func (s *ConformanceBaseSuite) TestAFileOutsideTheProjectIsMeasured() {
	_, ok := s.plan.unmeasured("consoles/app.ts")
	s.False(ok)
}

func (s *ConformanceBaseSuite) TestTheRootProjectHoldsEveryFile() {
	s.plan.external[0].dir = "."
	_, ok := s.plan.unmeasured("deep/x.tsx")
	s.True(ok)
}

func (s *ConformanceBaseSuite) TestAProjectTheBaseLacksIsNamedAndNotIndexed() {
	tree := s.T().TempDir()
	s.Require().NoError(os.MkdirAll(filepath.Join(tree, "libs", "old"), 0o700))
	s.plan.indexes = []baseIndex{{project: "libs/old", language: "go"}, {project: "libs/new", language: "go"}}

	present, absent := s.plan.splitAbsent(tree)

	s.Equal(baseIndexPlan{indexes: []baseIndex{{project: "libs/old", language: "go"}}, external: s.plan.external}, present)
	s.Equal([]types.ConformanceUnmeasured{{File: "libs/new", Language: "go", Reason: projectNewAtHead}}, absent)
}

func (s *ConformanceBaseSuite) TestAFileInAnAbsentProjectReportsItsReason() {
	absent := []types.ConformanceUnmeasured{{File: "libs/new", Language: "go", Reason: projectNewAtHead}}

	project, ok := insideAbsentProject(absent, "libs/new/a.go")

	s.True(ok)
	s.Equal(projectNewAtHead, project.Reason)
}

func (s *ConformanceBaseSuite) TestAFileBesideAnAbsentProjectIsJudged() {
	absent := []types.ConformanceUnmeasured{{File: "libs/new", Language: "go", Reason: projectNewAtHead}}

	_, ok := insideAbsentProject(absent, "libs/newer/a.go")

	s.False(ok)
}

func (s *ConformanceBaseSuite) TestGoRunsInProcessWithItsPatterns() {
	ix := inProcessIndexer(&spells.SymbolIndexer{Format: spells.SymbolFormatGoPackages}, "/t/p", "/t")
	got, ok := ix.(*goindex.Indexer)
	s.Require().True(ok)
	s.Equal("/t/p", got.Dir)
	s.Equal([]string{"."}, got.Patterns)
}

func (s *ConformanceBaseSuite) TestGoIsHandedTheTreeAsATrustedMiseConfig() {
	env := trustRoot([]string{"A=1", "MISE_TRUSTED_CONFIG_PATHS=/ws"}, "/t")

	s.Equal([]string{"A=1", "MISE_TRUSTED_CONFIG_PATHS=/ws" + string(os.PathListSeparator) + "/t"}, env)
}

func (s *ConformanceBaseSuite) TestGoIndexerEnvTrustsTheTree() {
	ix := inProcessIndexer(&spells.SymbolIndexer{Format: spells.SymbolFormatGoPackages}, "/t/p", "/t")

	var trusted []string
	for _, kv := range ix.(*goindex.Indexer).Env {
		if strings.HasPrefix(kv, "MISE_TRUSTED_CONFIG_PATHS=") {
			trusted = append(trusted, kv)
		}
	}
	s.Require().Len(trusted, 1)
	s.True(strings.HasSuffix(trusted[0], "/t"))
}

func (s *ConformanceBaseSuite) TestTheTreeIsTrustedAloneWhenNothingIsInherited() {
	s.Equal([]string{"A=1", "MISE_TRUSTED_CONFIG_PATHS=/t"}, trustRoot([]string{"A=1"}, "/t"))
}

func (s *ConformanceBaseSuite) TestBuzzAndShellRunInProcessUnderTheBaseRoot() {
	s.Equal(&buzzindex.Indexer{Dir: "/t/p", WorkspaceRoot: "/t"},
		inProcessIndexer(&spells.SymbolIndexer{Format: spells.SymbolFormatBuzzModule}, "/t/p", "/t"))
	s.Equal(&shindex.Indexer{Dir: "/t/p", WorkspaceRoot: "/t"},
		inProcessIndexer(&spells.SymbolIndexer{Format: spells.SymbolFormatShellScript}, "/t/p", "/t"))
}

func (s *ConformanceBaseSuite) TestAnExternalFormatHasNoInProcessIndexer() {
	s.Nil(inProcessIndexer(&spells.SymbolIndexer{Format: "scip"}, "/t/p", "/t"))
}

func (s *ConformanceBaseSuite) TestTheBaseTreeIsMadeInThePoliciesPrivateTempDir() {
	private := s.T().TempDir()
	ctx := sandbox.WithPolicy(s.T().Context(), &sandbox.Policy{TempDir: private})

	tree, err := conformanceTree(ctx)
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = os.RemoveAll(tree) })

	s.Equal(private, filepath.Dir(tree))
}

type ConformanceBaseBoxSuite struct {
	suite.Suite
}

func TestConformanceBaseBoxSuite(t *testing.T) {
	suite.Run(t, new(ConformanceBaseBoxSuite))
}

const boxMagusfile = "import \"magus\";\nexport fun build(ctx: magus\\Context, args: [str]) > void {}\n"

func (s *ConformanceBaseBoxSuite) inspect(root string, cfg config.Config) *Magus {
	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell("buzz"))
	inspected, err := Inspect(s.T().Context(), root, WithWorkspaceRegistry(reg), WithSpawnRule(TierCeiling), workspace.WithLoadedConfig(cfg))
	s.Require().NoError(err)
	m := inspected.(*Magus)
	s.T().Cleanup(func() { _ = m.Close() })
	return m
}

func (s *ConformanceBaseBoxSuite) TestMeasureWritesOnlyInsideTheBox() {
	denied := filepath.Join(s.T().TempDir(), "denied")
	s.Require().NoError(os.Mkdir(denied, 0o500))
	s.T().Setenv("XDG_CACHE_HOME", denied)
	s.T().Setenv("HOME", denied)
	granted := filepath.Join(s.T().TempDir(), "granted")
	cfg := config.Defaults()
	cfg.Cache.Dir = granted

	repo := testkit.NewRepository(s.T(), map[string]string{"magusfile.buzz": boxMagusfile})
	m := s.inspect(repo.Root(), cfg)
	commit, err := m.MeasureConformanceBase(s.T().Context(), "HEAD")
	s.Require().NoError(err)

	kept, err := filepath.Glob(filepath.Join(granted, "conformance", commit+"-*.jsonl"))
	s.Require().NoError(err)
	s.Len(kept, 1)
	s.NoDirExists(filepath.Join(denied, "magus"))
}

func (s *ConformanceBaseBoxSuite) TestCheckoutsOfOneWorkspaceReadOneMeasure() {
	s.T().Setenv("XDG_CACHE_HOME", s.T().TempDir())
	repo := testkit.NewRepository(s.T(), map[string]string{"magusfile.buzz": boxMagusfile})
	sibling := repo.AddWorktree("sibling")

	first := s.inspect(repo.Root(), config.Defaults())
	second := s.inspect(sibling.Root(), config.Defaults())
	s.NotEqual(first.CacheDir(), second.CacheDir())

	commit, err := first.MeasureConformanceBase(s.T().Context(), "HEAD")
	s.Require().NoError(err)

	plan, err := second.baseIndexPlan(s.T().Context(), second.Root())
	s.Require().NoError(err)
	entry, err := second.conformanceEntry(commit, plan)
	s.Require().NoError(err)
	_, kept, err := readConformanceEntry(entry)
	s.Require().NoError(err)
	s.True(kept, "the sibling checkout finds the measure the first one kept")
}
