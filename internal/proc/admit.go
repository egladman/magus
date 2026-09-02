package proc

import (
	"context"
	"fmt"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/proc/endpoint"
)

// admitTimeout caps one admission round-trip. The budget never blocks, so a slow
// answer means a wedged daemon rather than a busy machine, and a run must not queue
// behind one forever: the client treats a timeout as no arbiter and proceeds.
const admitTimeout = 5 * time.Second

// MachineAdmitter reaches the machine budget in the daemon at Addr. It is
// cache.MachineAdmitter over the proc socket, and the only thing that makes a magus
// running here answerable to a magus running in another worktree.
type MachineAdmitter struct{ Addr string }

// Request polls the budget on behalf of waiter.
func (m MachineAdmitter) Request(ctx context.Context, waiter string, c cache.MachineClaim) (cache.MachineVerdict, error) {
	var reply admitReply
	err := m.call(ctx, typeAdmit, admitRequest{Protocol: protocolV2, Waiter: waiter, Claim: c}, typeAdmitReply, &reply)
	if err != nil {
		return cache.MachineVerdict{}, err
	}
	if reply.Err != "" {
		return cache.MachineVerdict{}, fmt.Errorf("proc: admit: %s", reply.Err)
	}
	return reply.Verdict, nil
}

// Release returns a granted claim. Errors are dropped: a release that cannot be
// delivered is retired by the budget's own liveness reap, and a teardown must not fail
// over bookkeeping.
func (m MachineAdmitter) Release(ctx context.Context, id string) {
	var reply admitReleaseReply
	_ = m.call(ctx, typeAdmitRelease, admitReleaseRequest{Protocol: protocolV2, ID: id}, typeAdmitReleaseReply, &reply)
}

// Drop retires a waiter that gave up, for the same reason and with the same tolerance.
func (m MachineAdmitter) Drop(ctx context.Context, waiter string) {
	var reply admitReleaseReply
	_ = m.call(ctx, typeAdmitRelease, admitReleaseRequest{Protocol: protocolV2, Waiter: waiter}, typeAdmitReleaseReply, &reply)
}

func (m MachineAdmitter) call(ctx context.Context, reqType string, req any, wantType string, reply any) error {
	ep, err := endpoint.Parse(m.Addr)
	if err != nil {
		return fmt.Errorf("proc: %s: invalid address: %w", reqType, err)
	}
	conn, err := ep.Dial(ctx)
	if err != nil {
		return fmt.Errorf("proc: %s: dial %s: %w", reqType, ep, err)
	}
	defer func() { _ = conn.Close() }()

	deadline := time.Now().Add(admitTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)

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
