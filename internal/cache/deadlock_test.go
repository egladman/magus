package cache

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/safetyscissors/budget"
	"github.com/egladman/magus/types"
)

type SlotDeadlockSuite struct{ suite.Suite }

func TestSlotDeadlock(t *testing.T) { suite.Run(t, new(SlotDeadlockSuite)) }

// The limiter's own refusal becomes MGS3013 naming every party, with a config status:
// waiting cannot fix it, so it is not EX_TEMPFAIL.
func (s *SlotDeadlockSuite) TestRefusalIsMGS3013WithAConfigStatus() {
	t := s.T()
	err := slotDeadlockError(budget.SlotDeadlock{
		Capacity: 2,
		Holding: []string{
			". build holds 1 and is waiting on the cache lock for 4f1ac2be, held by . generate",
			". test holds 1 and is waiting on 1 build slot(s)",
		},
		Queued: []string{". mocks-generate needs 1"},
	})
	require.ErrorIs(t, err, types.BuildSlotsDeadlocked)
	for _, want := range []string{
		". build holds 1 and is waiting on the cache lock for 4f1ac2be, held by . generate",
		". test holds 1 and is waiting on 1 build slot(s)",
		". mocks-generate needs 1",
		"all 2 of this run's slots",
	} {
		assert.Contains(t, err.Error(), want)
	}
	var coded interface{ ExitCode() int }
	require.True(t, errors.As(err, &coded))
	assert.Equal(t, ExitCodeSlotDeadlock, coded.ExitCode())
}

// Every limiter magus builds refuses with that error.
func (s *SlotDeadlockSuite) TestNewLimiterRefusesWithMGS3013() {
	t := s.T()
	lim := NewLimiter(1)
	held, release, err := lim.Admit(t.Context(), ". build")
	require.NoError(t, err)
	defer release()
	unblock := budget.BlockedOn(held, "the cache lock")
	defer unblock()
	_, _, err = lim.Admit(t.Context(), ". test")
	require.ErrorIs(t, err, types.BuildSlotsDeadlocked)
}
