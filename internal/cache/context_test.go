package cache

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSlotHeld(t *testing.T) {
	ctx := context.Background()
	assert.False(t, SlotHeld(ctx), "bare context should report no slot held")

	held := WithSlotHeld(ctx)
	assert.True(t, SlotHeld(held), "WithSlotHeld: SlotHeld returned false")

	released := WithoutSlotHeld(held)
	assert.False(t, SlotHeld(released), "WithoutSlotHeld: SlotHeld still true after clearing")
}

// A slot marker changes ONE field of a hold and leaves the rest alone. Rewriting any of
// them to return a fresh admission compiles, reads correctly, and silently un-fixes the
// bug the struct exists for: a needs child taking a second machine claim. Whole-struct, so
// a field added later is covered without anyone remembering to extend this.
func TestSlotMarkersPreserveTheRestOfTheHold(t *testing.T) {
	base := admission{slots: 4, machineClaim: true}.on(context.Background())

	assert.Equal(t, admission{slots: 2, machineClaim: true}, admissionFrom(WithSlotsHeld(base, 2)))
	assert.Equal(t, admission{slots: 1, machineClaim: true}, admissionFrom(WithSlotHeld(base)))
	assert.Equal(t, admission{slots: 0, machineClaim: true}, admissionFrom(WithoutSlotHeld(base)))
}

func TestLimiterFromContext_RoundTrip(t *testing.T) {
	ctx := context.Background()
	assert.Nil(t, LimiterFromContext(ctx), "empty context: LimiterFromContext should be nil")
}

func TestFromContext_RoundTrip(t *testing.T) {
	ctx := context.Background()
	assert.Nil(t, FromContext(ctx), "empty context: FromContext should be nil")

	cacheDir := t.TempDir()
	c, err := Open(t.Context(), cacheDir, WithMutable(false))
	require.NoError(t, err)
	ctx = NewContext(ctx, c)
	assert.Equal(t, c, FromContext(ctx))
}
