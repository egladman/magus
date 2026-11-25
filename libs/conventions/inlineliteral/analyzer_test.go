package inlineliteral

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/libs/conventions/internal/gitdiff"
	"github.com/stretchr/testify/suite"
	"golang.org/x/tools/go/analysis/analysistest"
)

type AnalyzerSuite struct {
	suite.Suite
}

func TestAnalyzer(t *testing.T) { suite.Run(t, new(AnalyzerSuite)) }

// changed names each file's changed lines by base name. A file it does not
// name changed on every line, as one the base lacks.
type changed map[string][]gitdiff.Span

func (c changed) Lines(path string) ([]gitdiff.Span, error) {
	if spans, ok := c[filepath.Base(path)]; ok {
		return spans, nil
	}
	return []gitdiff.Span{{From: 1, To: math.MaxInt}}, nil
}

// policy writes body as analyzers.yaml in a fresh working directory, where an
// analyzer with no module reads it. It returns the testdata directory it left.
func (s *AnalyzerSuite) policy(body string) string {
	testdata := analysistest.TestData()
	s.T().Chdir(s.T().TempDir())
	s.Require().NoError(os.WriteFile("analyzers.yaml", []byte(body), 0o600))
	return testdata
}

func (s *AnalyzerSuite) TestReportsAndInlinesEachSite() {
	testdata := s.policy("")
	analyzer, err := newWith(Options{MaxSites: 2}, changed{})
	s.Require().NoError(err)
	analysistest.RunWithSuggestedFixes(s.T(), testdata, analyzer, "builders")
}

func (s *AnalyzerSuite) TestAnExitExemptsAndAStaleOneIsReported() {
	testdata := s.policy(`inlineliteral:
  exits:
    - {path: ledgered/ledgered.go, symbol: kept, reason: "named defaults"}
    - {path: ledgered/ledgered.go, symbol: gone, reason: "named defaults"}
`)
	analyzer, err := newWith(Options{MaxSites: 2}, changed{})
	s.Require().NoError(err)
	analysistest.Run(s.T(), testdata, analyzer, "ledgered")
}

func (s *AnalyzerSuite) TestNewRequiresABase() {
	_, err := New(Options{MaxSites: 2})
	s.ErrorContains(err, "no base revision")
}

func (s *AnalyzerSuite) TestNewRequiresMaxSites() {
	_, err := New(Options{Base: "origin/main"})
	s.ErrorContains(err, "max-sites")
}
