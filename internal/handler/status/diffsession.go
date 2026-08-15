package status

import (
	"log/slog"
	"net/http"

	"github.com/egladman/magus/internal/diff"
	"github.com/egladman/magus/internal/handler"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// DiffSessionHandler serves POST /api/v1/diff/session: the human's half of a paired
// review - where they are looking, what they have read, and what they have said.
//
// Every write here is stamped DiffAuthorHuman, because this route is only reachable from
// the console and the CLI. The agent's half lives on the MCP surface and is stamped
// DiffAuthorAgent there. Authorship is decided by WHICH ROUTE the write arrived on and
// never by the payload, which is what makes it unforgeable: an agent cannot reach this
// handler, so it cannot post as the person.
//
// It is one route with an `op` rather than five, because these are all small mutations of one
// object and a client applies them from one place - a keypress handler. Five routes would be
// five fetch wrappers for no gain in clarity.
type DiffSessionHandler struct {
	handler.Base
	sessions *diff.Store
	root     string
}

// NewDiffSessionHandler returns the POST /api/v1/diff/session handler.
func NewDiffSessionHandler(sessions *diff.Store, root string, log *slog.Logger) *DiffSessionHandler {
	h := &DiffSessionHandler{sessions: sessions, root: root}
	h.Base = handler.New(h.serve, log)
	return h
}

// reviewSessionRequest is the wire shape. Op names the mutation; the rest are its arguments,
// and which ones matter depends on Op.
type reviewSessionRequest struct {
	// Op is one of: cursor, viewed, comment, resolve, answer.
	Op string `json:"op"`
	// cursor
	Path string `json:"path,omitempty"`
	Hunk int    `json:"hunk,omitempty"`
	// viewed
	Digest string `json:"digest,omitempty"`
	On     bool   `json:"on,omitempty"`
	// comment
	Body   string `json:"body,omitempty"`
	Anchor string `json:"anchor,omitempty"`
	// resolve / answer
	ID string `json:"id,omitempty"`
}

func (h *DiffSessionHandler) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req reviewSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	var sess *types.DiffSession
	switch req.Op {
	case "cursor":
		sess = h.sessions.SetCursor(h.root, types.DiffCursor{Path: req.Path, Hunk: req.Hunk})
	case "viewed":
		sess = h.sessions.MarkViewed(h.root, req.Digest, req.On)
	case "comment":
		sess = h.sessions.AddComment(h.root, types.DiffComment{
			Path: req.Path, Hunk: req.Hunk, Body: req.Body, Anchor: req.Anchor,
		}, types.DiffAuthorHuman)
	case "resolve":
		sess = h.sessions.ResolveComment(h.root, req.ID, req.On)
	case "answer":
		sess = h.sessions.AnswerSuggestion(h.root, req.ID, req.On)
	default:
		http.Error(w, "unknown op "+req.Op, http.StatusBadRequest)
		return
	}
	if sess == nil {
		// No session attached yet. 409 rather than 404: the ROUTE exists and the workspace is
		// fine, the client just has not read a review yet, and the fix is to fetch one.
		http.Error(w, "no review session attached; GET /api/v1/diff first", http.StatusConflict)
		return
	}
	writeJSON(w, sess)
}
