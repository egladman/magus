package wire

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

type registryKey struct{}

// WorkspaceRegistry holds the per-Open project-option overrides for a single
// workspace open. Create one with NewWorkspaceRegistry, populate it via
// RegisterProject, then pass it to Inspect or Open via WithWorkspaceRegistry.
// A fresh WorkspaceRegistry per Open call means there is no shared mutable state
// between concurrent opens.
type WorkspaceRegistry struct {
	mu          sync.Mutex
	projectOpts map[string][]ProjectOption
	// remoteBackend is the spell name a magusfile chose as the remote cache
	// backend (via magus.cache.remote); empty when none was wired.
	remoteBackend string
}

// NewWorkspaceRegistry returns an empty WorkspaceRegistry.
func NewWorkspaceRegistry() *WorkspaceRegistry {
	return &WorkspaceRegistry{
		projectOpts: make(map[string][]ProjectOption),
	}
}

// ContextWithRegistry installs reg in ctx so that interpreters can retrieve it
// via WorkspaceRegistryFromContext.
func ContextWithRegistry(ctx context.Context, reg *WorkspaceRegistry) context.Context {
	return context.WithValue(ctx, registryKey{}, reg)
}

// WorkspaceRegistryFromContext returns the per-Open WorkspaceRegistry from ctx, or nil.
// Used by the Teal magus.project.register and magus.target bindings.
func WorkspaceRegistryFromContext(ctx context.Context) *WorkspaceRegistry {
	r, _ := ctx.Value(registryKey{}).(*WorkspaceRegistry)
	return r
}

// RegisterProject appends opts for the repo-relative project path. Safe to call
// concurrently.
func (r *WorkspaceRegistry) RegisterProject(path string, opts ...ProjectOption) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.projectOpts[path] = append(r.projectOpts[path], opts...)
}

// registerPathHint explains the explicit-path form of magus.project.register when
// the path didn't match a project — the classic footgun is passing the magusfile's
// own directory name (relative to the workspace root) instead of omitting the path
// to configure "this project". It lists the known projects so the caller can see
// the right spelling.
func registerPathHint(w types.WorkspaceRepository) string {
	var known []string
	for _, p := range w.All() {
		known = append(known, p.Path)
	}
	slices.Sort(known)
	return fmt.Sprintf("explicit register paths are relative to the workspace root (known projects: %s); "+
		"to configure the magusfile's own project, omit the path: register(fun(p, cb) { cb({...}); })",
		strings.Join(known, ", "))
}

// SetRemoteBackend records the spell name a magusfile chose as the remote cache
// backend. Last writer wins; safe to call concurrently.
func (r *WorkspaceRegistry) SetRemoteBackend(spellName string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.remoteBackend = spellName
}

// RemoteBackend returns the remote-cache-backend spell name a magusfile wired, or
// "" when none was.
func (r *WorkspaceRegistry) RemoteBackend() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.remoteBackend
}

// ProjectPaths returns the registered project paths in sorted order.
func (r *WorkspaceRegistry) ProjectPaths() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	paths := make([]string, 0, len(r.projectOpts))
	for p := range r.projectOpts {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	return paths
}

// Apply applies the registered project options to every project in w.
// Paths that do not match any discovered project are errors. Option errors
// are collected and joined. Spell names are resolved to *types.Spell values
// and their declared deps are unioned into each project's DependsOn.
func (r *WorkspaceRegistry) Apply(w types.WorkspaceRepository) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var errs []error
	for path, opts := range r.projectOpts {
		p := w.Get(path)
		if p == nil {
			errs = append(errs, fmt.Errorf("magus: register: %q in workspace %q: %w; %s",
				path, w.Root(), types.ErrUnknownProject, registerPathHint(w)))
			continue
		}
		for _, o := range opts {
			if err := o(p); err != nil {
				errs = append(errs, err)
			}
		}
	}
	// Resolve spell names to *types.Spell and accumulate spell-declared deps.
	// If any spell name is missing, the project's ResolvedSpells is left nil
	// so that Spells/Bindings/ResolvedSpells stay index-aligned: either all
	// three are in sync or the resolved view is absent, never shorter.
	for _, p := range w.All() {
		var resolved []*types.Spell
		projectOK := true
		for _, name := range p.Spells {
			l, ok := project.DefaultSpellRegistry().Lookup(name)
			if !ok {
				errs = append(errs, fmt.Errorf("magus: register: project %q: spell %q not registered",
					p.Path, name))
				projectOK = false
				continue
			}
			resolved = append(resolved, l)
		}
		spellDeps := make([]string, 0, len(resolved))
		for _, l := range resolved {
			spellDeps = append(spellDeps, l.DependsOn(p.Dir)...)
		}
		if len(spellDeps) > 0 {
			p.DependsOn = append(p.DependsOn, spellDeps...)
			slices.Sort(p.DependsOn)
			p.DependsOn = slices.Compact(p.DependsOn)
		}
		if projectOK {
			p.ResolvedSpells = resolved
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}
