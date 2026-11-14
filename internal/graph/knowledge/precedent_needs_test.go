package knowledge

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
)

type NeedsDirectionSuite struct {
	suite.Suite
	g *Graph
}

func TestNeedsDirectionSuite(t *testing.T) {
	suite.Run(t, new(NeedsDirectionSuite))
}

func (s *NeedsDirectionSuite) SetupTest() { s.g = NewGraph() }

// project defines names as targets of project.
func (s *NeedsDirectionSuite) project(project string, names ...string) {
	for _, n := range names {
		s.g.AddNode(types.KnowledgeNode{ID: targetID(project, n), Kind: types.KindTarget, Label: n, Source: project})
	}
}

// needs records that from's target needs to, which may sit in another project.
func (s *NeedsDirectionSuite) needs(from, to string) {
	s.g.AddEdge(types.KnowledgeEdge{Source: from, Target: to, Relation: types.RelationDependsOn,
		Confidence: types.ConfidenceExtracted, Score: 1})
}

// projects defines format and generate in n projects p0..p(n-1); the first
// follow of them have format need generate.
func (s *NeedsDirectionSuite) projects(n, follow int) {
	for i := range n {
		p := fmt.Sprintf("p%d", i)
		s.project(p, "format", "generate")
		if i < follow {
			s.needs(targetID(p, "format"), targetID(p, "generate"))
		}
	}
}

func (s *NeedsDirectionSuite) rows() []types.Precedent {
	var out []types.Precedent
	for _, r := range s.g.Precedents(PrecedentOptions{Conformance: testConformance()}) {
		if r.Family == types.PrecedentNeedsDirection {
			out = append(out, r)
		}
	}
	return out
}

func (s *NeedsDirectionSuite) row(from, to string) types.Precedent {
	for _, r := range s.rows() {
		if r.Key.From == from && r.Key.To == to {
			return r
		}
	}
	s.Require().FailNow("no row", "%s reaches %s in %+v", from, to, s.rows())
	return types.Precedent{}
}

func (s *NeedsDirectionSuite) TestMajorityDirectionIsEstablished() {
	s.projects(5, 4)
	r := s.row("format", "generate")
	s.Equal(4, r.Follow)
	s.Equal(5, r.Cohort)
	s.InDelta(0.8, r.Share, 1e-9)
	s.True(r.Established)
}

func (s *NeedsDirectionSuite) TestDepartingProjectIsListed() {
	s.projects(5, 4)
	r := s.row("format", "generate")
	s.Equal([]types.Case{needsCase("p4", "format")}, r.Departures)
	s.Equal([]types.Case{needsCase("p0", "format"), needsCase("p1", "format"), needsCase("p2", "format")}, r.Cited)
}

func (s *NeedsDirectionSuite) TestSmallCohortIsEmittedUnestablished() {
	s.projects(4, 4)
	r := s.row("format", "generate")
	s.Equal(4, r.Cohort)
	s.False(r.Established)
}

func (s *NeedsDirectionSuite) TestShareUnderMinShareIsNotEstablished() {
	s.projects(5, 3)
	r := s.row("format", "generate")
	s.Equal(3, r.Follow)
	s.False(r.Established)
}

func (s *NeedsDirectionSuite) TestReachIsTransitive() {
	s.project("a", "ci", "lint", "format")
	s.needs(targetID("a", "ci"), targetID("a", "lint"))
	s.needs(targetID("a", "lint"), targetID("a", "format"))
	s.Equal(1, s.row("ci", "format").Follow)
}

func (s *NeedsDirectionSuite) TestCrossProjectNeedsAreIgnored() {
	s.project("a", "ci", "format")
	s.project("b", "ci", "format")
	s.needs(targetID("a", "ci"), targetID("b", "format"))
	s.needs(targetID("b", "ci"), targetID("b", "format"))
	r := s.row("ci", "format")
	s.Equal(1, r.Follow)
	s.Equal([]types.Case{needsCase("a", "ci")}, r.Departures)
}

func (s *NeedsDirectionSuite) TestDepartureThatFollowsFromAnotherIsKept() {
	for i := range 6 {
		p := fmt.Sprintf("p%d", i)
		s.project(p, "ci", "format", "generate")
		s.needs(targetID(p, "ci"), targetID(p, "format"))
		if i < 5 {
			s.needs(targetID(p, "format"), targetID(p, "generate"))
		}
	}
	s.Equal([]types.Case{needsCase("p5", "format")}, s.row("format", "generate").Departures)
	s.Equal([]types.Case{needsCase("p5", "ci")}, s.row("ci", "generate").Departures,
		"ci misses generate only because format does, and it still departs")
}

func (s *NeedsDirectionSuite) TestPairNoProjectFollowsMakesNoRow() {
	s.projects(1, 1)
	for _, r := range s.rows() {
		s.NotEqual(types.PrecedentKey{From: "generate", To: "format"}, r.Key)
	}
}

func (s *NeedsDirectionSuite) TestRowsSortByFromThenTo() {
	s.project("a", "c", "b", "a")
	s.needs(targetID("a", "b"), targetID("a", "c"))
	s.needs(targetID("a", "a"), targetID("a", "c"))
	s.needs(targetID("a", "a"), targetID("a", "b"))
	var keys []types.PrecedentKey
	for _, r := range s.rows() {
		keys = append(keys, r.Key)
	}
	s.Equal([]types.PrecedentKey{{From: "a", To: "b"}, {From: "a", To: "c"}, {From: "b", To: "c"}}, keys)
}
