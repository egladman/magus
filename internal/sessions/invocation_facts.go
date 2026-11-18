package sessions

import (
	"context"
	"log/slog"
	"strings"

	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/session"
)

// WithInvocationFacts appends this invocation's fact sink to sinks, or
// returns sinks unchanged when the store cannot be resolved. verb and args
// are the recorded command line, and checkoutLease resolves the lease the
// invocation acts under. Every command and the server's adopted runs share
// this wiring, so a result lands in one shape whatever produced it.
func WithInvocationFacts(ctx context.Context, sinks []recorder.Sink, root, verb string, args []string, version string, checkoutLease func(ctx context.Context, root string) (string, job.LeaseSource, error)) []recorder.Sink {
	// The environment is read ONCE, here, and every fact this invocation writes carries that
	// copy. Reading it per fact would let a mid-run environment change split one
	// invocation's facts across two leases, which is a history no producer could have meant.
	spawn := recorder.SpawnFromEnv()
	// The lease is resolved like every other: on an ADOPTED run the environment belongs to
	// the SERVER, so the client's lease on ctx is the claim, and a checkout's binding
	// outranks it. A binding that does not read records no lease and says why, rather than
	// recording the claim as if nothing outranked it.
	lease, leaseFrom, err := checkoutLease(ctx, root)
	if err != nil {
		slog.With(attr.Component("magus")).WarnContext(ctx, "this invocation records no lease", attr.Error(err))
		lease, leaseFrom = "", ""
	}
	h := newFactHandler(root, session.InvocationStart{
		Origin:       recorder.LocalOrigin(ctx),
		Workspace:    root,
		Command:      strings.Join(append([]string{verb}, args...), " "),
		Version:      version,
		Lease:        lease,
		LeaseFrom:    leaseFrom,
		TraceID:      spawn.TraceID,
		ParentSpanID: spawn.ParentSpanID,
		SpanID:       recorder.NewSpanID(),
		Spawner:      spawn.Spawner,
	})
	if h == nil {
		return sinks
	}
	return append(sinks, h)
}
