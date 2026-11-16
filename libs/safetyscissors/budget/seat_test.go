package budget

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type SeatSuite struct{ suite.Suite }

func TestSeat(t *testing.T) { suite.Run(t, new(SeatSuite)) }

func (s *SeatSuite) TestAnUnmarkedContextHoldsNothing() {
	ctx := context.Background()
	assert.Equal(s.T(), 0, SlotsHeld(ctx))
	assert.False(s.T(), SlotHeld(ctx))
	assert.False(s.T(), MachineClaimHeld(ctx))
}

// Each marker changes its own half of the seat and keeps the other: a child context
// carrying a subset of its parent's hold is the bug the one value exists to prevent.
func (s *SeatSuite) TestEachMarkerKeepsTheRestOfTheSeat() {
	t := s.T()
	ctx := WithMachineClaim(WithSlotsHeld(context.Background(), 3), true)
	assert.Equal(t, 3, SlotsHeld(ctx))
	assert.True(t, MachineClaimHeld(ctx))

	child := WithoutSlotHeld(ctx)
	assert.False(t, SlotHeld(child))
	assert.True(t, MachineClaimHeld(child), "clearing the slots keeps the claim")

	lent := WithMachineClaim(WithSlotHeld(child), false)
	assert.Equal(t, 1, SlotsHeld(lent), "lending the claim keeps the slot")
	assert.False(t, MachineClaimHeld(lent))
}

// A marker changes ONE field of a seat and leaves the rest alone. Rewriting any of them to
// return a fresh seat compiles, reads correctly, and silently un-fixes the bug the struct
// exists for. Whole-struct, so a field added later is covered without anyone remembering
// to extend this.
func (s *SeatSuite) TestMarkersPreserveTheWholeSeat() {
	t := s.T()
	hold := &slotHold{label: ". ci"}
	base := seat{slots: 4, machineClaim: true, hold: hold}.on(context.Background())

	assert.Equal(t, seat{slots: 2, machineClaim: true, hold: hold}, seatFromContext(WithSlotsHeld(base, 2)))
	assert.Equal(t, seat{slots: 1, machineClaim: true, hold: hold}, seatFromContext(WithSlotHeld(base)))
	assert.Equal(t, seat{slots: 0, machineClaim: true, hold: hold}, seatFromContext(WithoutSlotHeld(base)))
	assert.Equal(t, seat{slots: 4, hold: hold}, seatFromContext(WithMachineClaim(base, false)))
}
