package magus

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/egladman/magus/types"
)

// ResolveProjects resolves targets to project records; unmatched targets are silently dropped.
func (m *Magus) ResolveProjects(targets []types.Target) []*types.Project {
	return m.targetProjects(targets)
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
	return m.cache.Clean(ctx, paths...)
}

// FindOutputOwner returns the first project whose declared Outputs globs
// match absPath. absPath must be an absolute filesystem path. Returns nil
// when no project claims the path.
func (m *Magus) FindOutputOwner(absPath string) *types.Project {
	for _, p := range m.ws.All() {
		for _, glob := range p.AllOutputs() {
			rel, err := filepath.Rel(p.Dir, absPath)
			if err != nil {
				continue
			}
			ok, err := doublestar.Match(glob, filepath.ToSlash(rel))
			if err == nil && ok {
				return p
			}
		}
	}
	return nil
}
