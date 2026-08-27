package status

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/egladman/magus/internal/handler"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/sessions"
)

// consoleSessionHost stamps the disposing session as having been driven by the console.
//
// sessions.SessionStart.Host names the surface that drove a session, and the CLI leaves it
// empty because nothing on its run path knows the answer - "not known", never "a human".
// This route DOES know: it is reachable only from the console, so the surface is a fact at
// the moment of the write rather than something inferred later from an absence. Recording
// it positively is what keeps that inference from being needed at all.
//
// The value is not an agent host ("claude", "cursor", ...) and is not meant to be mistaken
// for one. It says a person acted through their own surface, which is the whole distinction
// docs/doctrine.md's "Manual on purpose" row turns on.
const consoleSessionHost = "console"

// AttentionHandler serves /api/v1/attention: the blocks agents have raised in this repository
// and waiting on a person, plus the one write that closes one.
//
// GET answers {"requests":[...],"store":"<dir>"} - the same shape `magus session attention -o json`
// prints, so the console and the terminal cannot come to describe one queue differently. The
// requests are sessions.AttentionRequest values, which is what makes the two identical by
// construction rather than by review.
//
// POST {"id":"<id or prefix>","reason":"<text>"} disposes one, and is a HUMAN act. Nothing
// magus does closes a request: an event whose whole meaning is "blocked on a person" stops
// meaning that the moment the tool answers it (docs/doctrine.md, "Manual on purpose"). This
// route exists because the console IS the person's surface, alongside the CLI - not as an
// automation door. There is deliberately no dispose-all, no expiry and no filter that could
// clear the queue without reading it.
//
// Authorship rides the ROUTE, never the payload, the same rule DiffSessionHandler states: an
// agent reaches magus through MCP and cannot arrive here, so a write that lands here came from
// the console and is stamped [consoleSessionHost] without trusting anything the caller sent.
type AttentionHandler struct {
	handler.Base
	root    string
	version string
}

// NewAttentionHandler returns the /api/v1/attention handler for the repository at root.
//
// The store is resolved per request from root rather than once here: sessions.Dir is a hash
// and a path join, and resolving it live keeps the handler saying what the CLI would say from
// the same checkout even if the state directory moves under a long-lived daemon.
func NewAttentionHandler(root, version string, log *slog.Logger) *AttentionHandler {
	h := &AttentionHandler{root: root, version: version}
	h.Base = handler.New(h.serve, log)
	return h
}

// attentionView mirrors cmd/magus's attentionListOutput field for field.
//
// Mirrored rather than shared because the CLI's copy lives in package main, which nothing can
// import. The REQUESTS are the same sessions.AttentionRequest type, so the rows - every field
// a reader acts on - cannot drift; only this two-field envelope is duplicated, and it is
// duplicated in the one direction that matters, with the CLI as the original.
type attentionView struct {
	Requests []sessions.AttentionRequest `json:"requests"`
	Store    string                      `json:"store"`
}

// disposeRequestBody is the wire shape of a disposal. Reason is optional and free text: the
// store keeps it as the note beside the disposition, and a person closing a request they
// understand should not be forced to narrate it.
type disposeRequestBody struct {
	ID     string `json:"id"`
	Reason string `json:"reason,omitempty"`
}

func (h *AttentionHandler) serve(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodOptions:
		w.WriteHeader(http.StatusNoContent)
	case http.MethodGet:
		h.list(w)
	case http.MethodPost:
		h.dispose(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *AttentionHandler) list(w http.ResponseWriter) {
	dir, err := sessions.Dir(h.root)
	if err != nil {
		http.Error(w, "attention error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	fold, err := sessions.ReadAll(dir)
	if err != nil {
		http.Error(w, "attention error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// A repository nobody has raised a block in serves "requests":[] rather than null - an
	// empty queue is the GOOD state and the surface renders a list either way. AttentionQueue
	// already returns an empty slice rather than a nil one, so this needs no normalizing.
	handler.WriteJSON(w, attentionView{Requests: sessions.AttentionQueue(fold), Store: dir})
}

func (h *AttentionHandler) dispose(w http.ResponseWriter, r *http.Request) {
	var body disposeRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	dir, err := sessions.Dir(h.root)
	if err != nil {
		http.Error(w, "attention error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	req, err := sessions.DisposeRequest(dir, body.ID, body.Reason, sessions.SessionStart{
		Host:      consoleSessionHost,
		Workspace: h.root,
		Version:   h.version,
	})
	if err != nil {
		disposeStatus(w, err, body.ID)
		return
	}
	// The disposed request as the store now reads it, matching what `magus session dispose
	// -o json` prints. The caller re-reads the queue on its next poll; answering with the row
	// that closed lets it say WHICH one closed without waiting for that.
	handler.WriteJSON(w, req)
}

// disposeStatus maps a refusal from sessions.DisposeRequest onto the status a client can act
// on. Each one is a different thing to do next, which is why they are not one 400: an id that
// matches nothing is gone (404), an id that matches several needs more characters (400), and
// one that is already closed is a state the caller has to re-read rather than retry (409).
func disposeStatus(w http.ResponseWriter, err error, ref string) {
	var (
		ambiguous *sessions.AmbiguousRequestError
		disposed  *sessions.DisposedError
	)
	switch {
	case errors.Is(err, sessions.ErrNoRequest):
		http.Error(w, "no attention request matches "+ref, http.StatusNotFound)
	case errors.As(err, &ambiguous):
		http.Error(w, ambiguous.Error(), http.StatusBadRequest)
	case errors.As(err, &disposed):
		http.Error(w, disposed.Error(), http.StatusConflict)
	default:
		http.Error(w, "attention error: "+err.Error(), http.StatusInternalServerError)
	}
}
