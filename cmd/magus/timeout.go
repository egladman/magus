package main

import (
	"context"
	"log/slog"
	"time"
)

// withTimeout wraps ctx with a deadline and starts a background ticker that
// prints elapsed-time heartbeats to stderr. The tick interval scales with the
// timeout so short timeouts get frequent updates and long ones stay quiet:
//
//	timeout ≤ 1 min  → tick every 10 s
//	timeout ≤ 5 min  → tick every 30 s
//	timeout > 5 min  → tick every 1 min
//
// The caller must defer the returned cancel to release resources.
func withTimeout(ctx context.Context, d time.Duration, label string) (context.Context, context.CancelFunc) {
	tctx, cancel := context.WithTimeout(ctx, d)

	interval := tickInterval(d)
	start := time.Now()
	deadline := start.Add(d)
	tk := time.NewTicker(interval)

	go func() {
		defer tk.Stop()
		for {
			select {
			case <-tctx.Done():
				return
			case now := <-tk.C:
				elapsed := now.Sub(start).Round(time.Second)
				rem := deadline.Sub(now).Round(time.Second)
				if rem < 0 {
					rem = 0
				}
				slog.InfoContext(tctx, "still running", slog.String("scope", label), slog.Duration("elapsed", elapsed), slog.Duration("remaining", rem))
			}
		}
	}()

	return tctx, cancel
}

func tickInterval(d time.Duration) time.Duration {
	switch {
	case d <= time.Minute:
		return 10 * time.Second
	case d <= 5*time.Minute:
		return 30 * time.Second
	default:
		return time.Minute
	}
}
