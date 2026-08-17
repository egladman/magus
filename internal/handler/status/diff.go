package status

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/diff"
	"github.com/egladman/magus/internal/handler"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/types"
)

// patchSource is the narrow consumer contract the diff handler needs from the console
// service: the working tree's uncommitted unified diff. It is satisfied by *console.Service;
// the handler package holds no concrete service, matching insightSource above.
type patchSource interface {
	WorkingDiff(ctx context.Context, paths []string) (string, error)
}

// diffSource is the annotation half, kept a SEPARATE interface from patchSource because the
// two routes have genuinely different costs and a caller should be able to mount the cheap
// one without the expensive one.
type diffSource interface {
	Diff(ctx context.Context, paths []string) (types.Diff, error)
	// WorkingDiff is read here only to stamp the session's snapshot identity. It is the same
	// call the patch route makes, and it is cheap next to the symbol and impact work above it -
	// whereas a client-supplied digest would describe whatever that client last fetched, which
	// is not necessarily the tree this changeset was computed from.
	WorkingDiff(ctx context.Context, paths []string) (string, error)
}

// DiffHandler serves GET /api/v1/diff: the changed files annotated with what the
// workspace knows - role (generated or not), owning project, changed-symbol reach, observed
// coverage - in the order magus recommends reading them.
//
// It is the differentiated half of the review surface and a SECOND round trip on purpose.
// /api/v1/diff/patch returns the patch in milliseconds; this one loads the symbol shards and walks
// a reverse closure. Folding them together would hold the whole diff behind the slowest
// overlay for no reading benefit.
//
// The caller passes the paths it is actually reviewing, repeated as `path`. That is not an
// optimization: re-deriving the changed set here would race an edit made since the patch was
// read and annotate a file the reader cannot see.
type DiffHandler struct {
	handler.Base
	src      diffSource
	sessions *diff.Store
	root     string
}

// NewDiffHandler returns the GET /api/v1/diff handler reading from src. sessions and root
// may be nil/empty, which serves a session-less review - the shape is identical, so a client
// needs no branch for a daemon that is not pairing.
func NewDiffHandler(src diffSource, sessions *diff.Store, root string, log *slog.Logger) *DiffHandler {
	h := &DiffHandler{src: src, sessions: sessions, root: root}
	h.Base = handler.New(h.serve, log)
	return h
}

func (h *DiffHandler) serve(w http.ResponseWriter, r *http.Request) {
	if !allowGet(w, r) {
		return
	}
	paths := scopePaths(r)
	if len(paths) == 0 {
		// No paths is a caller BUG, not an empty review: annotating nothing would render as
		// "this change touches nothing", which is the most misleading answer available.
		http.Error(w, "review requires at least one path parameter", http.StatusBadRequest)
		return
	}
	out, err := h.src.Diff(r.Context(), paths)
	if err != nil {
		if errors.Is(err, console.ErrNoWorkspace) {
			http.Error(w, "workspace unavailable", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "review error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// Attaching here rather than on a separate route means a client that can read a review is
	// already paired: the console's first fetch is what makes the session an agent can find,
	// so pairing needs no setup step anyone has to remember.
	if h.sessions != nil && h.root != "" {
		// Best-effort: a session with an unknown snapshot id is worse than one with a correct
		// one, but far better than refusing to pair because a second git call failed.
		asOf := ""
		if patch, perr := h.src.WorkingDiff(r.Context(), nil); perr == nil {
			asOf = diff.PatchDigest(patch)
		}
		writeJSON(w, h.sessions.Attach(h.root, out.Base, out, asOf))
		return
	}
	writeJSON(w, types.DiffSession{Base: out.Base, Diff: out, Cursor: types.DiffCursor{Hunk: -1}})
}

// PatchHandler serves GET /api/v1/diff/patch: the working tree's uncommitted changes as one unified
// patch, for the console's review surface.
//
// The patch ships as TEXT rather than as a parsed file/hunk tree, and that split is the
// design rather than a shortcut. A unified patch is the one interchange format every backend
// and every forge already emits, so a reader written against it works unchanged on a patch
// this route produced and on one fetched from a provider later. Parsing it here would fork
// that reader in two and make the provider case the odd one out.
//
// Optional `path` query parameters scope the diff, repeated once per path. Absent, the whole
// repository is diffed. A service with no workspace yields 503, not 500 - the same posture
// the insight route takes, because "no workspace yet" is a state the console renders rather
// than an error it reports.
type PatchHandler struct {
	handler.Base
	src patchSource
}

// ContextHandler serves bounded, snapshot-paired file context.
type ContextHandler struct {
	handler.Base
	root string
	src  patchSource
}

const maxContextFileBytes = 1 << 20

// NewContextHandler returns the bounded loopback context handler.
func NewContextHandler(root string, src patchSource, log *slog.Logger) *ContextHandler {
	h := &ContextHandler{root: root, src: src}
	h.Base = handler.New(h.serve, log)
	return h
}

type contextResponse struct {
	Path  string   `json:"path"`
	AsOf  string   `json:"as_of"`
	Start int      `json:"start"`
	Lines []string `json:"lines"`
}

func (h *ContextHandler) serve(w http.ResponseWriter, r *http.Request) {
	if !allowGet(w, r) {
		return
	}
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" || filepath.IsAbs(path) || path != filepath.Clean(path) || path == "." || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
		http.Error(w, "context requires a workspace-relative path", http.StatusBadRequest)
		return
	}
	start, _ := strconv.Atoi(r.URL.Query().Get("start"))
	end, _ := strconv.Atoi(r.URL.Query().Get("end"))
	if start < 1 || end < start {
		http.Error(w, "context requires a valid line range", http.StatusBadRequest)
		return
	}
	radius, _ := strconv.Atoi(r.URL.Query().Get("radius"))
	if radius < 0 {
		radius = 0
	}
	if radius > 32 {
		radius = 32
	}
	asOf := strings.TrimSpace(r.URL.Query().Get("as_of"))
	if asOf == "" {
		http.Error(w, "context requires the review snapshot identity", http.StatusBadRequest)
		return
	}
	patch, err := h.src.WorkingDiff(r.Context(), nil)
	if err != nil {
		if errors.Is(err, console.ErrNoWorkspace) {
			http.Error(w, "workspace unavailable", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "context snapshot error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if diff.PatchDigest(patch) != asOf {
		http.Error(w, "review snapshot is stale; refresh the diff", http.StatusConflict)
		return
	}
	changed := false
	for _, file := range diff.ParseHunks(patch) {
		if file.Path == path {
			changed = true
			break
		}
	}
	if !changed {
		http.Error(w, "context path is not in the reviewed patch", http.StatusNotFound)
		return
	}
	root, err := filepath.EvalSymlinks(h.root)
	if err != nil {
		http.Error(w, "workspace unavailable", http.StatusServiceUnavailable)
		return
	}
	target, err := filepath.EvalSymlinks(filepath.Join(root, path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.Error(w, "file is not present in the working tree", http.StatusNotFound)
			return
		}
		http.Error(w, "context error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		http.Error(w, "context path escapes workspace", http.StatusBadRequest)
		return
	}
	info, err := os.Stat(target)
	if err != nil {
		http.Error(w, "context error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !info.Mode().IsRegular() {
		http.Error(w, "context requires a regular file", http.StatusBadRequest)
		return
	}
	if info.Size() > maxContextFileBytes {
		http.Error(w, "context is unavailable for files larger than 1 MiB", http.StatusRequestEntityTooLarge)
		return
	}
	body, err := os.ReadFile(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.Error(w, "file is not present in the working tree", http.StatusNotFound)
			return
		}
		http.Error(w, "context error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	lines := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
	from := max(1, start-radius)
	to := min(len(lines), end+radius)
	if from > len(lines) {
		writeJSON(w, contextResponse{Path: path, AsOf: asOf, Start: from, Lines: []string{}})
		return
	}
	writeJSON(w, contextResponse{Path: path, AsOf: asOf, Start: from, Lines: lines[from-1 : to]})
}

// NewPatchHandler returns the GET /api/v1/diff/patch handler reading from src.
func NewPatchHandler(src patchSource, log *slog.Logger) *PatchHandler {
	h := &PatchHandler{src: src}
	h.Base = handler.New(h.serve, log)
	return h
}

// diffResponse is the wire shape. Patch carries the whole unified body; Clean says the tree
// had nothing to review, which is DISTINCT from an empty patch the reader failed to parse -
// a console that cannot tell those apart renders "no changes" over a bug.
type diffResponse struct {
	Patch string `json:"patch"`
	Clean bool   `json:"clean"`
}

func (h *PatchHandler) serve(w http.ResponseWriter, r *http.Request) {
	if !allowGet(w, r) {
		return
	}
	patch, err := h.src.WorkingDiff(r.Context(), scopePaths(r))
	if err != nil {
		if errors.Is(err, console.ErrNoWorkspace) {
			http.Error(w, "workspace unavailable", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "diff error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, diffResponse{Patch: patch, Clean: strings.TrimSpace(patch) == ""})
}

// scopePaths reads the repeated `path` query parameter, dropping empties so a stray `?path=`
// scopes to nothing rather than to the empty pathspec - which every backend reads as "the
// whole repository", the opposite of what the caller asked for.
func scopePaths(r *http.Request) []string {
	raw := r.URL.Query()["path"]
	paths := make([]string, 0, len(raw))
	for _, p := range raw {
		if p = strings.TrimSpace(p); p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}
