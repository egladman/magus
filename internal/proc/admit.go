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
	req := budgetAcquireRequest{Magic: budgetMagic, Protocol: protocolV2, Waiter: waiter, Claim: c}
	reply, err := roundTrip[budgetAcquireReply](ctx, d.Addr, budgetExchange(typeBudgetAcquire, typeBudgetAcquireReply), req)
	if err != nil {
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
	req := budgetReleaseRequest{Magic: budgetMagic, Protocol: protocolV2, ID: id}
	_, _ = roundTrip[budgetReleaseReply](ctx, d.Addr, budgetExchange(typeBudgetRelease, typeBudgetReleaseReply), req)
}

// Drop retires a waiter that gave up, for the same reason and with the same tolerance.
func (d DaemonAdmitter) Drop(ctx context.Context, waiter string) {
	req := budgetReleaseRequest{Magic: budgetMagic, Protocol: protocolV2, Waiter: waiter}
	_, _ = roundTrip[budgetReleaseReply](ctx, d.Addr, budgetExchange(typeBudgetRelease, typeBudgetReleaseReply), req)
}

// budgetExchange is a machine-budget call, bounded by admitTimeout as it stands at the call.
func budgetExchange(request, reply string) exchange {
	return exchange{op: request, request: request, reply: reply, timeout: admitTimeout}
}

// exchange names one client call on the daemon socket: the label its errors carry, the
// frame types it sends and expects back, and its bound.
//
// timeout > 0 bounds DIAL as well as the exchange. A daemon whose accept queue is full is
// not dead (the socket file is there and the connection simply never completes), so a
// dial outside the bound hangs the caller for as long as the daemon stays sick, which for
// a release means holding a local limiter slot the whole time. A zero timeout leaves the
// caller's ctx as the only bound, for exchanges that legitimately wait on the daemon.
type exchange struct {
	op      string
	request string
	reply   string
	timeout time.Duration
}

var (
	statusExchange         = exchange{op: "query", request: typeStatus, reply: typeStatusReply, timeout: statusQueryTimeout}
	jobExchange            = exchange{op: "job", request: typeJob, reply: typeJobReply, timeout: statusQueryTimeout}
	shutdownExchange       = exchange{op: "shutdown", request: typeShutdown, reply: typeShutdownReply}
	serviceAcquireExchange = exchange{op: "service.acquire", request: typeServiceAcquire, reply: typeServiceAcquireReply}
	// Release is quick bookkeeping on the daemon (drop a ref, arm the idle timer), so it is
	// bounded: a wedged daemon must not block the run's teardown forever.
	serviceReleaseExchange = exchange{op: "service.release", request: typeServiceRelease, reply: typeServiceReleaseReply, timeout: statusQueryTimeout}
	serviceStopAllExchange = exchange{op: "service.stopall", request: typeServiceStopAll, reply: typeServiceStopAllReply}
	configReloadExchange   = exchange{op: "config.reload", request: typeConfigReload, reply: typeConfigReloadReply}
)

// roundTrip sends req to the daemon at addr as x and decodes the reply. Errors read
// "proc: <op>: ...".
//
// Every one-request, one-reply client call goes through this; Forward alone does not,
// because its server error carries a wire-encoded error to rebuild.
func roundTrip[Reply any](ctx context.Context, addr string, x exchange, req any) (Reply, error) {
	var reply Reply
	ep, err := endpoint.Parse(addr)
	if err != nil {
		return reply, fmt.Errorf("proc: %s: invalid address: %w", x.op, err)
	}
	if x.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, x.timeout)
		defer cancel()
	}

	conn, err := ep.Dial(ctx)
	if err != nil {
		return reply, fmt.Errorf("proc: %s: dial %s: %w", x.op, ep, err)
	}
	defer func() { _ = conn.Close() }()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	if err := writeFrame(conn, x.request, req); err != nil {
		return reply, fmt.Errorf("proc: %s: write: %w", x.op, err)
	}
	typ, line, err := readFrameCtx(ctx, conn)
	if err != nil {
		return reply, fmt.Errorf("proc: %s: read: %w", x.op, err)
	}
	if typ == typeError {
		var er errorReply
		if e := json.Unmarshal(line, &er); e == nil && er.Message != "" {
			return reply, fmt.Errorf("proc: %s: server error: %s", x.op, er.Message)
		}
		return reply, fmt.Errorf("proc: %s: server error (undecodable)", x.op)
	}
	if typ != x.reply {
		return reply, fmt.Errorf("proc: %s: unexpected reply type %q", x.op, typ)
	}
	if err := json.Unmarshal(line, &reply); err != nil {
		return reply, fmt.Errorf("proc: %s: decode reply: %w", x.op, err)
	}
	return reply, nil
}
