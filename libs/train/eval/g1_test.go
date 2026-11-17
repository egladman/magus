package eval

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type g1Suite struct{ suite.Suite }

func TestG1Suite(t *testing.T) { suite.Run(t, new(g1Suite)) }

func (s *g1Suite) TestFailureReasonBucketsByCode() {
	s.Equal("BZZ1005", failureReason("[BZZ1005] buzz: x.buzz:1:1: cannot assign"))
	s.Equal("other", failureReason("buzz: x.buzz:3:1: if statement is not allowed"))
}

func (s *g1Suite) TestCountChecksSetsProjectImportsAside() {
	rows := []CheckRow{
		{Path: "a", OKAtRev: true},
		{Path: "b", OKAtRev: true},
		{Path: "c", ReasonAtRev: `x.buzz:0:0: [BZZ2001] buzz: import "magus/spell/go": module not found`},
		{Path: "d", ReasonAtRev: `x.buzz:0:0: [BZZ2001] buzz: import "project/../..": module not found`},
		{Path: "e", ReasonAtRev: "[BZZ1005] x.buzz:1:1: cannot assign"},
	}
	got := CountChecks(rows)
	s.Equal(2, got.Pass)
	s.Equal(4, got.Counted)
	s.Equal(1, got.ProjectImport)
}

func (s *g1Suite) TestG1ThresholdsAreElisRetry3Bars() {
	s.Equal(2000, MinFIMBuzzSpans)
	s.Equal(30, MinFIMBuzzGroups)
	s.Equal(120, MinPassKTasks)
	s.InDelta(0.95, MinCheckPass, 1e-9)
}
