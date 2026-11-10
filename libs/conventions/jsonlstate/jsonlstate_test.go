package jsonlstate

import (
	"testing"

	"github.com/egladman/magus/libs/conventions/internal/gitdiff"
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
	analyzer := newWith(s.options(), lines{{From: 1, To: 1000}})
	analysistest.Run(s.T(), analysistest.TestData(), analyzer, "a")
}

func (s *AnalyzerSuite) TestUnchangedLinesAreNotReported() {
	analyzer := newWith(s.options(), lines{{From: 1, To: 1}})
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

type lines []gitdiff.Span

func (l lines) Lines(string) ([]gitdiff.Span, error) { return l, nil }

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
