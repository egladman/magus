package main

import (
	"context"

	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/journal"
)

// sinkFuncs lists sinks in the type BeginInvocation takes, whose exported
// signature names no internal type.
func sinkFuncs(sinks []recorder.Sink) []func(context.Context, journal.Record) {
	out := make([]func(context.Context, journal.Record), len(sinks))
	for i, s := range sinks {
		out[i] = s
	}
	return out
}

// withInvocationJournal adds this invocation's fact handler to the capture fan-out.
func withInvocationJournal(ctx context.Context, sinks []recorder.Sink, root, verb string, args []string) []recorder.Sink {
	lease := func(ctx context.Context, root string) (string, job.LeaseSource, error) {
		return guard.CheckoutLease(ctx, root, jobPlace)
	}
	return sessions.WithInvocationFacts(ctx, sinks, root, verb, args, version, lease)
}
