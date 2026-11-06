package mem

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
)

// The v1 sentinel case is why UsableBytes takes a minimum rather than trying to
// recognize either cgroup version's spelling of unlimited: a ceiling larger than
// the machine describes no ceiling at all.
func TestNarrowToLimit(t *testing.T) {
	const total = 16 << 30
	for _, tc := range []struct {
		name         string
		total, limit int64
		want         int64
	}{
		{"no limit is the machine", total, 0, total},
		{"a real container ceiling wins", total, 4 << 30, 4 << 30},
		{"a ceiling above the machine is no ceiling", total, 9223372036854771712, total},
		{"a ceiling equal to the machine changes nothing", total, total, total},
		{"an unmeasurable host inside a measured container", 0, 4 << 30, 4 << 30},
		{"both unknown stays unknown", 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, narrowToLimit(tc.total, tc.limit))
		})
	}
}

// BudgetMB is the arithmetic machine-wide admission is sized from, so an unmeasurable
// host must read as "no budget to arbitrate" rather than as a budget of nothing: the
// difference between admitting everything and refusing everything. Balanced,
// conservative and the zero value all keep the same fraction; only aggressive reads
// differently, in BudgetMB_Aggressive below.
func TestBudgetMB(t *testing.T) {
	assert.Equal(t, 12288, BudgetMB(16<<30, types.ProfileBalanced), "three quarters of the machine")
	assert.Equal(t, 12288, BudgetMB(16<<30, types.ProfileConservative), "conservative keeps the same fraction as balanced")
	assert.Equal(t, 12288, BudgetMB(16<<30, ""), "an unset profile keeps the fraction, same as balanced")
	assert.Equal(t, 0, BudgetMB(0, types.ProfileBalanced), "an unmeasurable host has no budget to arbitrate")
	assert.Equal(t, 0, BudgetMB(-1, types.ProfileBalanced))
}

// TestBudgetMB_Aggressive pins the default profile's reservation: an eighth,
// never under the 1 GiB floor, so the desktop keeps memory.
func TestBudgetMB_Aggressive(t *testing.T) {
	assert.Equal(t, 21504, BudgetMB(24<<30, types.ProfileAggressive),
		"a 24 GiB Mac keeps 3 GiB, so 12.0 and 10.2 GiB steps cannot both seat")
	assert.Equal(t, 6144, BudgetMB(7<<30, types.ProfileAggressive), "a 7 GiB runner keeps the 1 GiB floor")
	assert.Equal(t, 0, BudgetMB(0, types.ProfileAggressive), "an unmeasurable host still has no budget to arbitrate")
	assert.Equal(t, 0, BudgetMB(-1, types.ProfileAggressive))
	assert.Equal(t, 1, BudgetMB(256<<20, types.ProfileAggressive),
		"a measurable host under the floor gets a nominal budget of 1 MB, never 0: 0 reads as unmeasurable/unlimited")
}

// UsableBytes never exceeds the machine, whatever this host reports.
func TestUsableNeverExceedsTotal(t *testing.T) {
	ctx := context.Background()
	if total := totalBytes(ctx); total > 0 {
		assert.LessOrEqual(t, UsableBytes(ctx), total)
	} else {
		assert.Zero(t, totalBytes(ctx), "an unmeasurable host reports UNKNOWN")
	}
}

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
