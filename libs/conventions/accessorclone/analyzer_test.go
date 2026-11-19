package accessorclone

import (
	"os"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/conventions/internal/sourcetest"

	"github.com/stretchr/testify/suite"
	"golang.org/x/tools/go/analysis/analysistest"
)

type AnalyzerSuite struct {
	suite.Suite
}

func TestAccessorClone(t *testing.T) {
	suite.Run(t, new(AnalyzerSuite))
}

// policy writes body as analyzers.yaml in a fresh working directory, where an
// analyzer with no module reads it.
func (s *AnalyzerSuite) policy(body string) {
	s.T().Chdir(s.T().TempDir())
	s.Require().NoError(os.WriteFile(policyFile, []byte(body), 0o644))
}

const policyBody = `other:
  baseline: {unread: true}
accessorclone:
  baseline:
    api/api.go: [Store.Listed]
    stale/stale.go: [Box.Gone]
  exits:
    - path: nolint/nolint.go
      symbol: Box.Held
      reason: the fixture pins that a nolint directive excuses nothing
`

// TestReportsAccessorsOutsideThePolicy covers api (reported, resliced,
// multi-value, package variable, excused, cloned, closure, unexported),
// quiet (nothing to report), stale (a baseline entry to delete) and nolint
// (a directive that excuses nothing).
func (s *AnalyzerSuite) TestReportsAccessorsOutsideThePolicy() {
	testdata := analysistest.TestData()
	s.policy(policyBody)
	analyzer, err := New(Options{})
	s.Require().NoError(err)
	analysistest.Run(s.T(), testdata, analyzer, "api", "quiet", "stale", "nolint")
}

func (s *AnalyzerSuite) TestNoPolicyFileExcusesNothing() {
	s.T().Chdir(s.T().TempDir())
	_, err := New(Options{})
	s.Require().NoError(err)
}

func (s *AnalyzerSuite) TestAPolicyWithNoKeyExcusesNothing() {
	s.policy("other: {}\n")
	_, err := New(Options{})
	s.Require().NoError(err)
}

func (s *AnalyzerSuite) rejects(body, want string) {
	s.policy(body)
	_, err := New(Options{})
	s.Require().ErrorContains(err, want)
}

func (s *AnalyzerSuite) TestNewRejectsAnEmptySymbol() {
	s.rejects("accessorclone:\n  baseline:\n    a.go: [\"\"]\n", "names a path and a symbol")
}

func (s *AnalyzerSuite) TestNewRejectsAnUnknownField() {
	s.rejects("accessorclone:\n  baselines: {}\n", "not found")
}

func (s *AnalyzerSuite) TestNewRejectsAnAccessorListedTwice() {
	s.rejects("accessorclone:\n  baseline:\n    a.go: [A, A]\n", "twice")
}

func (s *AnalyzerSuite) TestNewRejectsAnExitWithNoReason() {
	s.rejects("accessorclone:\n  exits:\n    - {path: a.go, symbol: A}\n", "with no reason")
}

func (s *AnalyzerSuite) TestNewRejectsAnExitThatRepeatsTheBaseline() {
	s.rejects("accessorclone:\n  baseline:\n    a.go: [A]\n  exits:\n    - {path: a.go, symbol: A, reason: r}\n",
		"in accessorclone.baseline and accessorclone.exits")
}

func (s *AnalyzerSuite) TestNewReadsTheModuleRootsPolicy() {
	root := sourcetest.Module(s.T(), "example.com/m", "pkg/pkg.go")
	s.Require().NoError(os.WriteFile(root+"/"+policyFile, []byte("accessorclone:\n  baseline:\n    pkg/moved.go: [A]\n"), 0o644))
	_, err := New(Options{Module: "example.com/m"})
	s.Require().Error(err)
	s.True(strings.Contains(err.Error(), "pkg/moved.go"), err.Error())
}

func (s *AnalyzerSuite) TestNewAcceptsAFileThatExists() {
	root := sourcetest.Module(s.T(), "example.com/m", "pkg/pkg.go")
	s.Require().NoError(os.WriteFile(root+"/"+policyFile, []byte("accessorclone:\n  baseline:\n    pkg/pkg.go: [A]\n"), 0o644))
	_, err := New(Options{Module: "example.com/m"})
	s.Require().NoError(err)
}
