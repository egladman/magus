package trail

import (
	"context"
	"log/slog"
	"strings"

	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/libs/safetyscissors/job"
)

// WithInvocationJournal adds this invocation's fact handler to the capture fan-out,
// returning handlers unchanged when the store cannot be resolved.
//
// verb is the subcommand ("run", "affected") the recorded command line begins with;
// args is the rest of it; version is the build recorded beside it. checkoutLease resolves
// the lease the invocation acts under.
//
// It is shared rather than written per command on purpose: `magus session` is a view
// of the repository, so a target result must land in the store in the same shape no
// matter which command produced it. Two copies of the wiring would drift, and the
// drift would read as a gap in the history rather than as a bug.
//
// It also covers the SERVER, without a second wiring site: the server executes an
// adopted run by calling runTarget/affected itself (main.go's dispatchAdopted), so a
// forwarded run reaches this function in the server process. ctx is what tells the two
// cases apart; see below.
func WithInvocationJournal(ctx context.Context, handlers []slog.Handler, root, verb string, args []string, version string, checkoutLease func(ctx context.Context, root string) (string, job.LeaseSource, error)) []slog.Handler {
	// The environment is read ONCE, here, and every fact this invocation writes carries that
	// copy. Reading it per fact would let a mid-run environment change split one
	// invocation's facts across two leases, which is a history no producer could have meant.
	spawn := SpawnFromEnv()
	// The lease is resolved like every other: on an ADOPTED run the environment belongs to
	// the SERVER, so the client's lease on ctx is the claim, and a checkout's binding
	// outranks it. A binding that does not read records no lease and says why, rather than
	// recording the claim as if nothing outranked it.
	lease, leaseFrom, err := checkoutLease(ctx, root)
	if err != nil {
		slog.With(attr.Component("magus")).WarnContext(ctx, "this invocation records no lease", attr.Error(err))
		lease, leaseFrom = "", ""
	}
	h := sessions.NewFactHandler(root, sessions.InvocationStart{
		Origin:       LocalOrigin(ctx),
		Workspace:    root,
		Command:      strings.Join(append([]string{verb}, args...), " "),
		Version:      version,
		Lease:        lease,
		LeaseFrom:    leaseFrom,
		TraceID:      spawn.TraceID,
		ParentSpanID: spawn.ParentSpanID,
		SpanID:       NewSpanID(),
		Spawner:      spawn.Spawner,
	})
	if h == nil {
		return handlers
	}
	return append(handlers, h)
}
