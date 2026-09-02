package proc

import (
	"context"
	"fmt"
	"time"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/proc/endpoint"
	"github.com/egladman/magus/types"
)

// admitTimeout caps one admission round-trip, DIAL INCLUDED. The budget never blocks,
// so a slow answer means a wedged daemon rather than a busy machine, and a run must not
// queue behind one: the client treats a timeout as no arbiter and proceeds.
//
// A var so a test can spend it. It bounds the release too, which runs inside the defer
// that still holds a local limiter slot.
var admitTimeout = 5 * time.Second

// DaemonAdmitter reaches the machine budget in the daemon at Addr. It is the socket
// implementation of cache.MachineAdmitter, and the only thing that makes a magus
// running here answerable to a magus running in another worktree.
type DaemonAdmitter struct{ Addr string }

// Request polls the budget on behalf of waiter.
func (d DaemonAdmitter) Request(ctx context.Context, waiter string, c types.MachineClaim) (types.MachineVerdict, error) {
	var reply budgetAcquireReply
	req := budgetAcquireRequest{Magic: budgetMagic, Protocol: protocolV2, Waiter: waiter, Claim: c}
	if err := d.call(ctx, typeBudgetAcquire, req, typeBudgetAcquireReply, &reply); err != nil {
		return types.MachineVerdict{}, err
	}
	if reply.Err != "" {
		return types.MachineVerdict{}, fmt.Errorf("proc: %s: %s", typeBudgetAcquire, reply.Err)
	}
	return reply.Verdict, nil
}

// Release returns a granted claim. Errors are dropped: a release that cannot be
// delivered is retired by the budget's own liveness reap, and a teardown must not fail
// over bookkeeping.
func (d DaemonAdmitter) Release(ctx context.Context, id string) {
	var reply budgetReleaseReply
	req := budgetReleaseRequest{Magic: budgetMagic, Protocol: protocolV2, ID: id}
	_ = d.call(ctx, typeBudgetRelease, req, typeBudgetReleaseReply, &reply)
}

// Drop retires a waiter that gave up, for the same reason and with the same tolerance.
func (d DaemonAdmitter) Drop(ctx context.Context, waiter string) {
	var reply budgetReleaseReply
	req := budgetReleaseRequest{Magic: budgetMagic, Protocol: protocolV2, Waiter: waiter}
	_ = d.call(ctx, typeBudgetRelease, req, typeBudgetReleaseReply, &reply)
}

func (d DaemonAdmitter) call(ctx context.Context, reqType string, req any, wantType string, reply any) error {
	ep, err := endpoint.Parse(d.Addr)
	if err != nil {
		return fmt.Errorf("proc: %s: invalid address: %w", reqType, err)
	}
	// The timeout covers DIAL as well as the exchange. A daemon whose accept queue is
	// full is not dead - the socket file is there and the connection simply never
	// completes - so a dial outside the bound hangs the caller for as long as the daemon
	// stays sick, which for a release means holding a local limiter slot the whole time.
	ctx, cancel := context.WithTimeout(ctx, admitTimeout)
	defer cancel()

	conn, err := ep.Dial(ctx)
	if err != nil {
		return fmt.Errorf("proc: %s: dial %s: %w", reqType, ep, err)
	}
	defer func() { _ = conn.Close() }()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	if err := writeFrame(conn, reqType, req); err != nil {
		return fmt.Errorf("proc: %s: write: %w", reqType, err)
	}
	typ, line, err := readFrameCtx(ctx, conn)
	if err != nil {
		return fmt.Errorf("proc: %s: read: %w", reqType, err)
	}
	if typ == typeError {
		var er errorReply
		if e := json.Unmarshal(line, &er); e == nil && er.Message != "" {
			return fmt.Errorf("proc: %s: server error: %s", reqType, er.Message)
		}
		return fmt.Errorf("proc: %s: server error (undecodable)", reqType)
	}
	if typ != wantType {
		return fmt.Errorf("proc: %s: unexpected reply type %q", reqType, typ)
	}
	if err := json.Unmarshal(line, reply); err != nil {
		return fmt.Errorf("proc: %s: decode reply: %w", reqType, err)
	}
	return nil
}
