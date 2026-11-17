package job

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/types"
)

// FileAttention normalizes an event into an [sessions.AttentionOpen] and files it under
// the lease the checkout holds. The write lifecycle (dedupe, id derivation, the record
// itself) belongs to [sessions.OpenRequest]; what stays here is the two gates that decide
// whether an event is a request at all, and the flattening of the event onto the store's
// string fields. place locates the job store of root, which is where the lease is read.
//
// Only waiting and permission open one. Those two outcomes mean the work has STOPPED
// until a person acts; a failure or a finished run is news, and news that queued up
// for disposal would teach people to clear the queue without reading it.
//
// held reports that the queue already held this block before the call, so a person
// has been told once. It is false for a new row, for news, for a block that has
// no row to live in (no source id, no repository), and on error. An empty root is no
// repository.
func FileAttention(ctx context.Context, root string, ev types.Event, origin types.Origin, version string, place func(root string) (Location, error)) (held bool, err error) {
	if ev.Outcome != types.OutcomeWaiting && ev.Outcome != types.OutcomePermission {
		return false, nil
	}
	if ev.Source.ID == "" {
		// An empty agent session is not an id, it is the absence of one, and
		// [sessions.RequestID] would happily digest it: every producer that sent
		// none would collapse onto ONE request, and a single dispose would close blocks
		// nobody had read. No id, no durable request: the same graceful path as no
		// repository, because the notification itself still fires.
		noteMissingAttentionSource(ctx)
		return false, nil
	}
	if root == "" {
		// No repository, so no queue to join.
		return false, nil
	}
	dir, err := sessions.Dir(root)
	if err != nil {
		return false, err
	}

	// Lease and files are not inputs to the id, on purpose; see sessions.RequestID.
	// They ride the payload so the queue can say whose work is blocked, and which
	// paths the event named, without the row's identity moving when either changes.
	loc, err := place(root)
	if err != nil {
		return false, fmt.Errorf("resolve the checkout's lease: %w", err)
	}
	lease, leaseFrom := RunLease(ctx, loc)
	open := sessions.AttentionOpen{
		Outcome:   string(ev.Outcome),
		Severity:  string(ev.Severity),
		Source:    sessions.SourceLabel(ev.Source.Kind, ev.Source.Sub),
		Where:     sessions.AttentionWhere(ev.Where),
		Lease:     lease,
		LeaseFrom: leaseFrom,
		Files:     sessions.AttentionFiles(ev.Where),
		Message:   ev.Message,
	}
	_, opened, err := sessions.OpenRequest(dir, ev.Source.ID, open, sessions.InvocationStart{
		Origin:    origin,
		Workspace: root,
		Version:   version,
	})
	if err != nil {
		return false, err
	}
	return !opened, nil
}

// noteMissingAttentionSource reports the one event shape that cannot become a durable
// request. It leaves the notification itself alone (the desktop alert still fires) and
// says what the producer has to change, because an agent whose blocks never reach the
// queue has no other symptom.
func noteMissingAttentionSource(ctx context.Context) {
	slog.With(attr.Component("session notify")).WarnContext(ctx, "the event carries no source.id, so no attention request was opened; a request id keys on the agent session that raised the block, and an empty one would merge unrelated producers into a single row",
		attr.Why("have the agent wrapper send source.id, the host's own session identifier, in the event envelope"))
}
