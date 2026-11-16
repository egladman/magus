package difftui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"syscall"
	"time"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/libs/secrets"
	"github.com/egladman/magus/types/diff"
)

// Bridge is the running server's session, reached over the same loopback routes the
// console uses. The session it attached to travels with it, because a transport that could
// hand back a session it had not attached would be a client of nothing.
//
// Writes leave on sends rather than on the caller's stack: every one of them is provoked by a
// KEYPRESS (a cursor move, a read mark), and an inline post would put a network round trip
// between the key and the screen moving, up to the full bridgeWrite deadline against a
// server that has stopped answering.
//
// The two kinds of write get two queues, because they fail in opposite directions. A cursor is
// idempotent by REPLACEMENT (the newest one says where the reader is and everything behind it
// says where they no longer are), so that queue evicts to stay current. A read mark is
// replaceable by nothing: the bridge is the only writer of it on this path, so a dropped
// `viewed` is a hunk the reader read that no client will ever be told about. Its queue is deep,
// waited on, and drained in full at close.
type Bridge struct {
	addr    string
	token   secrets.Secret
	tokenID string // revoked at close; empty when the bridge minted no record
	tokens  Tokens
	session *diff.Review

	cursors chan sessionOp
	marks   chan sessionOp
	// stop is closed in place of the op channels, so a send racing close cannot panic.
	stop chan struct{}
	// done closes when deliver has returned, which is the only proof the sender is gone.
	done chan struct{}
	// cancel aborts the post on the wire once close has spent its budget.
	cancel context.CancelFunc
}

// AttachTimeout bounds the GET that annotates and attaches. It is the expensive route
// (symbol shards and a reverse closure), so this is generous; the alternative on a slow answer
// is not a faster one, it is computing the same thing locally.
const AttachTimeout = 10 * time.Second

// bridgeWrite bounds a coordination write. Short, because a wedged server must not hold
// the sender long enough for the queue behind it to overflow, and because it is also how long
// quitting waits for the last mark to leave.
const bridgeWrite = time.Second

// bridgeQueue bounds the CURSOR writes waiting to leave. Small on purpose: a backlog means
// the server has stopped keeping up, and at that point the newest cursor is the only one worth
// having: the ones behind it describe somewhere the reader no longer is.
const bridgeQueue = 8

// bridgeMarks bounds the read marks waiting to leave. Deep rather than small, because
// nothing here may be evicted and the volume is bounded by a human pressing `v`: reaching the
// end of it takes a server that has stopped answering AND a minute of uninterrupted marking.
const bridgeMarks = 64

// bridgeClose caps how long quitting waits for the queue to empty. The budget is one write
// deadline per queued op, and this is the ceiling on it: a wedged server costs the shell a few
// seconds rather than the whole queue's worth of deadlines.
const bridgeClose = 3 * time.Second

// DialBridge attaches to the server's session, or returns nil when there is nothing to
// join. A refused connection stays silent: no server was started.
// Every other miss warns, so a server that is up is never read as absent.
//
// asOf is the digest of the patch about to be rendered, and the session is DECLINED unless
// the server computed its changeset from the same bytes. Without that check a server serving
// a different workspace (the main checkout while this is a worktree) answers confidently
// about a tree the reader is not looking at, and the coordinate every comment and every
// viewed mark is keyed by would silently mean something else.
func DialBridge(ctx context.Context, tk Tokens, addr string, paths []string, asOf string) *Bridge {
	warn := func(msg string, why ...any) {
		slog.WarnContext(ctx, msg, append([]any{attr.Notice(""), attr.Component("magus")}, why...)...)
	}
	token, tokenID, ok := MintToken(ctx, tk, addr, bridgeTokenTTL)
	if !ok {
		return nil
	}
	joined := false
	defer func() {
		if !joined {
			RevokeToken(ctx, tk, tokenID)
		}
	}()
	q := url.Values{}
	for _, p := range paths {
		q.Add("path", p)
	}
	b := &Bridge{addr: addr, token: token, tokenID: tokenID, tokens: tk}
	ctx, cancel := context.WithTimeout(ctx, AttachTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://%s/api/v1/diff?%s", b.addr, q.Encode()), nil)
	if err != nil {
		return nil
	}
	SetBearer(req, token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if !errors.Is(err, syscall.ECONNREFUSED) {
			warn("the review is not shared with the server: it did not answer", attr.Error(err))
		}
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		warn("the review is not shared with the server: it refused the session", attr.Why(resp.Status))
		return nil
	}
	var sess diff.Review
	if err := json.NewDecoder(resp.Body).Decode(&sess); err != nil {
		warn("the review is not shared with the server: its session did not decode", attr.Error(err))
		return nil
	}
	if sess.AsOf != asOf {
		warn("the review is not shared with the server: it computed its changeset from a different patch", attr.Why(fmt.Sprintf("as-of %s, this patch is %s", sess.AsOf, asOf)))
		return nil
	}
	b.session = &sess
	b.start()
	joined = true
	return b
}

// start wires the sender. Split from DialBridge so the queue can be driven against a stub
// server without a server, a token, or the attach round trip.
func (b *Bridge) start() {
	b.cursors = make(chan sessionOp, bridgeQueue)
	b.marks = make(chan sessionOp, bridgeMarks)
	b.stop = make(chan struct{})
	b.done = make(chan struct{})
	// Not the caller's context and not the session's: a cancelled read must not turn the last
	// read mark into a lost one. close owns this cancel and nothing else does.
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel
	go b.deliver(ctx)
}

// sessionOp is one mutation of the shared session, the wire shape the server's
// /api/v1/diff/session route takes.
type sessionOp struct {
	Op     string `json:"op"`
	Path   string `json:"path,omitempty"`
	Hunk   int    `json:"hunk,omitempty"`
	Digest string `json:"digest,omitempty"`
	On     bool   `json:"on,omitempty"`
	// IDs are the host thread ids of a `seen` op, the only op that carries a set rather than
	// one coordinate, because the watermark advances by what a frame showed.
	IDs []string `json:"ids,omitempty"`
}

// SetCursor publishes where the reader is looking. The reply is discarded on purpose: it
// carries the session's own cursor, and applying that would let another client move this
// reader's viewport, which is the one thing the paired-review design forbids.
func (b *Bridge) SetCursor(c diff.Cursor) {
	b.queueCursor(sessionOp{Op: "cursor", Path: c.Path, Hunk: c.Hunk})
}

// SetViewed publishes a read mark, which the server persists for every client at once.
func (b *Bridge) SetViewed(digest string, on bool) {
	b.queueMark(sessionOp{Op: "viewed", Digest: digest, On: on})
}

// SetThreadsSeen publishes the watermark on the MARK queue, not the cursor one: like a read
// mark it is replaceable by nothing, and the viewer sends each thread once, so a dropped op is
// a remark the reader saw that no client will ever be told about.
func (b *Bridge) SetThreadsSeen(ids []string) {
	b.queueMark(sessionOp{Op: "seen", IDs: ids})
}

// queueCursor hands the newest cursor to the sender, evicting the OLDEST when the queue is
// full. It never blocks: the key loop is what calls it, and Sync promises best-effort
// delivery precisely so a slow server costs the reader nothing.
//
// A plain non-blocking send drops the ARRIVING op instead, which keeps exactly the positions
// the reader has already walked past and throws away the only one still true.
//
// Evicting and sending are two steps, and deliver can take the head between them, which is why
// this retries rather than assuming the receive made room. Each iteration ends with a slot free
// whichever way that race went, and stop short-circuits both selects once close has run.
func (b *Bridge) queueCursor(op sessionOp) {
	for {
		select {
		case b.cursors <- op:
			return
		case <-b.stop:
			return
		default:
		}
		select {
		case <-b.cursors:
		case <-b.stop:
			return
		default:
		}
	}
}

// queueMark hands a read mark to the sender. It never evicts, because there is nothing to
// replace a mark with; see Bridge.
//
// A full queue is waited on rather than dropped, and the wait is bounded at bridgeWrite
// because an unbounded one would DEADLOCK the quit path: close runs after the key loop returns,
// so a key loop parked in here is one that can never reach the drain that would empty the queue.
//
// Past that wait the mark is lost and there is no honest floor below it on this path: the server
// owns the session file, so writing the local store instead would be a second writer persisting
// its own idea of the whole set, and stderr belongs to the viewport until the reader quits.
func (b *Bridge) queueMark(op sessionOp) {
	select {
	case b.marks <- op:
		return
	default:
	}
	t := time.NewTimer(bridgeWrite)
	defer t.Stop()
	select {
	case b.marks <- op:
	case <-b.stop:
	case <-t.C:
	}
}

// deliver posts queued mutations one at a time, in order, until close stops it, then drains
// what is left behind. It is the only receiver on either queue and it returns on every path: the
// loop leaves on stop, and the drain is bounded by the depth it measured on entry.
func (b *Bridge) deliver(ctx context.Context) {
	defer close(b.done)
	for {
		select {
		case op := <-b.marks:
			b.post(ctx, op)
		case op := <-b.cursors:
			b.post(ctx, op)
		case <-b.stop:
			b.drain(ctx)
			return
		}
	}
}

// drain posts what was waiting when close ran, marks first because those are the writes that
// cannot be reconstructed, and never more than were queued at that moment: a send racing close
// is not worth extending the shell's exit for.
func (b *Bridge) drain(ctx context.Context) {
	for n := len(b.marks) + len(b.cursors); n > 0; n-- {
		select {
		case op := <-b.marks:
			b.post(ctx, op)
			continue
		default:
		}
		select {
		case op := <-b.cursors:
			b.post(ctx, op)
		default:
			return
		}
	}
}

// Close stops the sender and gives what is already queued a bounded chance to leave: a mark
// made on the last keypress should not be lost to the process exiting, and a server that has
// stopped answering should not hold the shell either.
//
// Spending the budget CANCELS the sender rather than just abandoning it, which is what aborts
// the post already on the wire. Left uncancelled, the goroutine went on talking to the server
// about a session the reader had walked away from, with nothing left to receive the answer.
func (b *Bridge) Close() {
	defer func() {
		if b.tokenID != "" {
			RevokeToken(context.Background(), b.tokens, b.tokenID)
		}
	}()
	defer b.cancel() // the drain finished inside its budget; nothing is on the wire to abort
	close(b.stop)
	select {
	case <-b.done:
		return
	case <-time.After(b.drainBudget()):
	}
	b.cancel()
	select {
	case <-b.done:
	case <-time.After(bridgeWrite):
	}
}

// drainBudget is how long close waits for the queue: one write deadline per op, since they leave
// one at a time, plus one for whatever is already on the wire, capped at bridgeClose.
func (b *Bridge) drainBudget() time.Duration {
	d := time.Duration(len(b.marks)+len(b.cursors)+1) * bridgeWrite
	if d > bridgeClose {
		return bridgeClose
	}
	return d
}

// post sends one mutation, best-effort. A coordination write that fails is a pairing that
// went quiet, not a review that has to stop, and there is nothing useful to say about it to
// somebody in the middle of reading a diff.
//
// ctx is the SENDER's, never the session's: a cancelled read must not turn the last write into a
// lost mark. close cancels it once the drain budget is spent, so a post outlives the process's
// interest in it by nothing.
func (b *Bridge) post(ctx context.Context, op sessionOp) {
	body, err := json.Marshal(op)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, bridgeWrite)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://%s/api/v1/diff/session", b.addr), bytes.NewReader(body))
	if err != nil {
		return
	}
	SetBearer(req, b.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	// Drained so the connection can be reused for the next keypress rather than torn down.
	_, _ = io.Copy(io.Discard, resp.Body) // a failed drain costs only the connection's reuse
}

// Session is the review the server's session answered with.
func (b *Bridge) Session() *diff.Review { return b.session }
