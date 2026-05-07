package types

import (
	"cmp"
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// ErrUnknownProject is returned (wrapped) by WorkspaceRepository.ExpandPath
// when a caller refers to a project path that does not exist.
var ErrUnknownProject = errors.New("magus: unknown project")

// ErrNoCache is returned by cache operations on a cache-free (Inspect) workspace.
var ErrNoCache = errors.New("magus: no cache available")

// WorkspaceReader is the read-only in-memory view of a discovered workspace.
type WorkspaceReader interface {
	Root() string
	All() []*Project
	Get(path string) *Project
	Graph() (*Graph, error)
	VCSOptions() VCSOptions
	Where(dir string) (*Project, bool)
}

// TargetExpander resolves a Target into concrete per-project targets.
type TargetExpander interface {
	ExpandPath(t Target) ([]Target, error)
	// ExpandCwd resolves t against the project containing the current working
	// directory. found reports whether cwd is inside any project; when false,
	// targets is empty and the caller typically falls back to ExpandPath or
	// reports "not inside a project". found is a deliberate signal distinct from
	// len(targets) — callers (e.g. magus tail) key their error message on it.
	ExpandCwd(t Target) (targets []Target, found bool, err error)
	ExpandAffected(ctx context.Context, target, baseRef string) ([]Target, string, error)
}

// AffectedComputer computes the VCS-impacted project set.
type AffectedComputer interface {
	Affected(ctx context.Context, base string) (*AffectedResult, error)
	AffectedFromPaths(ctx context.Context, paths []string) (*AffectedResult, error)
}

// Describer returns the structured inventory behind `magus describe`.
type Describer interface {
	DescribeSpells() SpellsOutput
	DescribeTargets() TargetsOutput
	DescribeGraph() TargetGraphOutput
	DescribeProjects() ProjectsOutput
	DescribeWorkspaces(cfg WorkspaceConfig) WorkspacesOutput
	DescribeTarget(t Target) (EvaluatedTargetsOutput, error)
	DescribeEvaluatedProjects() EvaluatedProjectsOutput
}

// WorkspaceRepository is the full domain interface for a discovered workspace.
// Prefer the narrowest embedded role a consumer actually uses.
type WorkspaceRepository interface {
	WorkspaceReader
	TargetExpander
	AffectedComputer
	Describer
}

// Workspace is the discovered set of projects under a root directory.
type Workspace struct {
	// Root is the workspace root, always absolute and symlink-free.
	Root string

	// Projects maps project path to *Project.
	Projects map[string]*Project

	// VCSOptions holds explicit VCS configuration injected at construction time.
	VCSOptions VCSOptions

	// Strict gates (*Workspace).Graph: unregistered deps become ErrUnregisteredDep
	// instead of silently dropped edges.
	Strict bool

	// graphObs is the default observer for Graph calls. Use ContextWithGraphObserver
	// for concurrent callers (daemon) — SetGraphObserver is only safe for a sole owner.
	graphObsMu sync.RWMutex
	graphObs   Observer
}

// SetGraphObserver installs a default graph observer. Pass nil to clear.
// For concurrent callers (daemon) use ContextWithGraphObserver instead.
func (w *Workspace) SetGraphObserver(o Observer) {
	w.graphObsMu.Lock()
	w.graphObs = o
	w.graphObsMu.Unlock()
}

// GraphObserver returns the default graph observer, or nil.
func (w *Workspace) GraphObserver() Observer {
	w.graphObsMu.RLock()
	defer w.graphObsMu.RUnlock()
	return w.graphObs
}

func (w *Workspace) All() []*Project {
	out := make([]*Project, 0, len(w.Projects))
	for _, p := range w.Projects {
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b *Project) int { return cmp.Compare(a.Path, b.Path) })
	return out
}

// UnderPath returns every project whose Path has prefix as a path prefix.
func (w *Workspace) UnderPath(prefix string) []*Project {
	prefix = strings.TrimSuffix(filepath.ToSlash(prefix), "/") + "/"
	var out []*Project
	for _, p := range w.All() {
		if strings.HasPrefix(p.Path+"/", prefix) {
			out = append(out, p)
		}
	}
	return out
}

// Get returns the project with the given path, or nil.
func (w *Workspace) Get(path string) *Project {
	if w == nil {
		return nil
	}
	return w.Projects[path]
}

type workspaceKey struct{}

// WithWorkspace returns a context carrying ws for downstream code (e.g. audit).
func WithWorkspace(ctx context.Context, ws WorkspaceRepository) context.Context {
	return context.WithValue(ctx, workspaceKey{}, ws)
}

// WorkspaceFromContext returns the WorkspaceRepository from ctx, or nil.
func WorkspaceFromContext(ctx context.Context) WorkspaceRepository {
	w, _ := ctx.Value(workspaceKey{}).(WorkspaceRepository)
	return w
}

type activeDispatchKey struct{}

// WithActiveDispatch returns a context carrying the set of project paths in the current dispatch.
func WithActiveDispatch(ctx context.Context, projects map[string]struct{}) context.Context {
	return context.WithValue(ctx, activeDispatchKey{}, projects)
}

// ActiveDispatchFromContext returns the active-dispatch set, or nil.
func ActiveDispatchFromContext(ctx context.Context) map[string]struct{} {
	m, _ := ctx.Value(activeDispatchKey{}).(map[string]struct{})
	return m
}

type graphObserverKey struct{}

// ContextWithGraphObserver returns a context carrying a request-scoped graph observer.
// Use this instead of SetGraphObserver when sharing a workspace across goroutines.
func ContextWithGraphObserver(ctx context.Context, o Observer) context.Context {
	return context.WithValue(ctx, graphObserverKey{}, o)
}

// GraphObserverFromContext returns the request-scoped observer, or nil.
func GraphObserverFromContext(ctx context.Context) Observer {
	o, _ := ctx.Value(graphObserverKey{}).(Observer)
	return o
}
