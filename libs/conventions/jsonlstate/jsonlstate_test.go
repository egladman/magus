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
		StateRoots:    []string{"a.stateDir"},
		StateSegments: []string{".magus"},
		StateFields:   []string{"a.store.path"},
		Writers:       []string{"a.writeAtomic"},
		Marshalers:    []string{"a.mustMarshal"},
	}
}

func (s *AnalyzerSuite) TestReportsWholeDocumentWrites() {
	analyzer, err := New(s.options())
	s.Require().NoError(err)
	analysistest.Run(s.T(), analysistest.TestData(), analyzer, "a")
}

func (s *AnalyzerSuite) TestNewNeedsAStatePath() {
	_, err := New(Options{})
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
