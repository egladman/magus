package jsonlstate

import (
	"testing"

	"github.com/stretchr/testify/suite"
	"golang.org/x/tools/go/analysis/analysistest"
)

type AnalyzerSuite struct {
	suite.Suite
}

func TestAnalyzerSuite(t *testing.T) {
	suite.Run(t, new(AnalyzerSuite))
}

func (s *AnalyzerSuite) options() Options {
	return Options{
		Base:          "origin/main",
		StateRoots:    []string{"a.stateDir"},
		StateSegments: []string{".magus"},
		StateFields:   []string{"a.store.path"},
		Writers:       []string{"a.writeAtomic"},
		Marshalers:    []string{"a.mustMarshal"},
	}
}

func (s *AnalyzerSuite) TestReportsWholeDocumentWrites() {
	analyzer := newWith(s.options(), lines{{1, 1000}})
	analysistest.Run(s.T(), analysistest.TestData(), analyzer, "a")
}

func (s *AnalyzerSuite) TestUnchangedLinesAreNotReported() {
	analyzer := newWith(s.options(), lines{{1, 1}})
	for _, r := range analysistest.Run(discard{}, analysistest.TestData(), analyzer, "a") {
		s.Empty(r.Diagnostics)
	}
}

func (s *AnalyzerSuite) TestNewRequiresABase() {
	opts := s.options()
	opts.Base = ""
	_, err := New(opts)
	s.Require().ErrorContains(err, "no base revision")
}

func (s *AnalyzerSuite) TestHunksSpanTheWorkingCopyLines() {
	diff := "@@ -3,0 +4,2 @@\n+a\n+b\n@@ -9,2 +11 @@\n@@ -20,3 +21,0 @@\n"
	s.Equal([]span{{4, 5}, {11, 11}, {21, 22}}, hunks(diff))
}

type lines []span

func (l lines) File(string) ([]span, error) { return l, nil }

// discard drops analysistest's verdict: the want comments expect reports.
type discard struct{}

func (discard) Errorf(string, ...any) {}

func (s *AnalyzerSuite) TestNewNeedsAStatePath() {
	_, err := New(Options{Base: "origin/main"})
	s.Require().ErrorContains(err, "no state-roots")
}

func (s *AnalyzerSuite) TestNewRejectsAMalformedRoot() {
	opts := s.options()
	opts.StateRoots = []string{"stateDir"}
	_, err := New(opts)
	s.Require().ErrorContains(err, "is not <import path>.<name>")
}

func (s *AnalyzerSuite) TestNewRejectsAMalformedField() {
	opts := s.options()
	opts.StateFields = []string{"a.store"}
	_, err := New(opts)
	s.Require().ErrorContains(err, "<import path>.<type>.<field>")
}

func (s *AnalyzerSuite) TestNewRejectsAnEmptySegment() {
	opts := s.options()
	opts.StateSegments = []string{""}
	_, err := New(opts)
	s.Require().ErrorContains(err, "empty state segment")
}
