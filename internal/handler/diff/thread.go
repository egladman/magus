package diff

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/handler"
	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/review"
	"github.com/egladman/magus/internal/rpcerr"
	"github.com/egladman/magus/types"
)

// threadLookupTimeout bounds the forge calls one thread read makes, and only those. A client
// asked for one thread and must not wait on a stranger's outage to get it; the annotation that
// follows is this server's own work and runs as long as it takes.
const threadLookupTimeout = 5 * time.Second

// threadSource is what the thread route reads from the workspace: where this tree's changes are
// discussed, the patch, and the annotated changeset the thread is read against.
type threadSource interface {
	reviewSource
	DiffWith(ctx context.Context, paths []string, opts types.DiffOptions) (types.Diff, error)
}

// ThreadOptions wires a [ThreadHandler].
type ThreadOptions struct {
	// Workspace is where the tree's changes are read from. Nil answers that no review is open,
	// which is what a server with no workspace has.
	Workspace threadSource
	// Anchors joins the workspace's notes stores against the changeset. Nil is a server with no
	// notes wiring, and the record then names note anchors among what it could not measure.
	Anchors func(ctx context.Context, rev types.Diff) ([]review.AnchorHit, error)
}

// ThreadReply is the route's answer: the record `magus diff --thread -o json` prints, and Text,
// the same record as `magus diff --thread` prints it, which the console's copy button copies.
type ThreadReply struct {
	types.DiffThread
	Text string `json:"text"`
}

// ThreadHandler serves GET /api/v1/diff/thread?id=<thread id>: one review thread with its hunk
// and what the change there reaches. It posts nothing to the host.
//
// Its own route rather than a field of the review lookup, for the reason that lookup has one:
// it costs the changeset annotation on top of a forge round trip, and the thread must paint
// before anything that annotates is allowed to hold it up.
type ThreadHandler struct {
	handler.Base
	opts ThreadOptions
}

// NewThreadHandler returns the GET /api/v1/diff/thread handler. A zero opts is valid: it answers
// that no review is open.
func NewThreadHandler(opts ThreadOptions, log *slog.Logger) *ThreadHandler {
	h := &ThreadHandler{opts: opts}
	h.Base = handler.New(h.serve, log)
	return h
}

func (h *ThreadHandler) serve(w http.ResponseWriter, r *http.Request) {
	if !handler.AllowGet(w, r) {
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		handler.Refuse(w, r, rpcerr.Invalid("thread requires an id parameter: the id of a thread's top-level comment"))
		return
	}
	ws := h.opts.Workspace
	if ws == nil {
		handler.Refuse(w, r, rpcerr.NotFound("no review is open: this server has no workspace"))
		return
	}

	ctx := r.Context()
	// The comments that decoded are used even when one did not: a malformed remark is no reason
	// to withhold the rest of a thread. The failure is named only when it left the id
	// unfindable, which is when it matters.
	at, comments, commentsErr := bindings.OriginReviewComments(ctx, ws.ReviewOrigin(ctx), threadLookupTimeout)
	if !at.Open() {
		handler.Refuse(w, r, rpcerr.NotFound("no review is open for this branch: "+at.Reason))
		return
	}
	patch, err := ws.WorkingDiff(ctx, nil)
	if err != nil {
		h.Fail(w, r, "thread", err)
		return
	}
	in, err := review.NewThreadInput(ctx, review.ThreadParts{
		Patch:    patch,
		Comments: comments,
		Annotate: func(ctx context.Context, paths []string) (types.Diff, error) {
			return ws.DiffWith(ctx, paths, types.DiffOptions{SkipOrder: true})
		},
		Anchors: h.opts.Anchors,
	})
	if err != nil {
		h.Fail(w, r, "thread", err)
		return
	}

	rec, err := review.ReadThread(in, id)
	switch {
	case errors.Is(err, changeset.ErrNoThread):
		msg := err.Error()
		if commentsErr != nil {
			msg += " (the review was only partly read: " + commentsErr.Error() + ")"
		}
		handler.Refuse(w, r, rpcerr.NotFound(msg))
	case err != nil:
		h.Fail(w, r, "thread", err)
	default:
		handler.WriteJSON(w, r, ThreadReply{DiffThread: rec, Text: strings.Join(review.ThreadLines(rec), "\n") + "\n"})
	}
}
