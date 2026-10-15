package config

import (
	"fmt"
	"path/filepath"

	"github.com/egladman/magus/libs/xdg"
	"github.com/egladman/magus/vcs"
)

// CacheDir resolves the workspace cache directory: cfg's Cache.Dir, which MAGUS_CACHE_DIR
// sets when the environment is overlaid on cfg, then <root>/.magus. A relative dir joins
// to root. It holds what one checkout keeps: run journals, locks, output records; the
// cache entries live in StoreDir.
func CacheDir(root string, cfg Config) string {
	if cfg.Cache.Dir != "" {
		if filepath.IsAbs(cfg.Cache.Dir) {
			return filepath.Clean(cfg.Cache.Dir)
		}
		return filepath.Join(root, cfg.Cache.Dir)
	}
	return filepath.Join(root, ".magus")
}

// StoreDir resolves where the workspace at root keeps its cache entries (cas/,
// manifests/, logs/). A key names no checkout, so by default every checkout of the
// repository shares one store: <user cache>/magus/cache/<repo>-<hash12>, keyed on the
// repository's identity as vcs.StateDir defines it, so worktrees and clones of one
// repository agree. A cache.dir (or MAGUS_CACHE_DIR) keeps the entries in CacheDir
// with everything else, as one directory.
//
// golangci-lint's own cache stays in the checkout: it records absolute paths, so a
// sibling worktree would replay another's findings.
//
// It fails rather than fall back to the checkout when no user cache dir resolves: a
// store that silently stopped being shared would rebuild every key in every worktree.
func StoreDir(root string, cfg Config) (string, error) {
	if cfg.Cache.Dir != "" {
		return CacheDir(root, cfg), nil
	}
	base, err := xdg.CacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve the shared cache dir, set XDG_CACHE_HOME to a writable absolute path or cache.dir: %w", err)
	}
	return vcs.StateDir(base, "cache", root)
}
