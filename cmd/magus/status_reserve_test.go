package main

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
)

type CapacityReserveSuite struct{ suite.Suite }

func TestCapacityReserveSuite(t *testing.T) { suite.Run(t, new(CapacityReserveSuite)) }

func (s *CapacityReserveSuite) TestTheMemoryFigureNamesWhatItLeftTheOS() {
	s.Equal("slots 8/10  mem 14.4 GiB/21.0 GiB (24.0 GiB less 3.0 GiB: the aggressive profile keeps 1/8, at least 1 GiB)",
		capacityText(types.MachineSnapshot{
			BudgetSlots: 10, HeldSlots: 8, BudgetMB: 21504, HeldMB: 14746,
			Reserve: types.MemoryReserve{TotalMB: 24576, ReservedMB: 3072, Rule: "the aggressive profile keeps 1/8, at least 1 GiB"},
		}))
}

func (s *CapacityReserveSuite) TestABrokerThatSendsNoReserveShowsTheBudgetAlone() {
	s.Equal("mem 1.0 GiB/4.0 GiB", capacityText(types.MachineSnapshot{BudgetMB: 4096, HeldMB: 1024}))
}
