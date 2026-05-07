// Package diff provides mtime+size snapshot diffing for attributing concurrent file writes.
// Not cryptographic; use cache.Hash for exact-content guarantees.
package diff

import (
	"crypto/sha256"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Snap is a snapshot of files keyed by absolute path; values pack mtime_ns and size into one int64.
type Snap map[string]int64

// pack encodes mtime+size into one int64. Collisions are benign (worst case: missed diff).
func pack(mtimeNs, size int64) int64 { return mtimeNs ^ (size << 17) }

// Take walks each directory in dirs and records the current mtime+size for
// every regular file found. Directories that do not exist are silently
// skipped. Symlinks are not followed.
func Take(dirs []string) Snap {
	snap := make(Snap, 64)
	for _, dir := range dirs {
		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || d.Type()&fs.ModeSymlink != 0 {
				return nil //nolint:nilerr // WalkDir: skip unreadable/dir/symlink entries, continue walking
			}
			if fi, err := d.Info(); err == nil {
				snap[path] = pack(fi.ModTime().UnixNano(), fi.Size())
			}
			return nil
		})
	}
	return snap
}

// ContentSnap is a SHA-256 snapshot per path; used for determinism replay where mtime+size is insufficient.
type ContentSnap map[string][32]byte

// HashContent walks each directory in dirs and returns a SHA-256 digest per
// regular file. Missing directories are silently skipped.
func HashContent(dirs []string) ContentSnap {
	snap := make(ContentSnap, 64)
	for _, dir := range dirs {
		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || d.Type()&fs.ModeSymlink != 0 {
				return nil //nolint:nilerr // WalkDir: skip unreadable/dir/symlink entries, continue walking
			}
			f, err := os.Open(path) //nolint:gosec // G122: symlinks are filtered above; path is a regular file under the workspace root
			if err != nil {
				return nil //nolint:nilerr // skip files we cannot open, continue walking
			}
			h := sha256.New()
			_, _ = io.Copy(h, f)
			_ = f.Close()
			var sum [32]byte
			copy(sum[:], h.Sum(nil))
			snap[path] = sum
			return nil
		})
	}
	return snap
}

// DiffContent returns paths whose content differs between pre and post (added/removed counts as different).
func DiffContent(pre, post ContentSnap) []string {
	var out []string
	for path, postHash := range post {
		if preHash, ok := pre[path]; !ok || preHash != postHash {
			out = append(out, path)
		}
	}
	for path := range pre {
		if _, ok := post[path]; !ok {
			out = append(out, path)
		}
	}
	return out
}

// Changed returns paths added or modified between pre and post. Deletions are not reported.
func Changed(pre, post Snap) []string {
	var out []string
	for path, postVal := range post {
		if preVal, ok := pre[path]; !ok || preVal != postVal {
			out = append(out, path)
		}
	}
	return out
}

// GlobBaseDirs extracts the non-wildcard base directory from each glob pattern for use as walk roots.
func GlobBaseDirs(root string, globs []string) []string {
	seen := make(map[string]struct{}, len(globs))
	var out []string
	for _, g := range globs {
		// Find the first wildcard character.
		cut := len(g)
		for i, c := range g {
			if c == '*' || c == '?' || c == '[' || c == '{' {
				cut = i
				break
			}
		}

		var dir string
		if cut == len(g) {
			// No wildcard: treat g as a file path, use its directory.
			dir = filepath.Dir(filepath.Join(root, g))
		} else {
			// Has wildcard: the non-wildcard prefix is the directory.
			// filepath.Clean strips the trailing separator.
			dir = filepath.Clean(filepath.Join(root, g[:cut]))
		}

		if _, ok := seen[dir]; !ok {
			seen[dir] = struct{}{}
			out = append(out, dir)
		}
	}
	return out
}
