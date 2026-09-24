package main

import (
	"context"
	"log/slog"
	"strings"

	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/trail"
)

// withInvocationJournal adds this invocation's fact handler to the capture fan-out,
// returning handlers unchanged when the store cannot be resolved.
//
// verb is the subcommand ("run", "affected") the recorded command line begins with;
// args is the rest of it.
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
func withInvocationJournal(ctx context.Context, handlers []slog.Handler, root, verb string, args []string) []slog.Handler {
	// The environment is read ONCE, here, and every fact this invocation writes carries that
	// copy. Reading it per fact would let a mid-run environment change split one
	// invocation's facts across two leases, which is a history no producer could have meant.
	//
	// On an ADOPTED run the environment belongs to the SERVER, not to whoever asked for the
	// run, so the environment channel alone leaves every forwarded run unattributed. proc
	// carries the client's lease on the request and lands it on ctx, and that is the claim
	// here. The trace context does not cross that socket, so a forwarded run records the
	// client's lease and no ancestry rather than borrowing the server's. A plain CLI run
	// carries none on ctx and reads its own environment.
	spawn := trail.SpawnFromEnv()
	if forwarded := proc.LeaseFromContext(ctx); forwarded != "" {
		spawn = trail.Spawn{Lease: forwarded}
	}
	// The claim is resolved like every other lease, so a checkout's binding outranks it. A
	// binding that does not read records no lease and says why, rather than recording the
	// claim as if nothing outranked it.
	lease, leaseFrom, err := checkoutLease(root, spawn.Lease)
	if err != nil {
		slog.WarnContext(ctx, "magus: this invocation records no lease", slog.String("error", err.Error()))
		lease, leaseFrom = "", ""
	}
	h := sessions.NewFactHandler(root, sessions.InvocationStart{
		Origin:       trail.LocalOrigin(ctx),
		Workspace:    root,
		Command:      strings.Join(append([]string{verb}, args...), " "),
		Version:      version,
		Lease:        lease,
		LeaseFrom:    leaseFrom,
		TraceID:      spawn.TraceID,
		ParentSpanID: spawn.ParentSpanID,
		SpanID:       trail.NewSpanID(),
		Spawner:      spawn.Spawner,
	})
	if h == nil {
		return handlers
	}
	return append(handlers, h)
}
