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

func TestLimiterFromContext_RoundTrip(t *testing.T) {
	ctx := context.Background()
	assert.Nil(t, LimiterFromContext(ctx), "empty context: LimiterFromContext should be nil")
}

func TestCacheFromContext_RoundTrip(t *testing.T) {
	ctx := context.Background()
	assert.Nil(t, CacheFromContext(ctx), "empty context: CacheFromContext should be nil")

	cacheDir := t.TempDir()
	c, err := Open(cacheDir, WithMutable(false))
	require.NoError(t, err)
	ctx = ContextWithCache(ctx, c)
	assert.Equal(t, c, CacheFromContext(ctx))
}
