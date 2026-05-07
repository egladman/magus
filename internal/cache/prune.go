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
		// Tally associated blobs.
		for _, blob := range e.blobs {
			if len(blob) < 2 {
				continue
			}
			bp := filepath.Join(casDir, blob[:2], blob)
			if info, statErr := os.Stat(bp); statErr == nil {
				freed += info.Size()
			}
		}
		if !dryRun {
			_ = os.Remove(e.manifestPath)
		}
		n++
	}

	// GC any blobs now unreferenced (no-op in dry-run mode).
	if !dryRun && n > 0 {
		err = c.gcBlobs(ctx)
	}
	return n, freed, err
}
