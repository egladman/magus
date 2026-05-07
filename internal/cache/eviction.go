package cache

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egladman/magus/internal/codec"
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
}

// evictLRU removes oldest manifests (by CreatedAt) until disk usage is at or
// below limit bytes. evictMu prevents concurrent goroutines from double-counting
// freed bytes and over-evicting.
func (c *Cache) evictLRU(ctx context.Context, limit int64) {
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

	casDir := filepath.Join(c.dir, "cas")
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
		}
		for _, blob := range e.blobs {
			if len(blob) < 2 {
				continue
			}
			blobRefs[blob]--
			if blobRefs[blob] > 0 {
				continue
			}
			bp := filepath.Join(casDir, blob[:2], blob)
			if info, err := os.Stat(bp); err == nil {
				total -= info.Size()
			}
		}
	}
	_ = c.gcBlobs(ctx)
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
		data, e := os.ReadFile(p) //nolint:gosec // p is always under c.dir; symlink escapes are not a concern for a local cache
		if e != nil {
			return nil //nolint:nilerr // skip unreadable entries; abort would leave GC incomplete
		}
		var m Manifest
		if codec.Unmarshal(data, &m) != nil {
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
		})
		return nil
	})
	return total, entries
}
