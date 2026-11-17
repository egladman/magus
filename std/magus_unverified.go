//go:build !wasm

package std

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/unverified"
	"github.com/egladman/magus/types"
)

// MagusWorkspaceUnverified backs magus\workspace(dir).unverified(revision). A guard rule
// has no workspace on its context, so it takes the reader the guard pinned for the checkout
// it judges; elsewhere it reads the workspace on ctx and refuses a dir outside it. A relative
// dir is read from the process's working directory.
func MagusWorkspaceUnverified(ctx context.Context, w types.MagusWorkspace, revision string) (types.UnverifiedReport, error) {
	if revision == "" {
		return types.UnverifiedReport{}, fmt.Errorf("magus\\workspace(`%s`).unverified: revision is empty, pass the commit to read, such as req.push.revision", w.Dir)
	}
	if read, pinned := unverified.PinnedReader(ctx); pinned {
		return read(ctx, w.Dir, revision)
	}
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return types.UnverifiedReport{}, errorNoWorkspace("workspace.unverified")
	}
	cacheDir, err := checkoutCacheDir(ctx, "workspace.unverified")
	if err != nil {
		return types.UnverifiedReport{}, err
	}
	root := ws.Root()
	dir := cmp.Or(w.Dir, root)
	// A relative dir is the script's, as every other path it names is.
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if !within(root, dir) {
		return types.UnverifiedReport{}, fmt.Errorf("magus\\workspace(`%s`).unverified: `%s` is outside this workspace (`%s`), whose runs are the only ones it reads", dir, dir, root)
	}
	return unverified.Open(ctx, unverified.Checkout{
		Dir: dir, Workspace: root, CacheDir: cacheDir, VCS: ws.VCSOptions(),
		Projects: func(context.Context) (unverified.Projects, error) { return ws, nil },
	}, revision), nil
}

// within reports whether dir is root or inside it, symlinks resolved where they exist.
func within(root, dir string) bool {
	rel, err := filepath.Rel(resolved(root), resolved(dir))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func resolved(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}
