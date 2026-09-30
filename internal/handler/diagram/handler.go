package diagram

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/handler"
	"github.com/egladman/magus/types"
)

// Path is the listing route; a figure is served at Path + "/" + its id.
const Path = "/api/v1/diagrams"

// workspace is what the figures read. *magus.Magus supplies all but ImportGraph and
// SymbolIndex.
type workspace interface {
	TargetGraph(ctx context.Context) (types.TargetGraphOutput, error)
	ImportGraph(ctx context.Context) (types.ImportGraph, error)
	// SymbolIndex reports whether a symbol index exists without loading its shards.
	SymbolIndex(ctx context.Context) (types.SymbolIndexDigest, error)
	ReviewOrigin(ctx context.Context) types.ReviewOrigin
	RevisionCheckpoint(ctx context.Context, rev string) (types.VCSCheckpoint, error)
}

// Handler serves the figures the workspace can draw, rendered server-side by the embedded
// magus/figure module. GET /api/v1/diagrams lists them: projects, targets:<project> per
// project, and imports. GET /api/v1/diagrams/{id}?scope=&focus=&depth= renders one through
// its lens; scope repeats, and depth defaults to 1 when focus is set. A rendered figure is
// cached by the graph's content and the lens, so an unchanged graph is drawn once.
//
// A figure the module refuses to draw, most often one over its node or edge budget, is a
// 422 whose body is its finding. An import figure without a symbol index is a 409.
type Handler struct {
	handler.Base
	ws workspace
	// cache holds rendered figures across requests; compile is what fills it.
	cache   *renderCache
	compile func(ctx context.Context, g graph, desc, anchorHref string) (Figure, error)
}

// NewHandler returns the diagrams handler reading from ws.
func NewHandler(ws workspace, log *slog.Logger) *Handler {
	h := &Handler{ws: ws, cache: newRenderCache(), compile: render}
	h.Base = handler.New(h.serve, log)
	return h
}

// Entry is one figure in the listing. Indexed is set only on the import figure.
type Entry struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Project string `json:"project,omitempty"`
	Indexed *bool  `json:"indexed,omitempty"`
}

// Rendered is one figure as GET /api/v1/diagrams/{id} answers it. SourceURL is a template
// with {path} and {line} to fill from a node's anchor, or "" when the remote is not one
// magus can link.
type Rendered struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	SVG       string `json:"svg"`
	Nodes     []Node `json:"nodes"`
	SourceURL string `json:"source_url"`
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request) {
	if !handler.AllowGet(w, r) {
		return
	}
	id, ok := strings.CutPrefix(r.URL.Path, Path+"/")
	if !ok || id == "" {
		h.list(w, r)
		return
	}
	h.render(w, r, id)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	tg, err := h.ws.TargetGraph(r.Context())
	if err != nil {
		h.Fail(w, r, "target graph", err)
		return
	}
	idx, err := h.ws.SymbolIndex(r.Context())
	if err != nil {
		h.Fail(w, r, "symbol index", err)
		return
	}
	out := []Entry{{ID: KindProjects, Kind: KindProjects, Title: "Workspace projects"}}
	for _, p := range tg.Projects {
		out = append(out, Entry{
			ID:      KindTargets + ":" + p.Path,
			Kind:    KindTargets,
			Title:   "Targets in " + p.Label(),
			Project: p.Path,
		})
	}
	out = append(out, Entry{ID: KindImports, Kind: KindImports, Title: "Package imports", Indexed: &idx.Indexed})
	handler.WriteJSON(w, map[string][]Entry{"diagrams": out})
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, id string) {
	lens, err := parseLens(id, r.URL.Query())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	g, err := h.graph(r.Context(), lens)
	switch {
	case errors.Is(err, ErrNotIndexed):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case errors.Is(err, errUnknownFigure):
		http.NotFound(w, r)
		return
	case err != nil:
		h.Fail(w, r, "figure source", err)
		return
	}
	g, err = g.cut(lens)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sourceURL := h.sourceURL(r.Context())
	desc, href := lens.describe(), anchorTemplate(sourceURL)
	fig, err := h.cache.get(r.Context(), renderKey(g, desc, href), func(ctx context.Context) (Figure, error) {
		return h.compile(ctx, g, desc, href)
	})
	var findings *FindingsError
	if errors.As(err, &findings) {
		http.Error(w, findings.Findings, http.StatusUnprocessableEntity)
		return
	}
	if err != nil {
		h.Fail(w, r, "render", err)
		return
	}
	nodes := fig.Nodes
	if nodes == nil {
		nodes = []Node{}
	}
	handler.WriteJSON(w, Rendered{
		ID:        id,
		Title:     fig.Title,
		SVG:       fig.SVG,
		Nodes:     nodes,
		SourceURL: sourceURL,
	})
}

// anchorTemplate is the source URL template without its line fragment: a node anchors a
// directory or a file, never a line, so the link lands on the path itself.
func anchorTemplate(sourceURL string) string {
	return strings.TrimSuffix(sourceURL, "#L{line}")
}

var errUnknownFigure = errors.New("diagram: unknown figure")

func (h *Handler) graph(ctx context.Context, l Lens) (graph, error) {
	switch l.Kind {
	case KindProjects, KindTargets:
		tg, err := h.ws.TargetGraph(ctx)
		if err != nil {
			return graph{}, err
		}
		if l.Kind == KindProjects {
			return projectGraph(tg), nil
		}
		g, ok := targetGraph(tg, l.Project)
		if !ok {
			return graph{}, errUnknownFigure
		}
		return g, nil
	case KindImports:
		ig, err := h.ws.ImportGraph(ctx)
		if err != nil {
			return graph{}, err
		}
		if !ig.Indexed {
			return graph{}, ErrNotIndexed
		}
		return importGraph(ig), nil
	}
	return graph{}, errUnknownFigure
}

// parseLens reads a figure id and its query into a Lens. An id it does not recognize keeps
// its kind so graph answers it not found.
func parseLens(id string, q url.Values) (Lens, error) {
	l := Lens{Kind: id, Scope: q["scope"], Focus: q.Get("focus")}
	if kind, project, ok := strings.Cut(id, ":"); ok && kind == KindTargets {
		l.Kind, l.Project = kind, project
	}
	switch d := q.Get("depth"); {
	case d != "":
		n, err := strconv.Atoi(d)
		if err != nil || n < 0 {
			return Lens{}, &LensError{msg: "depth must be a whole number, 0 or more"}
		}
		l.Depth = n
	case l.Focus != "":
		l.Depth = 1
	}
	return l, nil
}

// sourceURL is the blob template for this checkout's HEAD, or "" when the workspace has no
// GitHub remote or no revision to pin.
func (h *Handler) sourceURL(ctx context.Context) string {
	remote := h.ws.ReviewOrigin(ctx).Remote
	if remote == "" {
		return ""
	}
	cp, err := h.ws.RevisionCheckpoint(ctx, "HEAD")
	if err != nil {
		return ""
	}
	return githubBlobTemplate(remote, cp.Revision)
}

// githubBlobTemplate turns a github.com remote, https or ssh, into a blob URL template
// pinned to rev. Any other host yields "": a guessed link is worse than none.
func githubBlobTemplate(remote, rev string) string {
	if rev == "" {
		return ""
	}
	remote = strings.TrimSuffix(strings.TrimSpace(remote), ".git")
	var host, repo string
	if u, err := url.Parse(remote); err == nil && u.Scheme != "" && u.Host != "" {
		host, repo = u.Hostname(), strings.Trim(u.Path, "/")
	} else if at, rest, ok := strings.Cut(remote, "@"); ok && at != "" {
		host, repo, _ = strings.Cut(rest, ":")
	}
	owner, name, ok := strings.Cut(repo, "/")
	if !strings.EqualFold(host, "github.com") || !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return ""
	}
	return "https://github.com/" + owner + "/" + name + "/blob/" + rev + "/{path}#L{line}"
}
