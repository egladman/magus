package closedswitch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/libs/conventions/internal/deviation"
	"github.com/stretchr/testify/suite"
	"golang.org/x/tools/go/analysis/analysistest"
)

type AnalyzerSuite struct {
	suite.Suite
}

func TestAnalyzerSuite(t *testing.T) { suite.Run(t, new(AnalyzerSuite)) }

// policy writes body as analyzers.yaml in a fresh working directory, where an
// analyzer with no module reads it.
func (s *AnalyzerSuite) policy(body string) {
	s.T().Chdir(s.T().TempDir())
	s.Require().NoError(os.WriteFile(policyFile, []byte(body), 0o600))
}

const policyBody = `other-analyzer:
  baseline:
    - {path: ignored.go, symbol: Ignored}
exhaustive-switch:
  baseline:
    - {path: held/held.go, symbol: Count}
    - {path: stale/stale.go, symbol: Nothing, count: 2}
  exits:
    - {path: held/held.go, symbol: Eval, type: Op, reason: fixture}
    - {path: full/full.go, symbol: Gone, type: Color, reason: fixture}
`

func (s *AnalyzerSuite) TestReportsEachShapeOfViolationAndStaleRows() {
	testdata := analysistest.TestData()
	s.policy(policyBody)
	analyzer, err := New(Options{})
	s.Require().NoError(err)
	analysistest.Run(s.T(), testdata, analyzer, "full", "gaps", "held", "stale")
}

func (s *AnalyzerSuite) TestNoPolicyFileAllowsNothing() {
	s.T().Chdir(s.T().TempDir())
	l := load(".", false)
	s.Require().NoError(l.err)
	s.Empty(l.settings.Baseline)
}

func (s *AnalyzerSuite) TestAPolicyWithoutTheKeyAllowsNothing() {
	s.policy("other-analyzer:\n  baseline: []\n")
	l := load(".", false)
	s.Require().NoError(l.err)
	s.Empty(l.settings.Baseline)
}

func (s *AnalyzerSuite) TestAnExitWithoutAReasonFailsToLoad() {
	s.policy("exhaustive-switch:\n  exits:\n    - {path: a.go, symbol: f, type: T}\n")
	s.Require().Error(load(".", false).err)
}

func (s *AnalyzerSuite) TestAMisspelledFieldFailsToLoad() {
	s.policy("exhaustive-switch:\n  baselines: []\n")
	s.Require().Error(load(".", false).err)
}

func (s *AnalyzerSuite) TestARowNamingAGoneFileFailsToLoad() {
	s.policy("exhaustive-switch:\n  baseline:\n    - {path: gone.go, symbol: f}\n")
	s.Require().Error(load(".", true).err)
}

func (s *AnalyzerSuite) TestAFileAnswersToTheNearestModule() {
	root := s.T().TempDir()
	nested := filepath.Join(root, "libs", "inner")
	s.Require().NoError(os.MkdirAll(nested, 0o750))
	s.Require().NoError(os.WriteFile(filepath.Join(root, "go.mod"), []byte("module outer\n"), 0o600))
	s.Require().NoError(os.WriteFile(filepath.Join(nested, "go.mod"), []byte("module outer/libs/inner\n"), 0o600))

	dir, ok := deviation.Owner(filepath.Join(nested, "x.go"))
	s.True(ok)
	s.Equal(nested, dir)
	dir, ok = deviation.Owner(filepath.Join(root, "cmd", "y.go"))
	s.True(ok)
	s.Equal(root, dir)
}
