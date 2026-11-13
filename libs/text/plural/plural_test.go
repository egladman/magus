package plural

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type Suite struct{ suite.Suite }

func TestSuite(t *testing.T) { suite.Run(t, new(Suite)) }

func (s *Suite) TestWordOneIsSingular() {
	s.Equal("target", Word(1, "target", "targets"))
}

func (s *Suite) TestWordZeroIsPlural() {
	s.Equal("targets", Word(0, "target", "targets"))
}

func (s *Suite) TestWordManyIsPlural() {
	s.Equal("targets", Word(2, "target", "targets"))
}

func (s *Suite) TestWordNegativeOneIsPlural() {
	s.Equal("targets", Word(-1, "target", "targets"))
}

func (s *Suite) TestWordUsesTheSpelledForm() {
	s.Equal("branches", Word(2, "branch", "branches"))
}

func (s *Suite) TestCountOne() {
	s.Equal("1 target", Count(1, "target", "targets"))
}

func (s *Suite) TestCountZero() {
	s.Equal("0 targets", Count(0, "target", "targets"))
}

func (s *Suite) TestCountMany() {
	s.Equal("12 branches", Count(12, "branch", "branches"))
}
