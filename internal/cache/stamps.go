package cache

import (
	"context"
	"path/filepath"
)

// stampAbsent is the digest of a stamp that does not exist, so a deleted stamp and an
// unreadable one both differ from any recorded content digest.
const stampAbsent = "absent"

// StampPath is the file a step's stamp names: the stamp itself when absolute, and
// otherwise joined to root.
func StampPath(root, stamp string) string {
	if filepath.IsAbs(stamp) {
		return stamp
	}
	return filepath.Join(root, stamp)
}

// stampDigests reads each stamp's content digest. nil for no stamps.
//
// Through the mtime store, because a stamp is read on every lookup of its step and a
// symbol index runs to a hundred megabytes: an unchanged one costs a stat.
func (c *Cache) stampDigests(ctx context.Context, root string, stamps []string) map[string]string {
	if len(stamps) == 0 {
		return nil
	}
	c.mtimes.load(ctx)
	out := make(map[string]string, len(stamps))
	for _, s := range stamps {
		d, err := c.hashFileWithMtime(StampPath(root, s))
		if err != nil {
			d = stampAbsent
		}
		out[s] = d
	}
	return out
}

// movedStamps returns the stamps whose digest now differs from the recorded one. A
// stamp the entry never recorded counts as moved: the entry cannot vouch for it.
func (c *Cache) movedStamps(ctx context.Context, root string, stamps []string, recorded map[string]string) []string {
	var moved []string
	for s, d := range c.stampDigests(ctx, root, stamps) {
		if r, ok := recorded[s]; !ok || r != d || d == stampAbsent {
			moved = append(moved, s)
		}
	}
	return moved
}
