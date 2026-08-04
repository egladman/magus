package cache

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egladman/magus/internal/json"
)

// parseSizeMB reads MAGUS_CACHE_SIZE_MB and returns the value as an int.
// Returns 0 (disabled) when the variable is unset, zero, or unparseable.
func parseSizeMB() int {
	v := strings.TrimSpace(os.Getenv("MAGUS_CACHE_SIZE_MB"))
	if v == "" {
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return int(n)
}

type manifestEntry struct {
	manifestPath string
	createdAt    time.Time
	blobs        []string
	hash         string // the cache key, so an eviction can reclaim outputs/<hash> too
}

// evictLRU removes oldest manifests (by CreatedAt) until disk usage is at or
// below limit bytes. evictMu prevents concurrent goroutines from double-counting
// freed bytes and over-evicting.
func (c *Cache) evictOldest(ctx context.Context, limit int64) {
	if limit <= 0 {
		return
	}
	c.evictMu.Lock()
	defer c.evictMu.Unlock()
	total, entries := c.scanManifests()
	if total <= limit {
		return
	}
	slices.SortFunc(entries, func(a, b manifestEntry) int {
		return a.createdAt.Compare(b.createdAt)
	})

	// Blob refcount: credit a blob's bytes only when the last manifest referencing it is evicted.
	blobRefs := make(map[string]int)
	for _, e := range entries {
		for _, b := range e.blobs {
			blobRefs[b]++
		}
	}

	// Reclaim ORPHAN output dirs first: a failing run persists its output but is never
	// snapshotted, so it has no manifest and the loop below can never reach it. Those
	// bytes still count toward the cap, so without this pass a workspace with failures
	// would evict every manifest chasing a floor it cannot reach. Oldest first, and
	// only while over the limit - recent failures are the ones worth keeping.
	total -= c.evictOrphanOutputs(ctx, entries, total, limit)

	for _, e := range entries {
		if total <= limit {
			break
		}
		if ctx.Err() != nil {
			return
		}
		info, statErr := os.Stat(e.manifestPath)
		if statErr != nil {
			continue
		}
		if os.Remove(e.manifestPath) == nil {
			total -= info.Size()
			total -= c.removeOutputsForKey(e.hash)
		}
		for _, blob := range e.blobs {
			if blob == "" {
				continue
			}
			blobRefs[blob]--
			if blobRefs[blob] > 0 {
				continue
			}
			// Use the authoritative blobPath so the shard layout (incl. the <2-char
			// fallback) can't diverge from the writer in manifest.go.
			if info, err := os.Stat(c.blobPath(blob)); err == nil {
				total -= info.Size()
			}
		}
	}
	_ = c.gcBlobs(ctx)
}

// diskSizeTTL bounds how often DiskBytes re-walks the cache tree, so a status stream
// polling every few seconds does not stat the whole cache each frame.
const diskSizeTTL = 15 * time.Second

// DiskBytes returns the approximate on-disk size of the cache: the summed file sizes of
// cas/ (the blob store) and manifests/ (the index), stat-only (no file reads, unlike
// scanManifests). The result is memoized for diskSizeTTL so it is cheap to poll for a
// live dashboard. Returns 0 for a dirless cache.
func (c *Cache) DiskBytes() int64 {
	if c == nil || c.dir == "" {
		return 0
	}
	c.diskMu.Lock()
	defer c.diskMu.Unlock()
	if !c.diskAt.IsZero() && time.Since(c.diskAt) < diskSizeTTL {
		return c.diskBytes
	}
	var total int64
	for _, sub := range []string{"cas", "manifests"} {
		_ = filepath.WalkDir(filepath.Join(c.dir, sub), func(_ string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if info, e := d.Info(); e == nil {
				total += info.Size()
			}
			return nil
		})
	}
	c.diskBytes = total
	c.diskAt = time.Now()
	return total
}

// scanManifests returns the total byte count of cas/ + manifests/ and all parsed manifest entries.
func (c *Cache) scanManifests() (int64, []manifestEntry) {
	var total int64
	var entries []manifestEntry

	casDir := filepath.Join(c.dir, "cas")
	_ = filepath.WalkDir(casDir, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if info, e := d.Info(); e == nil {
			total += info.Size()
		}
		return nil
	})

	// Stored outputs count toward the cap too. They were exempt, which made the size
	// limit a lie: a workspace whose targets print a lot grew without bound while
	// eviction dutifully trimmed only blobs and manifests. Each key dir is charged to
	// its entry below, so evicting an entry reclaims its outputs with it.
	total += c.outputsBytes()

	manifestsDir := filepath.Join(c.dir, "manifests")
	_ = filepath.WalkDir(manifestsDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(p, ".json") {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return nil //nolint:nilerr // skip unreadable entries; abort would leave GC incomplete
		}
		total += info.Size()
		data, e := os.ReadFile(p) // p is always under c.dir; symlink escapes are not a concern for a local cache
		if e != nil {
			return nil //nolint:nilerr // skip unreadable entries; abort would leave GC incomplete
		}
		var m Manifest
		if json.Unmarshal(data, &m) != nil {
			return nil //nolint:nilerr // skip corrupt entries; abort would leave GC incomplete
		}
		blobs := make([]string, 0, len(m.Outputs))
		for _, out := range m.Outputs {
			if out.Blob != "" {
				blobs = append(blobs, out.Blob)
			}
		}
		entries = append(entries, manifestEntry{
			manifestPath: p,
			createdAt:    m.CreatedAt,
			blobs:        blobs,
			hash:         strings.TrimSuffix(filepath.Base(p), ".json"),
		})
		return nil
	})
	return total, entries
}

// evictOrphanOutputs removes output dirs no manifest claims, oldest first, until the
// cap is met or none are left, and reports the bytes freed. An orphan is the residue
// of a run that stored output but no entry - overwhelmingly a FAILURE, since a failing
// run is never snapshotted. They are the one part of the store nothing else collects.
func (c *Cache) evictOrphanOutputs(ctx context.Context, entries []manifestEntry, total, limit int64) int64 {
	if total <= limit {
		return 0
	}
	claimed := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		claimed[e.hash] = struct{}{}
	}
	dirs, err := os.ReadDir(c.outputsDir())
	if err != nil {
		return 0
	}
	type orphan struct {
		hash string
		mod  time.Time
	}
	var orphans []orphan
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		if _, ok := claimed[d.Name()]; ok {
			continue
		}
		info, err := d.Info()
		if err != nil {
			continue
		}
		orphans = append(orphans, orphan{hash: d.Name(), mod: info.ModTime()})
	}
	slices.SortFunc(orphans, func(a, b orphan) int { return a.mod.Compare(b.mod) })
	var freed int64
	for _, o := range orphans {
		if total-freed <= limit {
			break
		}
		if ctx.Err() != nil {
			break
		}
		freed += c.removeOutputsForKey(o.hash)
	}
	return freed
}

// outputsDir is the per-cache-key output store: <cacheDir>/outputs.
func (c *Cache) outputsDir() string { return filepath.Join(c.dir, "outputs") }

// outputsBytes sums the stored outputs' on-disk size, stat-only.
func (c *Cache) outputsBytes() int64 {
	var total int64
	_ = filepath.WalkDir(c.outputsDir(), func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if info, e := d.Info(); e == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// outputsSizeForKey reports the on-disk size of one cache key's stored outputs,
// stat-only. Prune uses it to tally a dry run without deleting anything.
func (c *Cache) outputsSizeForKey(hash string) int64 {
	if hash == "" {
		return 0
	}
	var total int64
	_ = filepath.WalkDir(filepath.Join(c.outputsDir(), hash), func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if info, e := d.Info(); e == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// removeOutputsForKey deletes a cache key's whole output directory and reports the
// bytes freed. Called when the entry that produced those outputs is evicted or
// pruned: the ref is derived from the key, so once the entry is gone the outputs
// behind it are unreachable history, not a resource anyone can still address.
func (c *Cache) removeOutputsForKey(hash string) int64 {
	freed := c.outputsSizeForKey(hash)
	if hash == "" || os.RemoveAll(filepath.Join(c.outputsDir(), hash)) != nil {
		return 0
	}
	return freed
}
