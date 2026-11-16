package tier

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type CeilingSuite struct{ suite.Suite }

func TestCeiling(t *testing.T) { suite.Run(t, new(CeilingSuite)) }

// lowHigh declares low and high, cheapest first, as a magusfile's enum would.
func (s *CeilingSuite) lowHigh() (low, high Tier) {
	set, err := NewSet(Declaration{
		Enum:   "Tier",
		Site:   "magusfile.buzz:1",
		Cases:  []string{"low", "high"},
		Bounds: make([]Bounds, 2),
		Skills: []SkillVariant{SkillVariantFull, SkillVariantShort},
	})
	s.Require().NoError(err)
	low, err = set.Parse("low")
	s.Require().NoError(err)
	high, err = set.Parse("high")
	s.Require().NoError(err)
	return low, high
}

func (s *CeilingSuite) TestAChildRunsAtItsParentsTierOrBelow() {
	low, high := s.lowHigh()
	s.NoError(Ceiling(high, low))
	s.NoError(Ceiling(high, high))
}

func (s *CeilingSuite) TestARankAboveTheParentIsAnAboveParentError() {
	low, high := s.lowHigh()
	err := Ceiling(low, high)
	var above *AboveParentError
	s.Require().ErrorAs(err, &above)
	s.Equal("low", above.Parent.Name())
	s.Equal("high", above.Child.Name())
	s.Equal(`a job at model tier "low" may not spawn one at "high": a child's tier is at most its parent's`, err.Error())
}

func (s *CeilingSuite) TestAnUnassignedTierBoundsNothing() {
	low, high := s.lowHigh()
	s.NoError(Ceiling(Tier{}, high), "an unassigned parent bounds nothing")
	s.NoError(Ceiling(low, Tier{}), "an unassigned child is bounded by nothing")
}

func (s *CeilingSuite) TestTiersFromTwoDeclarationsAreIncomparable() {
	low, _ := s.lowHigh()
	otherLow, _ := s.lowHigh()
	err := Ceiling(otherLow, low)
	var apart *IncomparableError
	s.Require().ErrorAs(err, &apart)
	s.Contains(err.Error(), "come from different declarations")
}
