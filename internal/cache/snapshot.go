package cache

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/egladman/magus/internal/cache/reflink"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// snapshot records the project's declared outputs into the cache and writes the manifest.
// A project with no declared outputs records an empty manifest (correct cache hit on rerun).
func (c *Cache) snapshot(ctx context.Context, s Step, hash string) ([]string, error) {
	root := s.WorkspaceRoot
	matches, err := expandOutputGlobs(s.Outputs, root)
	if err != nil {
		return nil, err
	}
	// Only a target's OWN declaration makes an empty result an error - see Step.OutputsDeclared.
	if len(matches) == 0 && len(s.Outputs) > 0 && s.OutputsDeclared {
		return nil, fmt.Errorf("snapshot: target %q in project %q declared outputs but produced none: %v",
			s.Target, s.ProjectPath, s.Outputs)
	}
	// Each required glob is checked on its own, not folded into the all-or-nothing test
	// above: a target declaring its own outputs alongside a cross-project one passes that
	// test on its own outputs alone, and the missing foreign file goes unnoticed.
	for _, g := range s.RequiredOutputs {
		found, err := expandOutputGlobs([]string{g}, root)
		if err != nil {
			return nil, err
		}
		if len(found) == 0 {
			return nil, types.DiagnosticErrorf(types.CrossOutputNotProduced,
				"target %q declared an output into another project (%q) but produced no file matching it; check the path the target actually writes",
				s.Target, g)
		}
	}
	manifest := &Manifest{
		ProjectPath: s.ProjectPath,
		Hash:        hash,
		Target:      s.Target,
		CreatedAt:   time.Now().UTC(),
	}
	// Carry the target's return value onto the entry so a hit can replay it; absent
	// for a void target, which is nearly all of them.
	if v, ok := types.ReturnFor(ctx, s.ProjectPath, s.Target); ok {
		// Stored verbatim on purpose: a HIT replays this value, so masking it here would
		// make the second run differ from the first. The remote copy is redacted at
		// export instead (see exportArtifact).
		manifest.Return = v
	}
	var written []string
	for _, m := range matches {
		rec, err := c.snapshotOne(m.abs, m.rel)
		if err != nil {
			return nil, err
		}
		manifest.Outputs = append(manifest.Outputs, rec)
		written = append(written, m.abs)
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := writeAtomic(c.manifestPath(s.ProjectPath, hash), data); err != nil {
		return nil, err
	}
	return written, nil
}

func (c *Cache) snapshotOne(abs, rel string) (OutputRecord, error) {
	info, err := os.Lstat(abs)
	if err != nil {
		return OutputRecord{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(abs)
		if err != nil {
			return OutputRecord{}, err
		}
		return OutputRecord{Path: rel, Symlink: target, Mode: uint32(info.Mode() & 0o777)}, nil
	}
	if info.IsDir() {
		return OutputRecord{}, fmt.Errorf("snapshotOne: %s is a directory (use a glob like %s/**)", rel, rel)
	}
	hash, err := hashFile(abs)
	if err != nil {
		return OutputRecord{}, err
	}
	dst := c.blobPath(hash)
	if _, err := os.Stat(dst); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return OutputRecord{}, err
		}
		tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".tmp.*")
		if err != nil {
			return OutputRecord{}, err
		}
		tmpName := tmp.Name()
		defer func() { _ = os.Remove(tmpName) }()
		if err := copyToFile(abs, tmp); err != nil {
			_ = tmp.Close()
			return OutputRecord{}, err
		}
		if err := tmp.Sync(); err != nil {
			_ = tmp.Close()
			return OutputRecord{}, err
		}
		if err := tmp.Close(); err != nil {
			return OutputRecord{}, err
		}
		if err := os.Rename(tmpName, dst); err != nil {
			return OutputRecord{}, err
		}
	}
	return OutputRecord{
		Path: rel,
		Blob: hash,
		Mode: uint32(info.Mode() & 0o777),
		Size: info.Size(),
	}, nil
}

// expandOutputGlobs expands output globs relative to root; rejects absolute paths and "..".
func expandOutputGlobs(globs []string, root string) ([]relAbs, error) {
	rootFS := os.DirFS(root)
	seen := map[string]struct{}{}
	var out []relAbs
	for _, g := range globs {
		if filepath.IsAbs(g) || strings.Contains(g, "..") {
			return nil, fmt.Errorf("output glob must be repo-relative without ..: %q", g)
		}
		g = filepath.ToSlash(g)
		matches, err := doublestar.Glob(rootFS, g)
		if err != nil {
			return nil, fmt.Errorf("glob %q: %w", g, err)
		}
		for _, m := range matches {
			abs := filepath.Join(root, m)
			info, err := os.Lstat(abs)
			if err != nil {
				continue
			}
			if info.IsDir() {
				err := filepath.WalkDir(abs, func(p string, d os.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if d.IsDir() {
						return nil
					}
					rel, _ := filepath.Rel(root, p)
					rel = filepath.ToSlash(rel)
					if _, ok := seen[rel]; ok {
						return nil
					}
					seen[rel] = struct{}{}
					out = append(out, relAbs{rel: rel, abs: p})
					return nil
				})
				if err != nil {
					return nil, err
				}
				continue
			}
			if _, ok := seen[m]; ok {
				continue
			}
			seen[m] = struct{}{}
			out = append(out, relAbs{rel: m, abs: abs})
		}
	}
	slices.SortFunc(out, func(a, b relAbs) int { return cmp.Compare(a.rel, b.rel) })
	return out, nil
}

// safeReplayPath joins a manifest record's path onto root and rejects any result
// that escapes it. expandOutputGlobs applies the same rule when a manifest is
// written, but a manifest read back from a shared remote cache or from
// `magus config cache import` never passed through it, and replay writes with the
// mode the record names - so an unchecked path is an arbitrary-write primitive.
func safeReplayPath(root, rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) || strings.Contains(rel, "..") {
		return "", fmt.Errorf("magus/cache: unsafe output path %q", rel)
	}
	dst := filepath.Clean(filepath.Join(root, filepath.FromSlash(rel)))
	if !strings.HasPrefix(dst, root+string(filepath.Separator)) {
		return "", fmt.Errorf("magus/cache: unsafe output path %q", rel)
	}
	return dst, nil
}

// checkReplayParent verifies dst's parent directory still resolves inside root
// after symlink evaluation. Rejecting ".." is not sufficient on its own: an
// earlier record in the same manifest can plant a symlinked directory that a
// later record then writes through, and MkdirAll/os.Create both follow it.
func checkReplayParent(root, dst, rel string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(dst))
	if err != nil {
		return err
	}
	if parent != realRoot && !strings.HasPrefix(parent, realRoot+string(filepath.Separator)) {
		return fmt.Errorf("magus/cache: output path %q escapes the workspace through a symlinked parent", rel)
	}
	return nil
}

// safeSymlinkTarget rejects a restored symlink whose target resolves outside
// root. The link itself writes nothing, but it is a pivot: a later record, or
// the next build, writes through it.
func safeSymlinkTarget(root, dst, target, rel string) error {
	resolved := target
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(filepath.Dir(dst), resolved)
	}
	resolved = filepath.Clean(resolved)
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	if resolved != realRoot && !strings.HasPrefix(resolved, realRoot+string(filepath.Separator)) &&
		!strings.HasPrefix(resolved, root+string(filepath.Separator)) {
		return fmt.Errorf("magus/cache: output %q is a symlink to %q, outside the workspace", rel, target)
	}
	return nil
}

// replay restores a manifest's outputs: reflink → hard link → byte copy.
func (c *Cache) replay(ctx context.Context, m *Manifest, root string) ([]string, error) {
	var paths []string
	for _, rec := range m.Outputs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		dst, err := safeReplayPath(root, rec.Path)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, err
		}
		if err := checkReplayParent(root, dst, rec.Path); err != nil {
			return nil, err
		}
		if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("replay %s: remove existing: %w", rec.Path, err)
		}
		if rec.Symlink != "" {
			if err := safeSymlinkTarget(root, dst, rec.Symlink, rec.Path); err != nil {
				return nil, err
			}
			if err := os.Symlink(rec.Symlink, dst); err != nil {
				return nil, err
			}
			paths = append(paths, dst)
			continue
		}
		blob := c.blobPath(rec.Blob)
		if err := replayBlob(blob, dst); err != nil {
			return nil, fmt.Errorf("replay %s: %w", rec.Path, err)
		}
		if rec.Mode != 0 {
			_ = file.Chmod(dst, os.FileMode(rec.Mode&0o777)) // best-effort (no-op on wasm)
		}
		paths = append(paths, dst)
	}
	return paths, nil
}

// replayBlob materialises blob at dst (dst must not exist). Tries reflink (CoW) → copy.
// Both yield a file with an independent inode, so a downstream in-place rewrite (or the
// caller's chmod) cannot mutate the shared CAS blob. Hard-linking is deliberately not used:
// it would alias the blob inode and silently poison the cache on the next in-place write.
func replayBlob(blob, dst string) error {
	if err := reflink.Clone(blob, dst); err == nil {
		return nil
	}
	return copyFile(blob, dst)
}

// copyFile copies src to dst, creating parent directories as needed.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	return errors.Join(copyErr, out.Close())
}

// copyToFile copies src into an already-open dst file.
func copyToFile(src string, dst *os.File) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	_, err = io.Copy(dst, in)
	return err
}
