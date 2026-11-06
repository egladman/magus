package mem

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
)

type ReserveSuite struct{ suite.Suite }

func TestReserveSuite(t *testing.T) { suite.Run(t, new(ReserveSuite)) }

func (s *ReserveSuite) TestNoProfileBudgetsTheWholeMachine() {
	for _, p := range []types.ConcurrencyProfile{"", types.ProfileConservative, types.ProfileBalanced, types.ProfileAggressive} {
		s.Less(BudgetMB(24<<30, p), 24576, "profile %q", p)
	}
}

func (s *ReserveSuite) TestTheReserveNamesTheProfileThatSetIt() {
	s.Equal(types.MemoryReserve{TotalMB: 24576, ReservedMB: 3072, Rule: "the aggressive profile keeps 1/8, at least 1 GiB"},
		Reserve(24<<30, types.ProfileAggressive))
	s.Equal(types.MemoryReserve{TotalMB: 16384, ReservedMB: 4096, Rule: "the balanced profile keeps 1/4"},
		Reserve(16<<30, ""))
}

func (s *ReserveSuite) TestAnUnmeasurableHostReservesNothing() {
	s.Zero(Reserve(0, types.ProfileAggressive))
}
