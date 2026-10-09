package diff

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/egladman/magus/internal/handler"
	"github.com/egladman/magus/internal/review"
	"github.com/egladman/magus/internal/rpcerr"
)

// readingResponse is what the reading op answers.
type readingResponse struct {
	// Reading is whether a mark is set after this request.
	Reading bool `json:"reading"`
	// Since is when the reading began, in unix milliseconds. Zero when Reading is false.
	Since int64 `json:"since,omitzero"`
	// Command is the one line the person may run to tell the forge, or empty when magus does not
	// know how to say it on this one. magus prints it and never runs it.
	Command string `json:"command,omitempty"`
}

// reading marks, or unmarks, the review as being read right now.
//
// It needs no attached session: the mark belongs to the review on the host, which a reader may
// hold open before any hunk is fetched.
//
// The mark is local: it makes a merge that lands under the reader reportable (see the
// check-review job) and measurable. It is never sent to the forge by magus. The command in the
// response is for the person to run if they want colleagues to see it, so that the one sentence
// that leaves the machine is typed by a person, like every other.
//
// Marking a review that has already merged is refused rather than recorded: the next job tick
// would report a merge "under" a reader who started after it.
func (h *ReviewHandler) reading(w http.ResponseWriter, r *http.Request, req reviewSessionRequest) {
	ctx := r.Context()
	if !req.On {
		if err := h.Sessions.ClearReading(ctx); err != nil {
			h.Log.WarnContext(ctx, "diff session: could not clear the reading mark", slog.String("error", err.Error()))
			handler.Refuse(w, r, rpcerr.Internal("clearing the reading mark"))
			return
		}
		handler.WriteJSON(w, r, readingResponse{})
		return
	}
	at, err := h.findReview(ctx)
	if err != nil {
		handler.Refuse(w, r, rpcerr.ReviewHostFailed("reading: "+err.Error()))
		return
	}
	if at.Merged() {
		handler.Refuse(w, r, rpcerr.Conflict("review "+at.ID+" has already merged, so there is nothing left to hold"))
		return
	}
	mark, err := h.Sessions.SetReading(ctx, at, time.Now())
	if err != nil {
		h.Log.WarnContext(ctx, "diff session: could not record the reading mark", slog.String("error", err.Error()))
		handler.Refuse(w, r, rpcerr.Internal("recording the reading mark"))
		return
	}
	handler.WriteJSON(w, r, readingResponse{
		Reading: true,
		Since:   mark.Since,
		Command: review.ReadingCommand(at, remoteHost(h.Workspace.ReviewOrigin(ctx).Remote)),
	})
}
