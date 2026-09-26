package magus

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// ResolveProjects resolves targets to project records; unmatched targets are silently dropped.
func (m *Magus) ResolveProjects(targets []types.Target) []*types.Project {
	return m.targetProjects(targets)
}

// ResolveTargetOutputs expands the output globs target declares for each project
// into the files that exist on disk right now.
//
// It reads buildStep, not the project-wide union, so the answer is scoped to the
// ONE target asked about: the same fold the cache keys and snapshots, so what this
// reports and what the cache replays cannot disagree.
//
// This is the question an agent otherwise has to guess at: a build says it passed,
// and where the artifact landed is left to be inferred from the target's name.
func (m *Magus) ResolveTargetOutputs(ctx context.Context, projects []*types.Project, target string) ([]types.TargetArtifact, error) {
	var found []types.TargetArtifact
	// buildStep's Outputs are WORKSPACE-relative ("api/dist/*.txt"), unlike
	// Project.AllOutputs which is project-relative. Globbing them against the project
	// dir looked for api/api/dist/*.txt and silently found nothing, and the root
	// project hid it, because there the two spellings are identical.
	fsys := os.DirFS(m.Root())
	for _, p := range projects {
		if ctx.Err() != nil {
			return found, ctx.Err()
		}
		for _, glob := range m.buildStep(p, target).Outputs {
			matches, err := doublestar.Glob(fsys, glob)
			if err != nil {
				return found, fmt.Errorf("%s: expand %q: %w", p.Path, glob, err)
			}
			for _, rel := range matches {
				abs := filepath.Join(m.Root(), rel)
				// A glob can match a directory (dist/** matches dist itself); an
				// artifact list is about files, and a directory entry would make
				// the count disagree with what a consumer can open.
				if fi, statErr := os.Stat(abs); statErr != nil || fi.IsDir() {
					continue
				}
				wsRel, err := filepath.Rel(m.Root(), abs)
				if err != nil {
					continue
				}
				found = append(found, types.TargetArtifact{Path: filepath.ToSlash(wsRel), Glob: glob, ProjectPath: p.Path})
			}
		}
	}
	slices.SortFunc(found, func(a, b types.TargetArtifact) int { return cmp.Compare(a.Path, b.Path) })
	return slices.CompactFunc(found, func(a, b types.TargetArtifact) bool { return a.Path == b.Path }), nil
}

// CleanedOutputs is what [Magus.CleanOutputs] did, or would do under a dry run.
// Both lists hold absolute file paths.
type CleanedOutputs struct {
	Removed []string
	// Tracked are matched outputs the VCS tracks. Clean never removes them: they
	// are committed, so deleting one dirties the tree instead of dropping a
	// build product.
	Tracked []string
}

// CleanOutputs removes files matched by each project's declared Outputs globs,
// except those the workspace's VCS tracks. When dryRun is true nothing is
// deleted and the result reports what a real run would do.
//
// A workspace no VCS claims tracks nothing, so every match is removed. When the
// VCS cannot say which matches it tracks, CleanOutputs returns an error and
// removes nothing.
func (m *Magus) CleanOutputs(ctx context.Context, projects []*types.Project, dryRun bool) (CleanedOutputs, error) {
	// A real clean deletes declared outputs, so take each project's EXCLUSIVE
	// workspace lock (sorted, deadlock-safe) up front so a concurrent magus process
	// cannot be regenerating the same outputs mid-delete. A dry run removes nothing
	// and takes no lock.
	if !dryRun {
		hold, err := m.acquireProjectLocks(ctx, projects, false, nil, nil)
		if err != nil {
			return CleanedOutputs{}, err
		}
		defer hold.release()
	}

	type match struct{ project, rel, abs string }
	var matched []match
	for _, p := range projects {
		if ctx.Err() != nil {
			return CleanedOutputs{}, ctx.Err()
		}
		fsys := os.DirFS(p.Dir)
		for _, glob := range p.AllOutputs() {
			if ctx.Err() != nil {
				return CleanedOutputs{}, ctx.Err()
			}
			found, err := doublestar.Glob(fsys, glob)
			if err != nil {
				return CleanedOutputs{}, fmt.Errorf("clean %s: expand %q: %w", p.Path, glob, err)
			}
			for _, rel := range found {
				abs := filepath.Join(p.Dir, rel)
				info, err := os.Lstat(abs)
				if err != nil {
					if os.IsNotExist(err) {
						continue
					}
					return CleanedOutputs{}, fmt.Errorf("clean %s: stat %q: %w", p.Path, rel, err)
				}
				if info.IsDir() {
					continue // globs may match containing dirs; only remove files
				}
				matched = append(matched, match{project: p.Path, rel: rel, abs: abs})
			}
		}
	}

	abs := make([]string, len(matched))
	for i, mt := range matched {
		abs[i] = mt.abs
	}
	tracked, err := m.trackedPaths(ctx, abs)
	if err != nil {
		return CleanedOutputs{}, fmt.Errorf("nothing removed: %w", err)
	}

	var out CleanedOutputs
	for _, mt := range matched {
		if tracked[mt.abs] {
			out.Tracked = append(out.Tracked, mt.abs)
			continue
		}
		if !dryRun {
			if err := os.Remove(mt.abs); err != nil && !os.IsNotExist(err) {
				return out, fmt.Errorf("clean %s: remove %q: %w", mt.project, mt.rel, err)
			}
		}
		out.Removed = append(out.Removed, mt.abs)
	}
	return out, nil
}

// trackedPaths returns the subset of absPaths the workspace's VCS tracks, keyed
// by the path as given. A workspace outside any checkout, or one with the VCS
// disabled, tracks nothing.
func (m *Magus) trackedPaths(ctx context.Context, absPaths []string) (map[string]bool, error) {
	tracked := make(map[string]bool)
	if len(absPaths) == 0 {
		return tracked, nil
	}
	res, err := vcs.Resolve(ctx, m.ws.Root, "", m.ws.VCSOptions)
	if err != nil {
		return nil, fmt.Errorf("resolve VCS to find tracked outputs: %w", err)
	}
	if res.VCS == nil {
		return tracked, nil
	}
	// Resolve looks for a VCS marker only at the root itself, so a workspace
	// nested inside a checkout lands here too. Reading that as "nothing tracked"
	// would delete committed files, so find the enclosing checkout instead.
	if res.Source == types.VCSSourceDefault {
		checkout, _, _ := vcs.Checkouts(m.ws.Root)
		if checkout == "" {
			return tracked, nil
		}
		if res, err = vcs.Resolve(ctx, checkout, "", m.ws.VCSOptions); err != nil {
			return nil, fmt.Errorf("resolve VCS to find tracked outputs: %w", err)
		}
	}
	byRel := make(map[string]string, len(absPaths))
	rels := make([]string, 0, len(absPaths))
	for _, abs := range absPaths {
		rel, err := filepath.Rel(m.ws.Root, abs)
		if err != nil {
			return nil, fmt.Errorf("%s is outside the workspace: %w", abs, err)
		}
		rel = filepath.ToSlash(rel)
		byRel[rel] = abs
		rels = append(rels, rel)
	}
	known, err := res.VCS.TrackedFiles(ctx, m.ws.Root, rels)
	if err != nil {
		return nil, fmt.Errorf("%s could not report which outputs are tracked: %w", res.VCS.Name(), err)
	}
	for _, rel := range known {
		if abs, ok := byRel[rel]; ok {
			tracked[abs] = true
		}
	}
	return tracked, nil
}

// CleanCache removes all cached build entries for the given projects.
// Pass no projects to clear the entire cache.
func (m *Magus) CleanCache(ctx context.Context, projects ...*types.Project) error {
	if m.cache == nil {
		return nil
	}
	paths := make([]string, 0, len(projects))
	for _, p := range projects {
		paths = append(paths, p.Path)
	}
	return m.cache.Delete(ctx, paths...)
}

// FindOutputProducer returns the project whose target REGENERATES absPath, or nil when
// no project declares the path as an output. absPath must be absolute.
//
// The producer is not always the project the file sits in. For an output a project
// declares for itself the two coincide, but for one another project writes into its tree
// (InboundOutputs) only the WRITER can rebuild it: the owner has no target that produces
// those bytes. The merge driver is the consumer that makes this distinction load-bearing:
// handed the owner, it would run a target that touches nothing, then copy the
// unregenerated file over the conflict and report a clean merge.
func (m *Magus) FindOutputProducer(absPath string) *types.Project {
	matches := func(p *types.Project, glob string) bool {
		rel, err := filepath.Rel(p.Dir, absPath)
		if err != nil {
			return false
		}
		ok, err := doublestar.Match(glob, filepath.ToSlash(rel))
		return err == nil && ok
	}
	for _, p := range m.ws.All() {
		// Sorted, so a path claimed by more than one writer resolves to the same
		// project every run rather than following map iteration order.
		for _, writer := range slices.Sorted(maps.Keys(p.InboundOutputs)) {
			for _, glob := range p.InboundOutputs[writer] {
				if matches(p, glob) {
					return m.ws.Get(writer)
				}
			}
		}
		for _, glob := range p.AllOutputs() {
			if matches(p, glob) {
				return p
			}
		}
	}
	return nil
}
