package cache

import (
	"context"
	"testing"
)

func TestSlotHeld(t *testing.T) {
	ctx := context.Background()
	if SlotHeld(ctx) {
		t.Error("bare context should report no slot held")
	}

	held := WithSlotHeld(ctx)
	if !SlotHeld(held) {
		t.Error("WithSlotHeld: SlotHeld returned false")
	}

	released := WithoutSlotHeld(held)
	if SlotHeld(released) {
		t.Error("WithoutSlotHeld: SlotHeld still true after clearing")
	}
}

func TestLimiterFromContext_RoundTrip(t *testing.T) {
	ctx := context.Background()
	if lim := LimiterFromContext(ctx); lim != nil {
		t.Errorf("empty context: LimiterFromContext = %v, want nil", lim)
	}
}

func TestCacheFromContext_RoundTrip(t *testing.T) {
	ctx := context.Background()
	if c := CacheFromContext(ctx); c != nil {
		t.Errorf("empty context: CacheFromContext = %v, want nil", c)
	}

	cacheDir := t.TempDir()
	c, err := Open(cacheDir, WithMutable(false))
	if err != nil {
		t.Fatalf("cache.Open: %v", err)
	}
	ctx = ContextWithCache(ctx, c)
	if got := CacheFromContext(ctx); got != c {
		t.Errorf("CacheFromContext = %v, want %v", got, c)
	}
}
