package project

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// Discover walks root and returns a *types.Workspace. Only directories with a
// magusfile are registered; explicit spell registration in the magusfile
// is required (auto-detection via spell markers has been retired).
func Discover(_ context.Context, root string) (*types.Workspace, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("magus/project: abs %q: %w", root, err)
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("magus/project: eval-symlinks %q: %w", abs, err)
	}

	if c, ok := loadWSCache(abs); ok && c.valid() {
		return restoreFromCache(abs, c), nil
	}

	ws := &types.Workspace{Root: abs, Projects: map[string]*types.Project{}}
	var mu sync.Mutex
	dirMtimes := make(map[string]int64)

	walkErr := filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		base := d.Name()
		if path != abs {
			if IsIgnoreDir(base) || vcs.IsSecondaryCheckout(path) {
				return fs.SkipDir
			}
		}
		if info, serr := d.Info(); serr == nil {
			dirMtimes[path] = info.ModTime().UnixNano()
		}
		rel := projectPath(abs, path)
		if hasDeclaration(path) {
			mu.Lock()
			ws.Projects[rel] = &types.Project{Path: rel, Dir: path}
			mu.Unlock()
		}
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("magus/project: walk %q: %w", abs, walkErr)
	}

	saveWSCache(abs, ws, dirMtimes)

	return ws, nil
}

func projectPath(root, dir string) string {
	if dir == root {
		return "."
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return dir
	}
	return filepath.ToSlash(rel)
}

// hasDeclaration reports whether dir has a declaration file or matching
// declaration-dir glob for any registered spell.
func hasDeclaration(dir string) bool {
	for _, s := range defaultRegistry.All() {
		for _, f := range s.DeclarationFiles() {
			if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
				return true
			}
		}
		for _, glob := range s.DeclarationDirGlobs() {
			matches, err := filepath.Glob(filepath.Join(dir, glob))
			if err == nil && len(matches) > 0 {
				return true
			}
		}
	}
	return false
}

type extraArgsKey struct{}

// WithExtraArgs returns a context carrying extra args for spell invocations.
func WithExtraArgs(ctx context.Context, args []string) context.Context {
	return context.WithValue(ctx, extraArgsKey{}, args)
}

// ExtraArgs returns the extra args stored by WithExtraArgs, or nil.
func ExtraArgs(ctx context.Context) []string {
	v, _ := ctx.Value(extraArgsKey{}).([]string)
	return v
}
