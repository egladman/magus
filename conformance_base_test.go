package magus

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/symbols/buzzindex"
	"github.com/egladman/magus/internal/symbols/goindex"
	"github.com/egladman/magus/internal/symbols/shindex"
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

	s.Equal([]baseIndex{{project: "libs/old", language: "go"}}, present.indexes)
	s.Equal(s.plan.external, present.external)
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
	s.Equal(&goindex.Indexer{Dir: "/t/p", Patterns: []string{"."}}, ix)
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
