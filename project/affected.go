package project

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/graph/dependency"
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
	prefix := vcsRootPrefix(w.Root, res.VCS.Claims())
	changed := workspaceRelative(prefix, normalizeFiles(rawFiles))
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

	g, err := dependency.Build(w, graphObserverOpts(ctx)...)
	if err != nil {
		return nil, err
	}
	closure := g.ReverseClosure(seed)
	slices.Sort(closure)

	// The affected-set derivation, end to end: the diff base, how many files
	// changed, the projects that directly contain them (seed), and the dependency
	// closure that selection expands to. The starting point for "why did magus run
	// X?" (or "why didn't it run Y?"). -vv surfaces it.
	slog.DebugContext(ctx, "affected: derived set",
		slog.String("base", base),
		slog.Int("changed_files", len(changed)),
		slog.Any("seed", seed),
		slog.Any("affected", closure),
	)

	return &types.AffectedResult{
		Base:        base,
		Changed:     changed,
		Seed:        seed,
		FilesBySeed: filesBySeed,
		Affected:    closure,
	}, nil
}

// vcsRootPrefix returns the slash-terminated path from the VCS root down to wsRoot
// (e.g. "magus/"), or "" when wsRoot is itself the VCS root or no marker is found.
// It walks up from wsRoot for any of the VCS's claim markers (.git, .hg, …) with
// plain filesystem stats — no `git rev-parse` subprocess — and because the marker
// dir is an ancestor of wsRoot by construction, the prefix is a pure lexical slice
// (no absolute-path resolution or symlink evaluation).
func vcsRootPrefix(wsRoot string, claims []string) string {
	for dir := wsRoot; ; {
		for _, c := range claims {
			if _, err := os.Stat(filepath.Join(dir, c)); err == nil {
				rel, err := filepath.Rel(dir, wsRoot)
				if err != nil || rel == "." {
					return "" // wsRoot is the VCS root; diff paths already match
				}
				return filepath.ToSlash(rel) + "/"
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "" // reached the filesystem root without a marker (best-effort)
		}
		dir = parent
	}
}

// workspaceRelative rewrites VCS diff paths — reported relative to the VCS root — to
// be relative to the workspace root by stripping prefix, dropping any path that
// falls outside the workspace. When a workspace is nested below its VCS root (e.g. a
// magus workspace in a subdir of a larger monorepo), every diff path otherwise
// carries the subdir prefix, matches no project in projectForFile, and collapses the
// affected set onto the root project so nested projects never seed. An empty prefix
// (workspace is the VCS root, the common case) returns the input untouched.
func workspaceRelative(prefix string, files []string) []string {
	if prefix == "" {
		return files
	}
	out := make([]string, 0, len(files))
	for _, f := range files {
		if rel, ok := strings.CutPrefix(f, prefix); ok {
			out = append(out, rel)
		}
	}
	return out
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

	g, err := dependency.Build(w, graphObserverOpts(ctx)...)
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
