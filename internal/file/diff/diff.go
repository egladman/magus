// Package diff provides mtime+size snapshot diffing for attributing concurrent file writes.
// Not cryptographic; use cache.Hash for exact-content guarantees.
package diff

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
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

// OutputGlobs is one root directory and the declared output globs RELATIVE to it.
//
// Relative because the root is a literal path and a glob is a pattern: joining them fed the
// checkout's own directory name to the glob engine, so a repository under a path containing
// `[`, `]`, `{` or `}` matched nothing and the replay passed having hashed zero files.
// doublestar has no QuoteMeta, so keeping the root out of the pattern is the only fix that
// covers the whole class. Globbing against os.DirFS(Root) also makes a `..` glob unable to
// reach outside the root at all.
//
// A slice, not one root, because a target may declare an output into another project's tree.
type OutputGlobs struct {
	Root  string   // absolute
	Globs []string // relative to Root, as the magusfile declared them
}

// HashContent returns a SHA-256 digest per regular file matching one of the declared output
// globs in sets. A malformed glob is an error; a glob matching nothing is not.
//
// It expands globs exactly the way the cache snapshot does (internal/cache/snapshot.go):
// glob against the root, and where a match is a DIRECTORY, take every file beneath it. That
// agreement is the point. An earlier version walked base directories and matched each file
// against the pattern, which reads as equivalent and is not: `*` does not cross a separator,
// so a declared "dist/*" matched the cache's `dist/linux_amd64/` directory and none of the
// files inside it. The replay then compared an empty snapshot and reported the target
// deterministic, and the workaround was to widen a declaration that had been right all along.
//
// Globbing rather than walk-then-filter also makes the malformed-glob promise
// unconditional: doublestar.Glob reports ErrBadPattern whether or not anything matches,
// where a matcher only consulted per walked file could hide a typo behind an earlier glob
// that matched, or behind an empty output directory.
func HashContent(ctx context.Context, sets []OutputGlobs) (ContentSnap, error) {
	snap := make(ContentSnap, 64)
	for _, set := range sets {
		rootFS := os.DirFS(set.Root)
		for _, g := range set.Globs {
			matches, err := doublestar.Glob(rootFS, filepath.ToSlash(g))
			if err != nil {
				return nil, fmt.Errorf("declared output glob %q: %w", g, err)
			}
			for _, m := range matches {
				if err := hashPath(ctx, snap, filepath.Join(set.Root, m)); err != nil {
					return nil, err
				}
			}
		}
	}
	return snap, nil
}

// hashPath records abs when it is a regular file, and every regular file beneath it when it
// is a directory. Symlinks are not followed.
func hashPath(ctx context.Context, snap ContentSnap, abs string) error {
	info, err := os.Lstat(abs)
	if err != nil {
		return nil //nolint:nilerr // a match that vanished between glob and stat is not an output
	}
	if !info.IsDir() {
		if info.Mode()&os.ModeSymlink == 0 {
			hashFileInto(snap, abs)
		}
		return nil
	}
	return filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Type()&fs.ModeSymlink != 0 {
			return nil //nolint:nilerr // skip unreadable/dir/symlink entries, keep walking
		}
		// A replay hashes every output tree twice, so this is the longest stretch in the run
		// with nothing else to interrupt it.
		if err := ctx.Err(); err != nil {
			return err
		}
		hashFileInto(snap, path)
		return nil
	})
}

func hashFileInto(snap ContentSnap, path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		// A partial hash would read as non-determinism on the next pass, which is now a hard
		// failure - drop the entry instead and let the missing path speak.
		return
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	snap[path] = sum
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
			// Has wildcard: the directory is everything before the wildcard's path
			// SEGMENT, not just before the wildcard character itself - a mid-segment
			// wildcard like "gen/index*.html" has cut mid-filename, and g[:cut]
			// ("gen/index") is not a directory at all.
			prefix := g[:cut]
			if i := strings.LastIndexByte(prefix, '/'); i >= 0 {
				prefix = prefix[:i]
			} else {
				prefix = ""
			}
			dir = filepath.Clean(filepath.Join(root, prefix))
		}

		if _, ok := seen[dir]; !ok {
			seen[dir] = struct{}{}
			out = append(out, dir)
		}
	}
	return out
}
