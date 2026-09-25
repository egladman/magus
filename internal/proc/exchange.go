package proc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/proc/endpoint"
)

// exchange names one client call on a proc socket: the label its errors carry, its method
// and path, and its bound.
//
// timeout > 0 bounds DIAL as well as the exchange. A server whose accept queue is full is
// not dead (the socket file is there and the connection simply never completes), so a
// dial outside the bound hangs the caller for as long as the server stays sick. A zero
// timeout leaves the caller's ctx as the only bound, for exchanges that legitimately wait
// on the server, a forwarded run above all.
type exchange struct {
	op      string
	method  string
	path    string
	timeout time.Duration
}

var (
	runExchange          = exchange{op: "forward", method: http.MethodPost, path: pathRun}
	statusExchange       = exchange{op: "query", method: http.MethodGet, path: pathStatus, timeout: statusQueryTimeout}
	jobExchange          = exchange{op: "job", method: http.MethodPost, path: pathJobs, timeout: statusQueryTimeout}
	shutdownExchange     = exchange{op: "shutdown", method: http.MethodPost, path: pathShutdown}
	configReloadExchange = exchange{op: "config.reload", method: http.MethodPost, path: pathReload}
)

// ctxCause reports the context's error alongside err when the context is what ended the
// exchange. The connection carries ctx's deadline, and its timer can fire before ctx marks
// itself done, so a deadline error on a ctx that HAS a deadline is that deadline, whether or
// not ctx.Err has caught up. A caller testing for context.DeadlineExceeded must not see a
// bare i/o timeout on the runs where the socket won.
func ctxCause(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%w: %w", ctxErr, err)
	}
	if _, ok := ctx.Deadline(); ok && errors.Is(err, os.ErrDeadlineExceeded) {
		return fmt.Errorf("%w: %w", context.DeadlineExceeded, err)
	}
	return err
}

// roundTrip sends req (nil for none) to the server at addr as x and decodes the reply.
// Errors read "proc: <op>: ...". A refusal carries the server's message rebuilt as the typed
// error it names (decodeWireError), so errors.Is and NotAdopted see through it; a server on
// the old line protocol is MGS3025.
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

	var body io.Reader
	if req != nil {
		raw, err := json.Marshal(req)
		if err != nil {
			return reply, fmt.Errorf("proc: %s: encode: %w", x.op, err)
		}
		body = bytes.NewReader(raw)
	}
	hreq, err := http.NewRequestWithContext(ctx, x.method, socketURL(x.path), body)
	if err != nil {
		return reply, fmt.Errorf("proc: %s: %w", x.op, err)
	}
	if req != nil {
		hreq.Header.Set("Content-Type", "application/json")
	}
	resp, err := socketClient(ep).Do(hreq)
	if err != nil {
		return reply, fmt.Errorf("proc: %s: %s: %w", x.op, ep, ctxCause(ctx, outdated(ep.String(), err)))
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return reply, fmt.Errorf("proc: %s: read: %w", x.op, ctxCause(ctx, err))
	}
	if resp.StatusCode/100 != 2 {
		return reply, fmt.Errorf("proc: %s: %w", x.op, decodeWireError(refusalMessage(raw)))
	}
	if err := json.Unmarshal(raw, &reply); err != nil {
		return reply, fmt.Errorf("proc: %s: decode reply: %w", x.op, err)
	}
	return reply, nil
}
