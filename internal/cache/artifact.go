package cache

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/cache/reflink"
	"github.com/egladman/magus/internal/codec"
	"time"
)

// ArtifactVersion is one cached version of a declared output: the record a run
// snapshotted, plus which run it was.
//
// Output is EMBEDDED rather than restated. An earlier version copied four of its
// five fields and dropped Symlink, which was a live bug: a symlink record carries no
// Blob, so every symlink version hashed alike, collapsed to one row, and resolved to
// the cas/ directory itself.
type ArtifactVersion struct {
	Output    OutputRecord
	Target    string    // the target whose run produced it
	CreatedAt time.Time // when that entry was written
	EntryHash string    // the cache key; Cache.LatestRef maps it to an output ref
}

// ShortBlob abbreviates the content hash for display. Empty for a symlink record,
// which has no content of its own.
func (v ArtifactVersion) ShortBlob() string {
	if len(v.Output.Blob) > 12 {
		return v.Output.Blob[:12]
	}
	return v.Output.Blob
}

// ErrArtifactMissing reports that a version's bytes are not in the store.
//
// Named for the observation, not the cause: eviction is the usual reason, but a
// hand-cleared store or an entry that never stored its blob reach here too. Callers
// must not treat it as "no differences" - an empty diff reads as "unchanged", which
// is the most misleading answer available.
var ErrArtifactMissing = errors.New("cache: artifact content not in store")

// ArtifactHistory returns every cached version of wsPath, newest first.
//
// wsPath is WORKSPACE-relative, matching how snapshot recorded it and what
// TargetArtifact.Path carries; it is not relative to projectPath.
//
// Consecutive versions with identical content collapse to one, keeping the earliest
// of each run: a target that ran twenty times producing the same bytes changed the
// artifact once, and the question is when content first appeared.
func (c *Cache) ArtifactHistory(ctx context.Context, projectPath, wsPath string) ([]ArtifactVersion, error) {
	manifests, err := c.projectManifests(ctx, projectPath)
	if err != nil {
		return nil, err
	}
	want := filepath.ToSlash(wsPath)

	var out []ArtifactVersion
	for _, entry := range manifests {
		for _, rec := range entry.man.Outputs {
			if filepath.ToSlash(rec.Path) != want {
				continue
			}
			out = append(out, ArtifactVersion{
				Output:    rec,
				Target:    entry.man.Target,
				CreatedAt: entry.man.CreatedAt,
				EntryHash: entry.hash,
			})
		}
	}
	slices.SortStableFunc(out, func(a, b ArtifactVersion) int { return b.CreatedAt.Compare(a.CreatedAt) })

	// Identity is (blob, symlink target): a symlink record has no blob, so comparing
	// blobs alone would treat two links pointing at different files as one version.
	same := func(a, b ArtifactVersion) bool {
		return a.Output.Blob == b.Output.Blob && a.Output.Symlink == b.Output.Symlink
	}
	deduped := make([]ArtifactVersion, 0, len(out))
	for i, v := range out {
		if i > 0 && same(out[i-1], v) {
			deduped[len(deduped)-1] = v
			continue
		}
		deduped = append(deduped, v)
	}
	return deduped, nil
}

// MaterializeArtifact writes v to dst, creating parent directories.
//
// Content is cloned from the store rather than copied where the filesystem supports
// reflink (APFS, btrfs, XFS), so comparing a large artifact costs almost nothing.
func (c *Cache) MaterializeArtifact(ctx context.Context, v ArtifactVersion, dst string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	// A symlink record stores its target, not content: recreate the link.
	if v.Output.Symlink != "" {
		_ = os.Remove(dst)
		return os.Symlink(v.Output.Symlink, dst)
	}
	if v.Output.Blob == "" {
		return fmt.Errorf("%w: %s recorded no content", ErrArtifactMissing, v.Output.Path)
	}

	src := c.blobPath(v.Output.Blob)
	if fi, err := os.Stat(src); err != nil || fi.IsDir() {
		return fmt.Errorf("%w: %s (%s), produced by %s at %s; re-run that target with --no-cache to regenerate it",
			ErrArtifactMissing, v.Output.Path, v.ShortBlob(), v.Target, v.CreatedAt.UTC().Format(time.RFC3339))
	}
	mode := os.FileMode(v.Output.Mode).Perm()
	if err := reflink.Clone(src, dst); err == nil {
		return os.Chmod(dst, mode)
	}
	// Reflink is an optimization; any failure falls back to a copy.
	return copyBlob(ctx, src, dst, mode)
}

// copyBlob writes src to dst via a temp file beside it, so a failed read cannot
// leave a truncated file where a valid artifact was and a symlink at dst is replaced
// rather than written through.
func copyBlob(ctx context.Context, src, dst string, mode os.FileMode) error {
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
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(tmpName, dst)
}

// storedManifest pairs a decoded manifest with its cache key. (eviction.go's
// manifestEntry holds paths and blob refs for the LRU scan; this carries content.)
type storedManifest struct {
	man  Manifest
	hash string
}

// projectManifests decodes every manifest for projectPath.
//
// A manifest that cannot be read or decoded is skipped rather than failing the whole
// read: this is a cache, and one corrupt entry should not make an artifact's history
// unavailable. The cost is that an I/O fault looks like a shorter history.
func (c *Cache) projectManifests(ctx context.Context, projectPath string) ([]storedManifest, error) {
	dir := filepath.Join(c.dir, "manifests", flattenPath(projectPath))
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("cache: no entries for %q: %w", projectPath, fs.ErrNotExist)
	}
	if err != nil {
		return nil, err
	}
	out := make([]storedManifest, 0, len(entries))
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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
