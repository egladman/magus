package job

import (
	"context"
	"fmt"
)

type jobLeaseKey struct{}

// InvalidLeaseError is a lease [WithLease] refused: an id failing [ValidID].
type InvalidLeaseError struct {
	Lease string
	// Rule is the id rule the lease breaks.
	Rule string
}

func (e *InvalidLeaseError) Error() string {
	return fmt.Sprintf("job: lease %q is not a job id: %s", e.Lease, e.Rule)
}

// WithLease carries the job lease the process that asked for this work runs under. The
// daemon runs an adopted run for a client, so its own environment names nobody's lease,
// and every record the run writes reads the client's lease from here instead.
//
// An answer of "" records that the asker runs under no lease, which overrides a lease an
// outer ctx carried and stops a reader falling back to the environment; [WithoutLease]
// says so by name. Any other id failing [ValidID] is an [*InvalidLeaseError] and ctx is
// returned unchanged, because the value crosses a socket any local process may dial, and
// a lease id is exempt from the journal's redaction.
func WithLease(ctx context.Context, lease string) (context.Context, error) {
	if lease == "" {
		return WithoutLease(ctx), nil
	}
	if rule := idRule(lease); rule != "" {
		return ctx, &InvalidLeaseError{Lease: lease, Rule: rule}
	}
	return context.WithValue(ctx, jobLeaseKey{}, lease), nil
}

// WithoutLease records that the asker runs under no lease, overriding a lease an outer
// ctx carried.
func WithoutLease(ctx context.Context) context.Context {
	return context.WithValue(ctx, jobLeaseKey{}, "")
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
