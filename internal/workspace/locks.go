package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
)

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
