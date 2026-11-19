package clikitmain

import (
	"os"
	"testing"

	"github.com/egladman/magus/libs/conventions/internal/sourcetest"

	"github.com/stretchr/testify/suite"
	"golang.org/x/tools/go/analysis/analysistest"
)

type AnalyzerSuite struct {
	suite.Suite
}

func TestClikitMain(t *testing.T) {
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
clikitmain:
  baseline:
    counted/main.go: 2
    over/main.go: 1
    stale/main.go: 3
    gone/main.go: 1
  exits:
    - path: excused/main.go
      reason: the fixture pins that an exit excuses its file
`

// TestReportsHandParsedMains covers handmain (each kind of read), counted (at
// its count), over (past it), stale (under it), gone (at none), excused (an
// exit), library (no main package) and nolint (a directive that excuses
// nothing).
func (s *AnalyzerSuite) TestReportsHandParsedMains() {
	testdata := analysistest.TestData()
	s.policy(policyBody)
	analyzer, err := New(Options{})
	s.Require().NoError(err)
	analysistest.Run(s.T(), testdata, analyzer, "handmain", "counted", "over", "stale", "gone", "excused", "library", "nolint")
}

func (s *AnalyzerSuite) TestNoPolicyFileExcusesNothing() {
	s.T().Chdir(s.T().TempDir())
	_, err := New(Options{})
	s.Require().NoError(err)
}

func (s *AnalyzerSuite) rejects(body, want string) {
	s.policy(body)
	_, err := New(Options{})
	s.Require().ErrorContains(err, want)
}

func (s *AnalyzerSuite) TestNewRejectsAnUnknownField() {
	s.rejects("clikitmain:\n  baselines: {}\n", "not found")
}

func (s *AnalyzerSuite) TestNewRejectsACountUnderOne() {
	s.rejects("clikitmain:\n  baseline:\n    a.go: 0\n", "leaves the baseline")
}

func (s *AnalyzerSuite) TestNewRejectsAnExitWithNoReason() {
	s.rejects("clikitmain:\n  exits:\n    - path: a.go\n", "with no reason")
}

func (s *AnalyzerSuite) TestNewRejectsAFileInBothLists() {
	s.rejects("clikitmain:\n  baseline:\n    a.go: 1\n  exits:\n    - {path: a.go, reason: r}\n", "in both")
}

func (s *AnalyzerSuite) TestNewRejectsAnEntryNamingNoFile() {
	sourcetest.Module(s.T(), "example.com/m", "cmd/app/main.go")
	s.Require().NoError(os.WriteFile(policyFile, []byte("clikitmain:\n  baseline:\n    cmd/gone/main.go: 1\n"), 0o644))
	_, err := New(Options{Module: "example.com/m"})
	s.Require().ErrorContains(err, "is not a file under")
}
