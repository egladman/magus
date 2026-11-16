package spells

import (
	"testing"

	"github.com/egladman/magus/libs/codec"
	"github.com/stretchr/testify/suite"
)

type coverageSuite struct{ suite.Suite }

func TestCoverage(t *testing.T) { suite.Run(t, new(coverageSuite)) }

func (s *coverageSuite) TestFormatListsItsCases() {
	s.Equal([]string{"lcov", "go-coverprofile"}, CoverageFormatNone.Values())
}

func (s *coverageSuite) TestAnUnknownCaseIsInvalid() {
	s.False(CoverageFormat("cobertura").Valid())
}

func (s *coverageSuite) TestLCOVIsAValidCase() {
	s.True(CoverageFormatLCOV.Valid())
}

func (s *coverageSuite) TestDecodesFromItsJSON() {
	var c Coverage
	s.Require().NoError(codec.Unmarshal([]byte(`{"format":"go-coverprofile","profile":".magus/coverage.out"}`), &c))
	s.Equal(Coverage{Format: CoverageFormatGoProfile, Profile: ".magus/coverage.out"}, c)
}
