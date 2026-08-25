package status

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/egladman/magus/internal/handler"
	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/types"
)

// DiffReviewHandler serves GET /api/v1/diff/review: which review is open for this tree, and
// the comment threads already on it.
//
// A THIRD route on the review surface rather than fields on the session, for the reason the
// patch/annotation split already gives: these two answers cost different amounts. The session
// is local state and returns in microseconds; this one crosses the network to a forge and can
// hang for as long as that forge feels like taking. Folding them together would hold the diff
// behind somebody else's outage.
//
// It never fails. No provider wired, no pull request, an unreachable host: all of them are a
// closed target with a reason, because the reader's options are identical in every case and a
// surface that rendered them as errors would be accusing them of something they did not do.
type DiffReviewHandler struct {
	handler.Base
	origin originSource
}

// NewDiffReviewHandler returns the review-lookup handler. origin may be nil, which reports no
// review rather than failing - a daemon with no workspace has no branch to look one up for.
func NewDiffReviewHandler(origin originSource, log *slog.Logger) *DiffReviewHandler {
	h := &DiffReviewHandler{origin: origin}
	h.Base = handler.New(h.serve, log)
	return h
}

// diffReviewResponse is the wire shape: the target, flattened, plus its threads.
//
// Threads is always an array, never null. A client rendering "what colleagues said" iterates
// it, and a null would make every caller write the same guard for a state that means exactly
// what an empty list means.
type diffReviewResponse struct {
	Number  int                  `json:"number"`
	Repo    string               `json:"repo,omitempty"`
	Reason  string               `json:"reason,omitempty"`
	Threads []types.ReviewThread `json:"threads"`
}

func (h *DiffReviewHandler) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	at := h.lookup(r.Context())
	out := diffReviewResponse{
		Number:  at.Number,
		Repo:    at.Repo,
		Reason:  at.Reason,
		Threads: []types.ReviewThread{},
	}
	if at.Open() {
		out.Threads = append(out.Threads, bindings.ReviewThreads(r.Context(), at)...)
	}
	writeJSON(w, out)
}

func (h *DiffReviewHandler) lookup(ctx context.Context) types.ReviewTarget {
	if h.origin == nil {
		return types.ReviewTarget{Reason: "no workspace"}
	}
	from := h.origin.ReviewOrigin(ctx)
	return bindings.OpenReview(ctx, from.Branch, from.Remote)
}
