package spells

import (
	"testing"

	"github.com/egladman/magus/internal/json"
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
	s.Require().NoError(json.Unmarshal([]byte(`{"format":"go-coverprofile","file":"cover.out","test":"go-test","command":{"bin":"go"},"uses":["go"]}`), &c))
	s.Equal(Coverage{Format: CoverageFormatGoProfile, File: "cover.out", Test: "go-test", Command: Command{Bin: "go"}, Uses: []string{"go"}}, c)
}
