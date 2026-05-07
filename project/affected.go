package project

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/depgraph"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// Affected returns the set of projects impacted by VCS changes. Errors wrap
// types.ErrAffectedFallback when VCS is disabled or fails so callers fall back to all projects.
func Affected(ctx context.Context, w *types.Workspace, base string) (*types.AffectedResult, error) {
	res, err := vcs.Resolve(ctx, w.Root, base, w.VCSOptions)
	if err != nil {
		return nil, err
	}
	if res.Source == types.VCSSourceDisabled {
		return nil, fmt.Errorf("%w: vcs disabled", types.ErrAffectedFallback)
	}
	rawFiles, err := res.VCS.Diff(ctx, w.Root, res.Base)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, errors.Join(types.ErrAffectedFallback, err)
	}
	changed := normalizeFiles(rawFiles)
	base = res.Base

	idx := newProjectIndex(w)
	seedSet := map[string]struct{}{}
	filesBySeed := map[string][]string{}
	for _, f := range changed {
		if path, ok := idx.projectForFile(f); ok {
			seedSet[path] = struct{}{}
			filesBySeed[path] = append(filesBySeed[path], f)
		}
	}
	seed := make([]string, 0, len(seedSet))
	for path := range seedSet {
		seed = append(seed, path)
	}
	slices.Sort(seed)

	g, err := depgraph.Build(w, graphObserverOpts(ctx)...)
	if err != nil {
		return nil, err
	}
	closure := g.ReverseClosure(seed)
	slices.Sort(closure)

	return &types.AffectedResult{
		Base:        base,
		Changed:     changed,
		Seed:        seed,
		FilesBySeed: filesBySeed,
		Affected:    closure,
	}, nil
}

func normalizeFiles(files []string) []string {
	set := make(map[string]struct{}, len(files))
	for _, f := range files {
		if f = strings.TrimSpace(filepath.ToSlash(f)); f != "" {
			set[f] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for f := range set {
		out = append(out, f)
	}
	slices.Sort(out)
	return out
}

// AffectedFromPaths computes the affected set from explicit paths without VCS.
// Absolute paths outside the workspace root are silently skipped.
func AffectedFromPaths(ctx context.Context, w *types.Workspace, paths []string) (*types.AffectedResult, error) {
	idx := newProjectIndex(w)
	seedSet := map[string]struct{}{}
	filesBySeed := map[string][]string{}

	for _, f := range paths {
		if filepath.IsAbs(f) {
			rel, err := filepath.Rel(w.Root, f)
			if err != nil || strings.HasPrefix(rel, "..") {
				continue
			}
			f = rel
		}
		f = filepath.ToSlash(f)
		if path, ok := idx.projectForFile(f); ok {
			seedSet[path] = struct{}{}
			filesBySeed[path] = append(filesBySeed[path], f)
		}
	}

	seed := make([]string, 0, len(seedSet))
	for path := range seedSet {
		seed = append(seed, path)
	}
	slices.Sort(seed)

	g, err := depgraph.Build(w, graphObserverOpts(ctx)...)
	if err != nil {
		return nil, err
	}
	closure := g.ReverseClosure(seed)
	slices.Sort(closure)

	return &types.AffectedResult{
		Base:        "paths",
		Changed:     append([]string(nil), paths...),
		Seed:        seed,
		FilesBySeed: filesBySeed,
		Affected:    closure,
	}, nil
}

// Where returns the innermost project containing dir (absolute or workspace-relative).
func Where(w *types.Workspace, dir string) (*types.Project, bool) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, false
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, false
	}
	if !strings.HasPrefix(abs+string(filepath.Separator), w.Root+string(filepath.Separator)) && abs != w.Root {
		return nil, false
	}
	cur := abs
	for {
		rel := projectPath(w.Root, cur)
		if p, ok := w.Projects[rel]; ok && rel != "." {
			return p, true
		}
		if cur == w.Root {
			return nil, false
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return nil, false
		}
		cur = parent
	}
}

// projectIndex pre-sorts project paths by descending length for O(n) longest-prefix lookup.
type projectIndex struct {
	w     *types.Workspace
	paths []string // sorted by descending length
}

func newProjectIndex(w *types.Workspace) *projectIndex {
	paths := make([]string, 0, len(w.Projects))
	for path := range w.Projects {
		if path == "." {
			continue
		}
		paths = append(paths, path)
	}
	slices.SortFunc(paths, func(a, b string) int { return len(b) - len(a) })
	return &projectIndex{w: w, paths: paths}
}

// projectForFile returns the innermost project for a repo-relative file path.
func (idx *projectIndex) projectForFile(file string) (string, bool) {
	file = filepath.ToSlash(file)
	for _, path := range idx.paths {
		if file == path || strings.HasPrefix(file, path+"/") {
			return path, true
		}
	}
	if _, ok := idx.w.Projects["."]; ok {
		return ".", true
	}
	return "", false
}

// graphObserverOpts extracts the request-scoped graph observer from ctx, if set.
func graphObserverOpts(ctx context.Context) []types.GraphOption {
	if o := types.GraphObserverFromContext(ctx); o != nil {
		return []types.GraphOption{types.WithGraphObserver(o)}
	}
	return nil
}
