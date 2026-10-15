package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"

	"github.com/egladman/magus/internal/config"
)

// CacheDir resolves the workspace cache directory: cfg's Cache.Dir, which MAGUS_CACHE_DIR
// sets when the environment is overlaid on cfg, then <root>/.magus. A relative dir joins
// to root.
func CacheDir(root string, cfg config.Config) string {
	if cfg.Cache.Dir != "" {
		if filepath.IsAbs(cfg.Cache.Dir) {
			return filepath.Clean(cfg.Cache.Dir)
		}
		return filepath.Join(root, cfg.Cache.Dir)
	}
	return filepath.Join(root, ".magus")
}

// LocksDir is where the workspace at root keeps its project locks and pipe records
// inside cacheDir: <cacheDir>/locks/<key>. The key hashes the absolute root rather than
// embedding it: it is the identity that matters, a path is not a legal single directory
// name, and a digest keeps the lock tree shallow. It keeps a cache dir shared by several
// workspaces from merging their locks.
func LocksDir(cacheDir, root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	sum := sha256.Sum256([]byte(filepath.Clean(abs)))
	return filepath.Join(cacheDir, "locks", hex.EncodeToString(sum[:8]))
}
