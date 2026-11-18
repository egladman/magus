package main

import (
	"context"

	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/safetyscissors/job"
)

// withInvocationJournal adds this invocation's fact handler to the capture fan-out.
func withInvocationJournal(ctx context.Context, sinks []recorder.Sink, root, verb string, args []string) []recorder.Sink {
	lease := func(ctx context.Context, root string) (string, job.LeaseSource, error) {
		return guard.CheckoutLease(ctx, root, jobPlace)
	}
	return trail.WithInvocationJournal(ctx, sinks, root, verb, args, version, lease)
}
