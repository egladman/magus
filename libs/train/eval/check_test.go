package eval

import ()

func (s *evalSuite) TestCountChecksSetsProjectImportsAside() {
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
