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

	"github.com/bmatcuk/doublestar/v4"

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
	rawFiles, err := res.VCS.ChangedFiles(ctx, w.Root, res.Base)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, errors.Join(types.ErrAffectedFallback, err)
	}
	prefix := vcsRootPrefix(w.Root, res.VCS.Claims())
	changed := workspaceRelative(prefix, normalizeFiles(rawFiles))
	base = res.Base

	seed, filesBySeed, undeclaredBySeed := attribute(newProjectIndex(w), changed)

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
		Base:             base,
		Changed:          changed,
		Seed:             seed,
		FilesBySeed:      filesBySeed,
		UndeclaredBySeed: undeclaredBySeed,
		Affected:         closure,
	}, nil
}

// attribute maps changed files onto the projects they seed, alongside the subset of
// them no project declares. Shared by the VCS path and the explicit-paths path so
// the two cannot drift on what seeds what.
func attribute(idx *projectIndex, files []string) (seed []string, filesBySeed, undeclaredBySeed map[string][]string) {
	seedSet := map[string]struct{}{}
	filesBySeed = map[string][]string{}
	undeclaredBySeed = map[string][]string{}
	for _, f := range files {
		paths, declared := idx.seedsForFile(f)
		for _, path := range paths {
			seedSet[path] = struct{}{}
			filesBySeed[path] = append(filesBySeed[path], f)
			if !declared {
				undeclaredBySeed[path] = append(undeclaredBySeed[path], f)
			}
		}
	}
	seed = make([]string, 0, len(seedSet))
	for path := range seedSet {
		seed = append(seed, path)
	}
	slices.Sort(seed)
	if len(undeclaredBySeed) == 0 {
		undeclaredBySeed = nil
	}
	return seed, filesBySeed, undeclaredBySeed
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
	rel := make([]string, 0, len(paths))
	for _, f := range paths {
		if filepath.IsAbs(f) {
			r, err := filepath.Rel(w.Root, f)
			if err != nil || strings.HasPrefix(r, "..") {
				continue
			}
			f = r
		}
		rel = append(rel, filepath.ToSlash(f))
	}
	seed, filesBySeed, undeclaredBySeed := attribute(newProjectIndex(w), rel)

	g, err := dependency.Build(w, graphObserverOpts(ctx)...)
	if err != nil {
		return nil, err
	}
	closure := g.ReverseClosure(seed)
	slices.Sort(closure)

	return &types.AffectedResult{
		Base:             "paths",
		Changed:          append([]string(nil), paths...),
		Seed:             seed,
		FilesBySeed:      filesBySeed,
		UndeclaredBySeed: undeclaredBySeed,
		Affected:         closure,
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

// projectIndex pre-sorts project paths by descending length for O(n) longest-prefix
// lookup, and carries each project's workspace-rooted declarations for the glob half
// of attribution.
type projectIndex struct {
	w     *types.Workspace
	paths []string            // sorted by descending length; excludes "."
	globs map[string][]string // project path -> its DeclaredGlobs, "." included
}

func newProjectIndex(w *types.Workspace) *projectIndex {
	paths := make([]string, 0, len(w.Projects))
	globs := make(map[string][]string, len(w.Projects))
	for path, p := range w.Projects {
		globs[path] = p.DeclaredGlobs()
		if path == "." {
			continue
		}
		paths = append(paths, path)
	}
	slices.SortFunc(paths, func(a, b string) int { return len(b) - len(a) })
	return &projectIndex{w: w, paths: paths, globs: globs}
}

// projectForFile returns the innermost project DIRECTORY containing a repo-relative
// file path, falling back to the root project. It answers ownership ("whose tree is
// this in"), which is what the VCS insight lenses attribute churn with; seedsForFile
// is the affected-set question and layers declarations on top of this one.
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

// seedsForFile returns the projects a changed file seeds, and whether ANY project
// declares it. This is the seam attribution turns on; it used to be directory
// containment alone, with the root project as an unconditional catch-all.
//
// Three rules, in order, and the order is the whole design:
//
//  1. A project whose DIRECTORY contains the file owns it. Containment is the most
//     specific claim there is, and it is what makes today's affected sets correct -
//     a broad parent glob (the root's "**/*.go" reaches every nested Go file in this
//     very repo) must not add the parent as a second seed on every child edit.
//  2. Otherwise, every project that DECLARES the path seeds. This is the redirect:
//     a file outside every project tree that a project reads through a reaching glob
//     ("../proto/**") now seeds the project whose cache key it actually moves,
//     instead of the root project that merely happens to sit above it.
//  3. Otherwise the root project seeds it anyway. That catch-all is load-bearing and
//     stays: a config nobody declares still changes what a build MEANS, and seeding
//     is the only reason editing one reruns anything at all. It is reported rather
//     than narrowed - see the declared return, MGS1028, and doctor's advice.
//
// declared is false only in case 3 and in the containment case where no project
// declares the path either: precisely the files that rerun work without moving a
// cache key.
func (idx *projectIndex) seedsForFile(file string) (paths []string, declared bool) {
	file = filepath.ToSlash(file)
	owner, hasOwner := idx.projectForFile(file)
	contained := hasOwner && owner != "."

	var declaring []string
	for path, globs := range idx.globs {
		if !matchAnyGlob(globs, file) {
			continue
		}
		// Containment already decided the seed, so the declarations are down to a
		// yes/no and the first match settles it. This is the common path - a source
		// file inside its own project - and it is what keeps the scan off the hot
		// loop for a large changeset.
		if contained {
			return []string{owner}, true
		}
		declaring = append(declaring, path)
	}
	if contained {
		return []string{owner}, false
	}
	if len(declaring) > 0 {
		slices.Sort(declaring)
		return declaring, true
	}
	if _, ok := idx.w.Projects["."]; ok {
		return []string{"."}, false
	}
	return nil, false
}

func matchAnyGlob(globs []string, file string) bool {
	for _, g := range globs {
		if ok, _ := doublestar.Match(g, file); ok {
			return true
		}
	}
	return false
}

// graphObserverOpts extracts the request-scoped graph observer from ctx, if set.
func graphObserverOpts(ctx context.Context) []types.GraphOption {
	if o := types.GraphObserverFromContext(ctx); o != nil {
		return []types.GraphOption{types.WithGraphObserver(o)}
	}
	return nil
}
