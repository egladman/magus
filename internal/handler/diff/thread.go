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
	"github.com/egladman/magus/internal/prompt"
	"github.com/egladman/magus/internal/review"
	"github.com/egladman/magus/internal/rpcerr"
	"github.com/egladman/magus/types"
)

// threadLookupTimeout bounds the two forge calls one brief makes. A client asked for a text
// and must not wait on a stranger's outage to get it.
const threadLookupTimeout = 5 * time.Second

// threadSource is what the brief route reads from the workspace: where this tree's changes are
// discussed, the patch, and the annotated changeset the conversation is read against.
type threadSource interface {
	reviewSource
	Diff(ctx context.Context, paths []string) (types.Diff, error)
}

// ThreadHandler serves GET /api/v1/diff/thread?id=<root comment id>: the brief a person pastes
// to their own model to ask about one review conversation.
//
// The answer is {"id", "brief"} and nothing more. The route only renders text: it posts nothing
// to the host and sends nothing to a model, and the console shows the text with a copy
// affordance the person uses, because carrying it across is theirs to do.
//
// Its own route rather than a field of the review lookup, for the reason that lookup has one:
// it costs the changeset annotation on top of a forge round trip, and the conversation must
// paint before anything that annotates is allowed to hold it up.
type ThreadHandler struct {
	handler.Base
	workspace threadSource
	// Anchors joins the workspace's notes stores against the changeset. Nil is a server with no
	// notes wiring, and the brief then names note anchors among what it could not measure.
	Anchors func(ctx context.Context, rev types.Diff) []review.AnchorHit
}

// NewThreadHandler returns the conversation-brief handler. A nil workspace answers that no
// review is open, which is what a server with no workspace has.
func NewThreadHandler(workspace threadSource, log *slog.Logger) *ThreadHandler {
	h := &ThreadHandler{workspace: workspace}
	h.Base = handler.New(h.serve, log)
	return h
}

func (h *ThreadHandler) serve(w http.ResponseWriter, r *http.Request) {
	if !handler.AllowGet(w, r) {
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		handler.Refuse(w, r, rpcerr.Invalid("thread requires an id parameter: the root comment id of the conversation"))
		return
	}
	if h.workspace == nil {
		handler.Refuse(w, r, rpcerr.NotFound("no review is open: this server has no workspace"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), threadLookupTimeout)
	defer cancel()
	from := h.workspace.ReviewOrigin(ctx)
	at := bindings.FindReview(ctx, from.Branch, from.Remote)
	if !at.Open() {
		handler.Refuse(w, r, rpcerr.NotFound("no review is open for this branch: "+at.Reason))
		return
	}
	// The threads that decoded are used even when one did not: a malformed remark is no reason
	// to withhold the rest of a conversation. The failure is named only when it left the id
	// unfindable, which is when it matters.
	threads, threadsErr := bindings.ReviewThreads(ctx, at)

	in := review.ThreadInput{Threads: threads, Variant: prompt.Short}
	patch, err := h.workspace.WorkingDiff(ctx, nil)
	if err != nil {
		h.Fail(w, r, "thread", err)
		return
	}
	in.Hunks = changeset.ParseHunks(patch)
	if len(in.Hunks) > 0 {
		paths := make([]string, 0, len(in.Hunks))
		for _, f := range in.Hunks {
			paths = append(paths, f.Path)
		}
		rev, derr := h.workspace.Diff(ctx, paths)
		if derr != nil {
			h.Fail(w, r, "thread", derr)
			return
		}
		in.Changeset = rev
	}
	switch {
	case h.Anchors != nil:
		in.Anchors = h.Anchors(ctx, in.Changeset)
	default:
		in.AnchorsUnread = "this server has no notes store wired, so none was joined"
	}

	reply, err := review.ThreadBriefFor(in, id)
	switch {
	case errors.Is(err, review.ErrNoConversation):
		msg := err.Error()
		if threadsErr != nil {
			msg += " (the review was only partly read: " + threadsErr.Error() + ")"
		}
		handler.Refuse(w, r, rpcerr.NotFound(msg))
	case err != nil:
		h.Fail(w, r, "thread", err)
	default:
		handler.WriteJSON(w, r, reply)
	}
}
