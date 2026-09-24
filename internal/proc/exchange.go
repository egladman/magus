package proc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/proc/endpoint"
)

// exchange names one client call on a proc socket: the label its errors carry, the
// frame types it sends and expects back, and its bound.
//
// timeout > 0 bounds DIAL as well as the exchange. A server whose accept queue is full is
// not dead (the socket file is there and the connection simply never completes), so a
// dial outside the bound hangs the caller for as long as the server stays sick. A zero
// timeout leaves the caller's ctx as the only bound, for exchanges that legitimately wait
// on the server.
type exchange struct {
	op      string
	request string
	reply   string
	timeout time.Duration
}

var (
	statusExchange       = exchange{op: "query", request: typeStatus, reply: typeStatusReply, timeout: statusQueryTimeout}
	jobExchange          = exchange{op: "job", request: typeJob, reply: typeJobReply, timeout: statusQueryTimeout}
	shutdownExchange     = exchange{op: "shutdown", request: typeShutdown, reply: typeShutdownReply}
	configReloadExchange = exchange{op: "config.reload", request: typeConfigReload, reply: typeConfigReloadReply}
)

// ctxCause reports the context's error alongside err when the context is what ended the
// exchange. The socket carries ctx's deadline, and its timer can fire before ctx marks
// itself done, so a socket deadline error on a ctx that HAS a deadline is that deadline,
// whether or not ctx.Err has caught up. A caller testing for context.DeadlineExceeded must
// not see a bare i/o timeout on the runs where the socket won.
func ctxCause(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%w: %w", ctxErr, err)
	}
	if _, ok := ctx.Deadline(); ok && errors.Is(err, os.ErrDeadlineExceeded) {
		return fmt.Errorf("%w: %w", context.DeadlineExceeded, err)
	}
	return err
}

// roundTrip sends req to the server at addr as x and decodes the reply. Errors read
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
		return reply, fmt.Errorf("proc: %s: write: %w", x.op, ctxCause(ctx, err))
	}
	typ, line, err := readFrameCtx(ctx, conn)
	if err != nil {
		return reply, fmt.Errorf("proc: %s: read: %w", x.op, ctxCause(ctx, err))
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
