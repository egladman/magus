package diff

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/handler"
	"github.com/egladman/magus/internal/jobs"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/trail"
)

// runSource is the workspace half a run needs: which targets the magusfile declares for a
// project. Narrow on purpose, so a test can answer it without a workspace.
type runSource interface {
	ProjectTargets(ctx context.Context, project string) []string
}

// RunHandler serves /api/v1/diff/run: the reader asks a question about the change in front
// of them - does this still pass? - and gets the answer from the machine the code is on.
//
// This is the one review capability that cannot have a provider gap. It asks the local workspace
// rather than a forge, so it works identically on GitHub, GitLab, a fork, or no forge at all, and
// on git or hg alike. Nothing below touches either boundary.
//
// A run is named by a TARGET and a PROJECT, never by an argv. The daemon's job dispatch admits
// the resulting `run <target> <project>` only when the magusfile declares that target for that
// project, so the console can ask for work the workspace already defines and cannot ask for
// anything else. That is strictly less than a terminal's `magus run`, which is the bar a browser-
// reachable surface has to clear.
type RunHandler struct {
	handler.Base
	workspace runSource
	// cacheDir is where the activity trail lives: the durable record of what a finished run
	// decided, read back after the live registry has pruned it.
	cacheDir string
	version  string
	// socket returns the daemon's proc socket address to submit to, and submitFn/statusFn are
	// the proc entry points - injectable so the submit and poll paths are testable without a
	// live daemon.
	socket   func() string
	submitFn func(ctx context.Context, addr string, argv []string, version string) (string, error)
	statusFn func(ctx context.Context, addr string) (*proc.StatusReply, error)
}

// NewRunHandler returns the inline-run handler. A nil workspace declares no targets, so every
// request is refused as undeclared - which is what a daemon with no workspace can honestly say.
func NewRunHandler(workspace runSource, cacheDir, version string, log *slog.Logger) *RunHandler {
	h := &RunHandler{
		workspace: workspace,
		cacheDir:  cacheDir,
		version:   version,
		socket:    func() string { return os.Getenv("MAGUS_DAEMON_SOCKET") },
		submitFn:  proc.SubmitJob,
		statusFn:  proc.QueryStatus,
	}
	h.Base = handler.New(h.serve, log)
	return h
}

// diffRunRequest names the work: a declared target and the project to run it for.
//
// The patch digest the reader is looking at is NOT here. Staleness is a client-side comparison -
// the surface knows which digest it rendered against when the verdict arrived, and greys the
// verdict out when its own digest moves - so sending it to the daemon would only give it a second
// place to be wrong.
type diffRunRequest struct {
	Target  string `json:"target"`
	Project string `json:"project"`
}

// diffRunResponse is the wire shape for both submitting and polling.
type diffRunResponse struct {
	Target  string `json:"target"`
	Project string `json:"project"`
	// State is one of: running, passed, failed, unknown. "unknown" means no run of this target
	// has finished on this machine yet - distinct from a run that finished and failed.
	State string `json:"state"`
	// Started reports that THIS request started the run, as opposed to finding one already in
	// flight. The surface says "already running" rather than appearing to start a second one.
	Started bool `json:"started,omitempty"`
	// FinishedMs, DurationMs and Error describe the last completed run, read from the activity
	// trail. Zero while a run is in flight and no earlier one exists.
	FinishedMs int64  `json:"finished_ms,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	Error      string `json:"error,omitempty"`
	// Undeclared names a target the magusfile does not declare for this project, and lists what
	// it does declare. It is a 200 rather than an error status for the reason the branch lookup
	// names its gaps: the reader did nothing wrong, and what they are owed is the difference
	// between "this failed" and "magus does not know that target here".
	Undeclared string   `json:"undeclared,omitempty"`
	Available  []string `json:"available,omitempty"`
}

func (h *RunHandler) serve(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodOptions:
		w.WriteHeader(http.StatusNoContent)
	case http.MethodGet:
		req := diffRunRequest{
			Target:  strings.TrimSpace(r.URL.Query().Get("target")),
			Project: strings.TrimSpace(r.URL.Query().Get("project")),
		}
		h.answer(r.Context(), w, req, false)
	case http.MethodPost:
		var req diffRunRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "malformed request", http.StatusBadRequest)
			return
		}
		req.Target, req.Project = strings.TrimSpace(req.Target), strings.TrimSpace(req.Project)
		h.answer(r.Context(), w, req, true)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// answer reports the state of req's target, submitting it first when start is set. Both verbs
// share it because a submit's useful reply IS the poll's reply: the surface renders one shape
// whether it just started the run or is watching one somebody else did.
func (h *RunHandler) answer(ctx context.Context, w http.ResponseWriter, req diffRunRequest, start bool) {
	out := diffRunResponse{Target: req.Target, Project: req.Project, State: "unknown"}
	if req.Target == "" || req.Project == "" {
		http.Error(w, "target and project are required", http.StatusBadRequest)
		return
	}
	var declared []string
	if h.workspace != nil {
		declared = h.workspace.ProjectTargets(ctx, req.Project)
	}
	if !slices.Contains(declared, req.Target) {
		out.Undeclared = req.Project + " declares no target named " + req.Target
		out.Available = declared
		handler.WriteJSON(w, out)
		return
	}

	argv := []string{"run", req.Target, req.Project}
	running := h.isRunning(ctx, argv)
	if start && !running {
		if err := h.submit(ctx, argv); err != nil {
			out.State = "failed"
			out.Error = err.Error()
			handler.WriteJSON(w, out)
			return
		}
		out.Started, running = true, true
	}
	if running {
		out.State = "running"
		handler.WriteJSON(w, out)
		return
	}
	// Not in flight, so the trail holds whatever the last run of this exact target decided.
	//
	// The trail does not record whether the run executed or replayed from cache, and this
	// deliberately does not guess. A replay is not a stale answer: the cache key covers the
	// target's sources, so a hit means THIS tree state passed. What can go stale is the reader's
	// view moving on after the verdict, which as_of covers.
	if ev, ok := trail.LastRun(h.cacheDir, jobs.ActionString(argv)); ok {
		out.State = "passed"
		if ev.Outcome != trail.OutcomeOK {
			out.State = "failed"
			out.Error = ev.Error
		}
		out.FinishedMs = ev.Ts + ev.DurMs
		out.DurationMs = ev.DurMs
	}
	handler.WriteJSON(w, out)
}

// submit hands argv to the daemon's own proc socket - a self-dial - so an inline run rides the
// identical coalescing and journal path a terminal's run does.
func (h *RunHandler) submit(ctx context.Context, argv []string) error {
	addr := h.socket()
	if addr == "" {
		return errors.New("no daemon socket to submit to; run `magus server start`")
	}
	_, err := h.submitFn(ctx, addr, argv, h.version)
	return err
}

// isRunning reports whether argv is already in flight in the daemon. A failed status query
// reports not-running: the submit that follows coalesces anyway, so the worst case is an
// accurate reply built from one extra round trip, never a duplicate run.
func (h *RunHandler) isRunning(ctx context.Context, argv []string) bool {
	st, err := h.statusFn(ctx, h.socket())
	if err != nil || st == nil {
		return false
	}
	for _, c := range st.Calls {
		if slices.Equal(c.Args, argv) {
			return true
		}
	}
	return false
}
