package cache

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/egladman/magus/internal/cache/reflink"
	"github.com/egladman/magus/internal/codec"
)

// ArtifactVersion is one cached version of a declared output: the bytes a target
// produced on some earlier run, still addressable because the store is
// content-addressed (cas/ blobs keyed by sha256, referenced by per-entry manifests).
//
// This is the fact that makes an artifact's history answerable at all, and it was
// already on disk before anything exposed it. Nothing here writes.
type ArtifactVersion struct {
	Path      string    // repo-relative, as the manifest recorded it
	Blob      string    // sha256 hex of the contents; the CAS key
	Size      int64     // bytes
	Mode      uint32    // permission bits
	Target    string    // the target whose run produced it
	CreatedAt time.Time // when that entry was written
	EntryHash string    // the cache key, so a caller can reach the run's log
}

// Short returns the abbreviated blob hash, for display next to a path.
func (v ArtifactVersion) Short() string {
	if len(v.Blob) > 12 {
		return v.Blob[:12]
	}
	return v.Blob
}

// ErrArtifactEvicted reports that a version's blob is no longer in the store.
//
// It is a distinct error because it is the one failure a caller must not paper
// over. The store evicts LRU blobs, so a version can be listed from a surviving
// manifest and still have no bytes behind it. Reporting that as "no differences"
// would be a lie in the most misleading direction available: the reader concludes
// the artifact is unchanged when the truth is that the comparison could not happen.
var ErrArtifactEvicted = errors.New("cache: artifact blob evicted")

// ArtifactHistory returns every cached version of the repo-relative path under
// projectPath, newest first.
//
// Consecutive entries holding identical bytes collapse to one. A target that ran
// four times and produced the same output each time changed the artifact once, and
// listing it four times would bury the change the reader is looking for. The
// surviving record is the OLDEST occurrence of that blob, because the question a
// history answers is "when did this content first appear", not "when was it most
// recently re-confirmed".
func (c *Cache) ArtifactHistory(projectPath, path string) ([]ArtifactVersion, error) {
	manifests, err := c.projectManifests(projectPath)
	if err != nil {
		return nil, err
	}
	want := filepath.ToSlash(path)

	var out []ArtifactVersion
	for _, entry := range manifests {
		for _, rec := range entry.man.Outputs {
			if filepath.ToSlash(rec.Path) != want {
				continue
			}
			out = append(out, ArtifactVersion{
				Path:      rec.Path,
				Blob:      rec.Blob,
				Size:      rec.Size,
				Mode:      rec.Mode,
				Target:    entry.man.Target,
				CreatedAt: entry.man.CreatedAt,
				EntryHash: entry.hash,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })

	// Walk newest-first and drop a version whose blob matches the one before it,
	// keeping the oldest of each run of identical content.
	deduped := make([]ArtifactVersion, 0, len(out))
	for i, v := range out {
		if i > 0 && out[i-1].Blob == v.Blob {
			deduped[len(deduped)-1] = v
			continue
		}
		deduped = append(deduped, v)
	}
	return deduped, nil
}

// MaterializeArtifact writes v's bytes to dst, creating parent directories.
//
// It clones from the CAS rather than copying when the filesystem supports it
// (APFS, btrfs, XFS), so materializing a large artifact to compare against is
// near-free. A filesystem without reflink falls back to a plain copy.
//
// The blob is checked for existence first so an evicted version reports
// [ErrArtifactEvicted] rather than a bare open failure the caller would have to
// interpret.
func (c *Cache) MaterializeArtifact(v ArtifactVersion, dst string) error {
	src := c.blobPath(v.Blob)
	if _, err := os.Stat(src); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: %s (%s) produced %s; re-run the target with --no-cache to regenerate it",
				ErrArtifactEvicted, v.Path, v.Short(), v.CreatedAt.Format(time.RFC3339))
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := reflink.Clone(src, dst); err == nil {
		return os.Chmod(dst, os.FileMode(v.Mode).Perm())
	}
	// Reflink is an optimization, not a requirement: any failure (unsupported
	// filesystem, cross-device) falls back to a copy rather than surfacing.
	return copyBlob(src, dst, os.FileMode(v.Mode).Perm())
}

// copyBlob writes src to dst atomically via a temp file beside it, for the same
// reasons the chain's export does: a partial read must not leave a truncated file
// where a valid artifact was, and dst may already exist as a symlink.
func copyBlob(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".magus-materialize-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.ReadFrom(in); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, dst)
}

// storedManifest pairs a decoded manifest with its cache key. (eviction.go has its
// own manifestEntry holding paths and blob refs for the LRU scan; this one carries
// the decoded content, which is what a history read needs.)
type storedManifest struct {
	man  Manifest
	hash string
}

// projectManifests decodes every manifest for projectPath. A manifest that fails
// to decode is skipped rather than failing the read: one corrupt entry should not
// make an artifact's whole history unavailable, and the store is a cache whose
// worst-case answer is "fewer versions than exist".
func (c *Cache) projectManifests(projectPath string) ([]storedManifest, error) {
	dir := filepath.Join(c.dir, "manifests", flattenPath(projectPath))
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("no cache entries for %q: %w", projectPath, fs.ErrNotExist)
	}
	if err != nil {
		return nil, err
	}
	out := make([]storedManifest, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var m Manifest
		if codec.Unmarshal(data, &m) != nil {
			continue
		}
		out = append(out, storedManifest{man: m, hash: strings.TrimSuffix(e.Name(), ".json")})
	}
	return out, nil
}
