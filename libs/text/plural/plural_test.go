package plural

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type PluralSuite struct{ suite.Suite }

func TestPluralSuite(t *testing.T) { suite.Run(t, new(PluralSuite)) }

func (s *PluralSuite) TestWordOneIsSingular() {
	s.Equal("target", Word(1, "target", "targets"))
}

func (s *PluralSuite) TestWordZeroIsPlural() {
	s.Equal("targets", Word(0, "target", "targets"))
}

func (s *PluralSuite) TestWordManyIsPlural() {
	s.Equal("targets", Word(2, "target", "targets"))
}

func (s *PluralSuite) TestWordNegativeOneIsPlural() {
	s.Equal("targets", Word(-1, "target", "targets"))
}

func (s *PluralSuite) TestWordUsesTheSpelledForm() {
	s.Equal("branches", Word(2, "branch", "branches"))
}

func (s *PluralSuite) TestCountOne() {
	s.Equal("1 target", Count(1, "target", "targets"))
}

func (s *PluralSuite) TestCountZero() {
	s.Equal("0 targets", Count(0, "target", "targets"))
}

func (s *PluralSuite) TestCountMany() {
	s.Equal("12 branches", Count(12, "branch", "branches"))
}
