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
	"github.com/egladman/magus/internal/codec"
)

// snapshot records the project's declared outputs into the cache and writes the manifest.
// A project with no declared outputs records an empty manifest (correct cache hit on rerun).
func (c *Cache) snapshot(s Spec, hash string) ([]string, error) {
	root := s.WorkspaceRoot
	matches, err := expandOutputGlobs(s.Outputs, root)
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 && len(s.Outputs) > 0 {
		return nil, fmt.Errorf("snapshot: no files matched declared outputs (project %q)", s.ProjectPath)
	}
	manifest := &Manifest{
		ProjectPath: s.ProjectPath,
		Hash:        hash,
		Target:      s.Target,
		CreatedAt:   time.Now().UTC(),
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
	data, err := codec.MarshalIndent(manifest, "", "  ")
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

// replay restores a manifest's outputs: reflink → hard link → byte copy.
func (c *Cache) replay(ctx context.Context, m *Manifest, root string) ([]string, error) {
	var paths []string
	for _, rec := range m.Outputs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		dst := filepath.Join(root, filepath.FromSlash(rec.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, err
		}
		if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("replay %s: remove existing: %w", rec.Path, err)
		}
		if rec.Symlink != "" {
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
			_ = os.Chmod(dst, os.FileMode(rec.Mode&0o777)) // best-effort
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
