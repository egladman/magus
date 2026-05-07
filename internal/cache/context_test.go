package cache_test

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/cache"
)

func TestSlotHeld(t *testing.T) {
	ctx := context.Background()
	if cache.SlotHeld(ctx) {
		t.Error("bare context should report no slot held")
	}

	held := cache.WithSlotHeld(ctx)
	if !cache.SlotHeld(held) {
		t.Error("WithSlotHeld: SlotHeld returned false")
	}

	released := cache.WithoutSlotHeld(held)
	if cache.SlotHeld(released) {
		t.Error("WithoutSlotHeld: SlotHeld still true after clearing")
	}
}

func TestLimiterFromContext_RoundTrip(t *testing.T) {
	ctx := context.Background()
	if lim := cache.LimiterFromContext(ctx); lim != nil {
		t.Errorf("empty context: LimiterFromContext = %v, want nil", lim)
	}
}

func TestCacheFromContext_RoundTrip(t *testing.T) {
	ctx := context.Background()
	if c := cache.CacheFromContext(ctx); c != nil {
		t.Errorf("empty context: CacheFromContext = %v, want nil", c)
	}

	cacheDir := t.TempDir()
	c, err := cache.Open(cacheDir, cache.WithMutable(false))
	if err != nil {
		t.Fatalf("cache.Open: %v", err)
	}
	ctx = cache.ContextWithCache(ctx, c)
	if got := cache.CacheFromContext(ctx); got != c {
		t.Errorf("CacheFromContext = %v, want %v", got, c)
	}
}
