package viewer

import (
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"

	"google.golang.org/protobuf/proto"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/handler"
	"github.com/egladman/magus/internal/journal"
	json "github.com/egladman/magus/internal/json"
)

// runSource is the narrow repository contract the invocation routes need: list the retained run
// journals, read one back as events, and resolve an output ref to the run that produced it.
// Satisfied by *cache.OutputStore, like [outputSource] beside it.
type runSource interface {
	ListRunLogs(limit int) []cache.RunLog
	InvocationEventsByID(inv string) (journal.Invocation, []journal.Event, error)
	DescriptorByRef(ref string) (cache.OutputDescriptor, error)
}

// defaultRunLimit bounds an unparameterized /api/v1/runs read. The store retains
// cache.DefaultMaxRuns journals, which is more history than a browsable tree wants to paint at
// once; a caller after the rest asks for it.
const defaultRunLimit = 200

// runLog is the JSON shape of one retained invocation in the browser feed. An explicit wire DTO,
// not cache.RunLog, for the reason runSummary gives: the store's layout evolves without moving
// what the console reads. Times are unix milliseconds.
type runLog struct {
	Inv          string   `json:"inv"`
	Arguments    []string `json:"arguments,omitempty"`
	Trigger      string   `json:"trigger,omitempty"`
	StartedMs    int64    `json:"started_ms"`
	FinishedMs   int64    `json:"finished_ms,omitempty"`
	Status       string   `json:"status,omitempty"`
	MagusVersion string   `json:"magus_version,omitempty"`
}

// RunsHandler serves GET /api/v1/runs[?limit=N]: the retained invocation journals as JSON
// ({"runs":[...]}), newest first, so the console's run browser can list past runs by the COMMAND
// that produced them. It is the invocation-addressed half of the browser feed; /api/v1/outputs is
// the target-addressed half, and the console joins the two on each row's inv id.
type RunsHandler struct {
	handler.Base
	src runSource
}

// NewRunsHandler returns the GET /api/v1/runs handler reading from src.
func NewRunsHandler(src runSource, log *slog.Logger) *RunsHandler {
	h := &RunsHandler{src: src}
	h.Base = handler.New(h.serve, log)
	return h
}

func (h *RunsHandler) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := defaultRunLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			http.Error(w, "bad limit", http.StatusBadRequest)
			return
		}
		limit = min(n, cache.DefaultMaxRuns)
	}
	logs := h.src.ListRunLogs(limit)
	runs := make([]runLog, 0, len(logs))
	for _, l := range logs {
		runs = append(runs, runLog{
			Inv: l.Inv, Arguments: l.Arguments, Trigger: l.Trigger,
			StartedMs: l.StartedMs, FinishedMs: l.FinishedMs, Status: l.Status,
			MagusVersion: l.MagusVersion,
		})
	}
	body, err := json.Marshal(map[string]any{"runs": runs})
	if err != nil {
		http.Error(w, "marshal error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}

// RunHandler serves GET /api/v1/run?inv=<id> (or ?ref=<output-ref>): one past invocation's whole
// journal as a magus.viewer.v1alpha1 Journal in binary protobuf - the SAME message the `#data=`
// fragment carries, so a browsed run renders through the viewer's structural path (per-target
// sections, exact statuses, the waterfall) instead of the heuristic parse a verbatim blob gets.
//
// ?ref= resolves the output's descriptor to the invocation that produced it and serves that run,
// which is why a ref opens its whole run in context rather than one blob. Journals rotate on a
// coarser cap than outputs, so a ref whose journal has aged out is a 404 - the caller's cue to
// fall back to /api/v1/output, which keeps the verbatim bytes for longer.
type RunHandler struct {
	handler.Base
	src runSource
}

// NewRunHandler returns the GET /api/v1/run handler reading from src.
func NewRunHandler(src runSource, log *slog.Logger) *RunHandler {
	h := &RunHandler{src: src}
	h.Base = handler.New(h.serve, log)
	return h
}

func (h *RunHandler) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	inv := r.URL.Query().Get("inv")
	if ref := r.URL.Query().Get("ref"); ref != "" {
		if inv != "" {
			http.Error(w, "pass inv or ref, not both", http.StatusBadRequest)
			return
		}
		desc, err := h.src.DescriptorByRef(ref)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			http.Error(w, "no such run", http.StatusNotFound)
			return
		case err != nil:
			var amb *cache.AmbiguousRefError
			if errors.As(err, &amb) {
				http.Error(w, "ambiguous ref", http.StatusBadRequest)
				return
			}
			http.Error(w, "read error", http.StatusInternalServerError)
			return
		}
		if desc.Inv == "" {
			http.Error(w, "no such run", http.StatusNotFound)
			return
		}
		inv = desc.Inv
	}
	if inv == "" {
		http.Error(w, "missing inv", http.StatusBadRequest)
		return
	}
	header, events, err := h.src.InvocationEventsByID(inv)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			http.Error(w, "no such run", http.StatusNotFound)
			return
		}
		http.Error(w, "read error", http.StatusInternalServerError)
		return
	}
	raw, err := proto.Marshal(journalToProto(header, events))
	if err != nil {
		http.Error(w, "marshal error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(raw) //nolint:gosec // G705: a protobuf message served as application/octet-stream, which no browser parses as markup
}
