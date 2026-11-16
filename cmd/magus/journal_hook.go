package main

import (
	"context"
	"log/slog"

	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/safetyscissors/job"
)

// withInvocationJournal adds this invocation's fact handler to the capture fan-out.
func withInvocationJournal(ctx context.Context, handlers []slog.Handler, root, verb string, args []string) []slog.Handler {
	lease := func(ctx context.Context, root string) (string, job.LeaseSource, error) {
		return guard.CheckoutLease(ctx, root, jobPlace)
	}
	return trail.WithInvocationJournal(ctx, handlers, root, verb, args, version, lease)
}
