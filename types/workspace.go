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

// Workspace is the discovered set of projects under a root directory.
type Workspace struct {
	// Root is the workspace root, always absolute and symlink-free.
	Root string

	// Projects maps project path to *Project.
	Projects map[string]*Project

	// VCSOptions holds explicit VCS configuration injected at construction time.
	VCSOptions VCSOptions

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
// The workspace rides on the context rather than a parameter because it must
// cross the spell-invocation boundary (spells.Driver.Invoke takes only ctx +
// InvokeRequest), reaching host bindings the run engine cannot thread it to
// directly. WorkspaceFromContext callers must handle a nil result.
func WithWorkspace(ctx context.Context, ws WorkspaceRepository) context.Context {
	return context.WithValue(ctx, workspaceKey{}, ws)
}

// WorkspaceFromContext returns the WorkspaceRepository from ctx, or nil when no
// workspace was installed (e.g. a direct SpellDriver call outside a run).
func WorkspaceFromContext(ctx context.Context) WorkspaceRepository {
	w, _ := ctx.Value(workspaceKey{}).(WorkspaceRepository)
	return w
}

type activeDispatchKey struct{}

// ActiveDispatch records which projects are running a target of their OWN during this
// run, so the descendant-write audit can tell a child's writes from a parent reaching
// across a boundary.
//
// It is filled as dispatch happens rather than derived from declarations, because the
// dependency that reaches a descendant is often not on the target being audited: the
// root's build needs go-build, and go-build is what needs the nested project. Only the
// live dispatch sees the whole chain. Entries are project dirs, which is what the
// dispatcher knows and what the audit already carries per descendant.
type ActiveDispatch struct {
	mu sync.Mutex
	m  map[string]struct{}
}

// Mark records dir as running its own target. Safe for concurrent use: cross-project
// dependencies dispatch in parallel.
func (a *ActiveDispatch) Mark(dir string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.m == nil {
		a.m = map[string]struct{}{}
	}
	a.m[dir] = struct{}{}
}

// Has reports whether dir was marked.
func (a *ActiveDispatch) Has(dir string) bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.m[dir]
	return ok
}

// WithActiveDispatch installs a shared ActiveDispatch for one run.
func WithActiveDispatch(ctx context.Context, a *ActiveDispatch) context.Context {
	return context.WithValue(ctx, activeDispatchKey{}, a)
}

// ActiveDispatchFromContext returns the run's ActiveDispatch, or nil.
func ActiveDispatchFromContext(ctx context.Context) *ActiveDispatch {
	a, _ := ctx.Value(activeDispatchKey{}).(*ActiveDispatch)
	return a
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
