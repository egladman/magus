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
)

// ResolveProjects resolves targets to project records; unmatched targets are silently dropped.
func (m *Magus) ResolveProjects(targets []types.Target) []*types.Project {
	return m.targetProjects(targets)
}

// TargetArtifact is one file a target actually produced: a declared output glob
// expanded against the working tree. Glob is carried alongside Path because the
// declaration is what makes the file a build artifact rather than an incidental
// file, and a reader chasing an unexpected artifact needs to know which
// ctx.writesFiles(...) claimed it.
type TargetArtifact struct {
	Path string // workspace-relative
	Glob string // the declaration it matched
	// ProjectPath is the project whose target DECLARED the glob - not necessarily the
	// project the file sits in, since a target may declare an output into another
	// project's tree. Recorded here because this is the only place that knows it: a
	// consumer re-deriving attribution from Path has to guess, and the guess fails
	// outright for a file no project's tree claims.
	ProjectPath string
}

// ResolveTargetOutputs expands the output globs target declares for each project
// into the files that exist on disk right now.
//
// It reads buildStep, not the project-wide union, so the answer is scoped to the
// ONE target asked about - the same fold the cache keys and snapshots, so what this
// reports and what the cache replays cannot disagree.
//
// This is the question an agent otherwise has to guess at: a build says it passed,
// and where the artifact landed is left to be inferred from the target's name.
func (m *Magus) ResolveTargetOutputs(ctx context.Context, projects []*types.Project, target string) ([]TargetArtifact, error) {
	var found []TargetArtifact
	// buildStep's Outputs are WORKSPACE-relative ("api/dist/*.txt"), unlike
	// Project.AllOutputs which is project-relative. Globbing them against the project
	// dir looked for api/api/dist/*.txt and silently found nothing - and the root
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
				found = append(found, TargetArtifact{Path: filepath.ToSlash(wsRel), Glob: glob, ProjectPath: p.Path})
			}
		}
	}
	slices.SortFunc(found, func(a, b TargetArtifact) int { return cmp.Compare(a.Path, b.Path) })
	return slices.CompactFunc(found, func(a, b TargetArtifact) bool { return a.Path == b.Path }), nil
}

// CleanOutputs removes files matched by each project's declared Outputs globs.
// It returns the list of removed absolute file paths. When dryRun is true, no
// files are deleted — only the matched paths are collected and returned.
func (m *Magus) CleanOutputs(ctx context.Context, projects []*types.Project, dryRun bool) ([]string, error) {
	// A real clean deletes declared outputs, so take each project's EXCLUSIVE
	// workspace lock (sorted, deadlock-safe) up front so a concurrent magus process
	// cannot be regenerating the same outputs mid-delete. A dry run removes nothing
	// and takes no lock.
	if !dryRun {
		release, err := m.acquireProjectLocks(ctx, projects)
		if err != nil {
			return nil, err
		}
		defer release()
	}

	var removed []string
	for _, p := range projects {
		if ctx.Err() != nil {
			return removed, ctx.Err()
		}
		fsys := os.DirFS(p.Dir)
		for _, glob := range p.AllOutputs() {
			if ctx.Err() != nil {
				return removed, ctx.Err()
			}
			matches, err := doublestar.Glob(fsys, glob)
			if err != nil {
				return removed, fmt.Errorf("clean %s: expand %q: %w", p.Path, glob, err)
			}
			for _, rel := range matches {
				abs := filepath.Join(p.Dir, rel)
				info, err := os.Lstat(abs)
				if err != nil {
					if os.IsNotExist(err) {
						continue
					}
					return removed, fmt.Errorf("clean %s: stat %q: %w", p.Path, rel, err)
				}
				if info.IsDir() {
					continue // globs may match containing dirs; only remove files
				}
				if !dryRun {
					if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
						return removed, fmt.Errorf("clean %s: remove %q: %w", p.Path, rel, err)
					}
				}
				removed = append(removed, abs)
			}
		}
	}
	return removed, nil
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
// (InboundOutputs) only the WRITER can rebuild it - the owner has no target that produces
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
