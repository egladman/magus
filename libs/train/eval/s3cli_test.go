package eval

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type CLISuite struct{ suite.Suite }

func TestCLI(t *testing.T) { suite.Run(t, new(CLISuite)) }

func (s *CLISuite) TestParseSetsReadsCaps() {
	got, err := ParseSets("buzz-test,go-test", "go-test=300")
	s.Require().NoError(err)
	s.Equal([]SetSpec{{Name: "buzz-test", Language: "buzz"}, {Name: "go-test", Language: "go", Max: 300}}, got)
	s.Equal("fim-buzz-test", got[0].Digest())
}

func (s *CLISuite) TestParseSetsRefusesAStrayCapAndABadName() {
	_, err := ParseSets("buzz-test", "go-test=3")
	s.ErrorContains(err, "go-test")
	_, err = ParseSets("buzz", "")
	s.ErrorContains(err, "not <language>")
	_, err = ParseSets("buzz-test", "buzz-test=x")
	s.ErrorContains(err, "not set=N")
}

func (s *CLISuite) TestOpenNeedsARunName() {
	_, _, err := Config{}.Open()
	s.ErrorContains(err, "-run")
}
