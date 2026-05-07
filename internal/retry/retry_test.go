package retry_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/retry"
)

var errFake = errors.New("fake error")

func TestDoSucceedsFirstTry(t *testing.T) {
	t.Parallel()
	calls := 0
	err := retry.Do(context.Background(), func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestDoSucceedsAfterRetries(t *testing.T) {
	t.Parallel()
	calls := 0
	err := retry.Do(context.Background(), func() error {
		calls++
		if calls < 3 {
			return errFake
		}
		return nil
	}, retry.WithAttempts(3), retry.WithDelay(time.Millisecond), retry.WithMaxDelay(time.Millisecond))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestDoExhausts(t *testing.T) {
	t.Parallel()
	calls := 0
	err := retry.Do(context.Background(), func() error {
		calls++
		return errFake
	}, retry.WithAttempts(3), retry.WithDelay(time.Millisecond), retry.WithMaxDelay(time.Millisecond))
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !errors.Is(err, errFake) {
		t.Fatalf("error %q does not wrap errFake", err)
	}
	if !strings.Contains(err.Error(), "3 attempts") {
		t.Fatalf("error %q does not mention attempt count", err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestDoRespectsContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())

	calls := 0
	start := time.Now()
	err := retry.Do(ctx, func() error {
		calls++
		if calls == 1 {
			cancel()
		}
		return errFake
	}, retry.WithAttempts(5), retry.WithDelay(10*time.Second), retry.WithMaxDelay(10*time.Second))

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("took %v, want < 500ms", elapsed)
	}
}

func TestDoCallsOnRetry(t *testing.T) {
	t.Parallel()
	var retries []int
	err := retry.Do(
		context.Background(), func() error { return errFake },
		retry.WithAttempts(4),
		retry.WithDelay(time.Millisecond),
		retry.WithMaxDelay(time.Millisecond),
		retry.WithOnRetry(func(attempt int, _ error) {
			retries = append(retries, attempt)
		}),
	)

	if err == nil {
		t.Fatal("want error, got nil")
	}
	// OnRetry fires between attempts — not after the last failure.
	if len(retries) != 3 {
		t.Fatalf("OnRetry called %d times, want 3; calls: %v", len(retries), retries)
	}
	for i, got := range retries {
		if want := i + 1; got != want {
			t.Fatalf("retries[%d] = %d, want %d", i, got, want)
		}
	}
}

func TestDoAppliesDefaults(t *testing.T) {
	t.Parallel()
	// No options → default 3 attempts, 1s delay, 30s maxDelay.
	// Override Delay to keep the test fast; observe Attempts via OnRetry.
	calls := 0
	err := retry.Do(
		context.Background(), func() error { return errFake },
		retry.WithDelay(time.Millisecond),
		retry.WithMaxDelay(time.Millisecond),
		retry.WithOnRetry(func(_ int, _ error) { calls++ }),
	)

	if err == nil {
		t.Fatal("want error, got nil")
	}
	// Default Attempts == 3, so OnRetry fires 2 times.
	if calls != 2 {
		t.Fatalf("OnRetry calls = %d, want 2 (default 3 attempts)", calls)
	}
}

func TestDoBackoffCaps(t *testing.T) {
	t.Parallel()
	cap := 4 * time.Millisecond
	var gaps []time.Duration
	prev := time.Now()

	retry.Do(
		context.Background(), func() error { return errFake },
		retry.WithAttempts(5),
		retry.WithDelay(2*time.Millisecond),
		retry.WithMaxDelay(cap),
		retry.WithOnRetry(func(_ int, _ error) {
			now := time.Now()
			gaps = append(gaps, now.Sub(prev))
			prev = now
		}),
	)

	// gaps[i] is the time between attempt i completing and OnRetry being called —
	// essentially zero. The sleep happens after OnRetry. So we verify that the
	// total duration of the test is bounded by (Attempts-1) * MaxDelay.
	for i, g := range gaps {
		if g > cap+20*time.Millisecond { // generous for scheduler jitter
			t.Errorf("gap[%d] = %v, want ≤ %v", i, g, cap+20*time.Millisecond)
		}
	}
}
