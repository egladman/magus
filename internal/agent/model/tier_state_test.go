package model

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/safetyscissors/tier"
)

type TierStateSuite struct{ suite.Suite }

func TestTierState(t *testing.T) { suite.Run(t, new(TierStateSuite)) }

func (s *TierStateSuite) named(enum string) *tier.Set {
	set, err := tier.NewSet(tier.Declaration{
		Enum:    enum,
		Site:    "magusfile.buzz:3",
		Cases:   []string{"routine", "bounded"},
		Default: 1,
		Bounds:  []tier.Bounds{{}, {}},
		Skills:  []tier.SkillVariant{tier.SkillVariantFull, tier.SkillVariantShort},
	})
	s.Require().NoError(err)
	return set
}

func (s *TierStateSuite) TestNoDeclarationAndNoDefaultIsOff() {
	var state TierState
	set, err := state.Tiers()
	var off *TiersOffError
	s.Require().ErrorAs(err, &off)
	s.False(off.TurnedOff, "nothing turned them off; nothing declared them")
	s.Nil(set)
}

func (s *TierStateSuite) TestTheDefaultBuildsOnceOnTheFirstAsk() {
	var state TierState
	builds := 0
	shipped := s.named("Shipped")
	state.SetDefault(func() (*tier.Set, error) { builds++; return shipped, nil })
	s.Zero(builds, "setting the default builds nothing")
	for range 2 {
		set, err := state.Tiers()
		s.Require().NoError(err)
		s.Same(shipped, set)
	}
	s.Equal(1, builds)
}

func (s *TierStateSuite) TestADeclarationReplacesTheDefaultAndARerunReplacesIt() {
	var state TierState
	state.SetDefault(func() (*tier.Set, error) { return s.named("Shipped"), nil })
	first, second := s.named("First"), s.named("Second")
	state.Declare(first)
	set, err := state.Tiers()
	s.Require().NoError(err)
	s.Same(first, set)
	state.Declare(second)
	set, err = state.Tiers()
	s.Require().NoError(err)
	s.Same(second, set)
}

func (s *TierStateSuite) TestADeclarationOfNilTurnsTiersOff() {
	var state TierState
	state.SetDefault(func() (*tier.Set, error) {
		return nil, errors.New("the default must not build when tiers are off")
	})
	state.Declare(nil)
	set, err := state.Tiers()
	var off *TiersOffError
	s.Require().ErrorAs(err, &off)
	s.True(off.TurnedOff)
	s.Nil(set)
}

func (s *TierStateSuite) TestADefaultThatFailsReturnsItsErrorAndBuildsAgain() {
	var state TierState
	builds := 0
	shipped := s.named("Shipped")
	state.SetDefault(func() (*tier.Set, error) {
		builds++
		if builds == 1 {
			return nil, errors.New("broken")
		}
		return shipped, nil
	})
	_, err := state.Tiers()
	s.Require().ErrorContains(err, "broken")
	set, err := state.Tiers()
	s.Require().NoError(err)
	s.Same(shipped, set)
}

func (s *TierStateSuite) TestTheContextCarriesTheState() {
	state := &TierState{}
	s.Same(state, TierStateFromContext(WithTierState(s.T().Context(), state)))
	s.Nil(TierStateFromContext(s.T().Context()))
}
