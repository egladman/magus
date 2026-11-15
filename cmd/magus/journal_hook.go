package main

import (
	"context"
	"log/slog"

	"github.com/egladman/magus/internal/trail"
)

// withInvocationJournal adds this invocation's fact handler to the capture fan-out.
func withInvocationJournal(ctx context.Context, handlers []slog.Handler, root, verb string, args []string) []slog.Handler {
	return trail.WithInvocationJournal(ctx, handlers, root, verb, args, version, checkoutLease)
}
