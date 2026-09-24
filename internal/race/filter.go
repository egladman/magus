package race

import (
	"context"
	"path/filepath"
	"sync"

	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// trackedFilter checks whether a path is tracked by the workspace's VCS. Tracked files
// are the only ones eligible for race findings: tool-generated files, caches, and build
// artifacts are excluded by definition.
type trackedFilter struct {
	tracked func() map[string]struct{}
}

// newTrackedFilter lists root's tracked files on the first Allow, so a run with nothing to
// filter spawns nothing. Not a repository, or a backend that cannot answer, allows nothing,
// so there are no race findings.
func newTrackedFilter(ctx context.Context, root string) *trackedFilter {
	return &trackedFilter{tracked: sync.OnceValue(func() map[string]struct{} {
		out := map[string]struct{}{}
		res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{})
		if err != nil || res.VCS == nil {
			return out
		}
		// "." is the whole tree in one listing.
		files, err := res.VCS.TrackedFiles(ctx, root, []string{"."})
		if err != nil {
			return out
		}
		for _, rel := range files {
			out[filepath.Join(root, filepath.FromSlash(rel))] = struct{}{}
		}
		return out
	})}
}

// Allow returns true if path is tracked and should be considered for race detection.
func (f *trackedFilter) Allow(path string) bool {
	_, ok := f.tracked()[path]
	return ok
}
