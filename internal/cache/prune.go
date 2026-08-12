package cache

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// Prune removes cache entries whose CreatedAt is before cutoff and then GCs
// orphaned blobs. Returns the count of entries removed and total bytes freed.
// When dryRun is true no files are deleted; counts are still returned.
func (c *Cache) Prune(ctx context.Context, cutoff time.Time, dryRun bool) (n int, freed int64, err error) {
	_, entries := c.scanManifests()
	casDir := filepath.Join(c.dir, "cas")

	// Blob refcount across ALL entries (pruned and surviving), matching evictLRU:
	// gcBlobs below only removes a blob once every manifest referencing it is
	// gone, so a blob a surviving manifest still points to is never actually
	// reclaimed and must not be counted as freed.
	blobRefs := make(map[string]int)
	for _, e := range entries {
		for _, b := range e.blobs {
			blobRefs[b]++
		}
	}

	for _, e := range entries {
		if ctx.Err() != nil {
			return n, freed, ctx.Err()
		}
		if !e.createdAt.Before(cutoff) {
			continue
		}
		// Tally manifest size.
		if info, statErr := os.Stat(e.manifestPath); statErr == nil {
			freed += info.Size()
		}
		// Tally associated blobs, crediting bytes only when this was the last
		// reference: gcBlobs will not remove a blob a surviving manifest still uses.
		for _, blob := range e.blobs {
			if len(blob) < 2 {
				continue
			}
			blobRefs[blob]--
			if blobRefs[blob] > 0 {
				continue
			}
			bp := filepath.Join(casDir, blob[:2], blob)
			if info, statErr := os.Stat(bp); statErr == nil {
				freed += info.Size()
			}
		}
		// The entry's stored outputs go with it: their refs derive from this key, so
		// once the entry is pruned nothing can address them.
		outBytes := c.outputsSizeForKey(e.hash)
		freed += outBytes
		if !dryRun {
			_ = os.Remove(e.manifestPath)
			c.removeOutputsForKey(e.hash)
		}
		n++
	}

	// GC any blobs now unreferenced (no-op in dry-run mode).
	if !dryRun && n > 0 {
		err = c.gcBlobs(ctx)
	}
	return n, freed, err
}
