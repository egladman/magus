package settle

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// contended has the shape of the root package's unexported lockContendedError: a plain
// error that states 75.
type contended struct{}

func (contended) Error() string {
	return "magus: project . is locked by another magus process (held by pid 41221 magus run test), not waiting"
}

func (contended) ExitCode() int { return 75 }

func TestSettleBusyIsTheTempFailStatus(t *testing.T) {
	assert.True(t, busy(contended{}))
	assert.True(t, busy(fmt.Errorf("magus run generate:rw .: %w", contended{})), "through the wrapping Hook adds")
	assert.False(t, busy(errors.New("generate failed")))
	assert.False(t, busy(nil))
}

// TestSettleWaitingRetriesUntilTheLockFrees: a run refused as busy is retried, the
// holder is named once however many attempts it takes, and the run that finally starts
// decides the result.
func TestSettleWaitingRetriesUntilTheLockFrees(t *testing.T) {
	attempts := 0
	var said []string
	run := Waiting(func(context.Context, []string) error {
		attempts++
		if attempts < 3 {
			return contended{}
		}
		return nil
	}, time.Minute, time.Millisecond, func(_ context.Context, line string) { said = append(said, line) })

	require.NoError(t, run(t.Context(), []string{"generate:rw", "."}))
	assert.Equal(t, 3, attempts)
	assert.Equal(t, []string{"magus: project . is locked by another magus process (held by pid 41221 magus run test); waiting up to 1m0s for it"}, said)
}

// TestSettleWaitingGivesUpAtTheBound: past the bound the refusal comes back, still busy
// so the caller can tell a held lock from a failed generator; any other failure comes
// back at once.
func TestSettleWaitingGivesUpAtTheBound(t *testing.T) {
	attempts := 0
	run := Waiting(func(context.Context, []string) error {
		attempts++
		return contended{}
	}, 20*time.Millisecond, 5*time.Millisecond, func(context.Context, string) {})
	err := run(t.Context(), []string{"generate:rw", "."})
	assert.True(t, busy(err))
	assert.ErrorContains(t, err, "waited 20ms: magus: project . is locked")
	assert.Greater(t, attempts, 1)

	attempts = 0
	broken := errors.New("generate failed")
	run = Waiting(func(context.Context, []string) error {
		attempts++
		return broken
	}, time.Minute, time.Millisecond, func(context.Context, string) {})
	require.ErrorIs(t, run(t.Context(), nil), broken)
	assert.Equal(t, 1, attempts)
}

// TestSettleWaitingStopsWithItsContext: Ctrl-C during the wait ends it.
func TestSettleWaitingStopsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	run := Waiting(func(context.Context, []string) error {
		cancel()
		return contended{}
	}, time.Minute, time.Second, func(context.Context, string) {})
	require.ErrorIs(t, run(ctx, nil), context.Canceled)
}
