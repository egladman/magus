package types

import "context"

type jobLeaseKey struct{}

// WithJobLease carries the job lease the process that asked for this work runs under. The
// daemon runs an adopted run for a client, so its own environment names nobody's lease,
// and every record the run writes reads the client's lease from here instead.
//
// An id failing [ValidJobID], "" included, stores nothing: the value crosses a socket any
// local process may dial, and a lease id is exempt from the trail's redaction.
func WithJobLease(ctx context.Context, lease string) context.Context {
	if !ValidJobID(lease) {
		return ctx
	}
	return context.WithValue(ctx, jobLeaseKey{}, lease)
}

// JobLeaseFromContext returns the lease WithJobLease stored, or "" when ctx carries none.
// A reader that also reads the environment's lease prefers this one when it is set.
func JobLeaseFromContext(ctx context.Context) string {
	lease, _ := ctx.Value(jobLeaseKey{}).(string)
	return lease
}
