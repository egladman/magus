package job

import "context"

type jobLeaseKey struct{}

// WithLease carries the job lease the process that asked for this work runs under. The
// daemon runs an adopted run for a client, so its own environment names nobody's lease,
// and every record the run writes reads the client's lease from here instead.
//
// It always answers: an id failing [ValidID], "" included, records that the asker
// runs under no lease, which overrides a lease an outer ctx carried and stops a reader
// falling back to the environment. The value crosses a socket any local process may
// dial, and a lease id is exempt from the trail's redaction.
func WithLease(ctx context.Context, lease string) context.Context {
	if !ValidID(lease) {
		lease = ""
	}
	return context.WithValue(ctx, jobLeaseKey{}, lease)
}

// LeaseFromContext returns the lease WithLease stored, or "" when ctx carries none.
func LeaseFromContext(ctx context.Context) string {
	lease, _ := LookupLease(ctx)
	return lease
}

// LookupLease returns the lease WithLease stored and whether ctx carries an answer
// at all. An answer of "" means the asker runs under no lease; only a ctx with no answer
// leaves the environment's lease to a reader.
func LookupLease(ctx context.Context) (string, bool) {
	lease, ok := ctx.Value(jobLeaseKey{}).(string)
	return lease, ok
}
