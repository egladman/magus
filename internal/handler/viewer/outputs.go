package viewer

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/handler"
)

// OutputSource is the narrow repository contract the run-browser handlers need: list the stored run
// descriptors, and read one run's captured bytes by ref. Satisfied by *cache.OutputStore, so the
// handler package never grows its own store logic - it just serves what the store already knows.
type OutputSource interface {
	ListDescriptors() []cache.OutputDescriptor
	ByRef(ref string) ([]byte, cache.OutputDescriptor, error)
}

// runSummary is the JSON shape of one stored run in the browser feed. It is an explicit wire DTO (not
// the on-disk cache.OutputDescriptor) so the store's layout can evolve without changing what the
// console's run tree reads. Times stay unix milliseconds (the descriptor's own unit); the browser
// formats them. Target is the REPRO target verbatim (a charm suffix like "build:rw" is preserved) so
// a run's exact invocation stays visible; the console collapses to the bare name for grouping.
type runSummary struct {
	Ref         string `json:"ref"`
	Project     string `json:"project"`
	Target      string `json:"target"`
	Inv         string `json:"inv,omitempty"`
	Failed      bool   `json:"failed"`
	Error       string `json:"error,omitempty"`
	TimestampMs int64  `json:"timestamp_ms"`
	DurationMs  int64  `json:"duration_ms"`
}

// OutputsHandler serves GET /api/v1/outputs: every stored run's descriptor as JSON
// ({"outputs":[...]}), newest first, so the console's log-viewer tree can browse recent runs grouped
// project -> target -> run. Read-only; a browser fetch reads it cross-origin under the same /api CORS
// + bearer guards as the rest of the bridge.
type OutputsHandler struct {
	handler.Base
	src OutputSource
}

// NewOutputsHandler returns the GET /api/v1/outputs handler reading from src.
func NewOutputsHandler(src OutputSource, log *slog.Logger) *OutputsHandler {
	h := &OutputsHandler{src: src}
	h.Base = handler.New(h.serve, log)
	return h
}

func (h *OutputsHandler) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	descs := h.src.ListDescriptors()
	runs := make([]runSummary, 0, len(descs))
	for _, d := range descs {
		runs = append(runs, runSummary{
			Ref: d.Ref, Project: d.Project, Target: d.Target, Inv: d.Inv,
			Failed: d.Failed, Error: d.ErrMsg, TimestampMs: d.TimestampMs, DurationMs: d.DurationMs,
		})
	}
	body, err := json.Marshal(map[string]any{"outputs": runs})
	if err != nil {
		http.Error(w, "marshal error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}

// OutputHandler serves GET /api/v1/output?ref=<ref>: one stored run's VERBATIM captured output as
// text/plain (via ByRef - the exact bytes the process wrote, never a reconstruction). The console's
// run browser fetches this on selection and renders it in the viewer. ref accepts a git-style unique
// prefix, matching the CLI. An absent/aged-out ref is a 404 and an ambiguous prefix a 400, so a stale
// tree selection fails honestly rather than silently.
type OutputHandler struct {
	handler.Base
	src OutputSource
}

// NewOutputHandler returns the GET /api/v1/output handler reading from src.
func NewOutputHandler(src OutputSource, log *slog.Logger) *OutputHandler {
	h := &OutputHandler{src: src}
	h.Base = handler.New(h.serve, log)
	return h
}

func (h *OutputHandler) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ref := r.URL.Query().Get("ref")
	if ref == "" {
		http.Error(w, "missing ref", http.StatusBadRequest)
		return
	}
	raw, _, err := h.src.ByRef(ref)
	if err != nil {
		var amb *cache.AmbiguousRefError
		switch {
		case errors.Is(err, fs.ErrNotExist):
			http.Error(w, "no such run", http.StatusNotFound)
		case errors.As(err, &amb):
			http.Error(w, "ambiguous ref", http.StatusBadRequest)
		default:
			http.Error(w, "read error", http.StatusInternalServerError)
		}
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(raw) //nolint:gosec // G705: served as text/plain (not html), so the captured build output cannot execute as script
}
