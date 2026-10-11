package cache

import (
	"context"
	"log/slog"
	"os"
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

// treeMoved reports whether the tree no longer reads as m recorded it, through a stamp
// or a stamped output; either makes m unfit to replay, so its tier answers a miss.
func (c *Cache) treeMoved(ctx context.Context, s *Step, m *Manifest) bool {
	moved := c.movedStamps(ctx, s.WorkspaceRoot, s.Stamps, m.Stamps)
	moved = append(moved, c.movedOutputs(ctx, s, m)...)
	if len(moved) == 0 {
		return false
	}
	slog.DebugContext(ctx, "cache.stamp", slog.String("project", s.ProjectPath),
		slog.String("target", s.Target), slog.Any("moved", moved))
	return true
}

// movedOutputs returns the outputs that no longer read as m recorded them, for a step
// with StampOutputs (nil for any other): a recorded file now different or gone, and a
// file the outputs match that m never recorded. Through the mtime store, so a tree
// that still matches costs a stat per output.
func (c *Cache) movedOutputs(ctx context.Context, s *Step, m *Manifest) []string {
	if !s.StampOutputs {
		return nil
	}
	scope := s.withoutForeignOutputs()
	matches, err := expandOutputGlobs(scope.Outputs, s.WorkspaceRoot, s.NestedDirs)
	if err != nil {
		return []string{err.Error()}
	}
	recorded := map[string]OutputRecord{}
	for _, rec := range ownedOutputs(m, scope).Outputs {
		recorded[rec.Path] = rec
	}
	c.mtimes.load(ctx)
	var moved []string
	for _, f := range matches {
		rec, ok := recorded[f.rel]
		delete(recorded, f.rel)
		if !ok || !c.outputReads(f.abs, rec) {
			moved = append(moved, f.rel)
		}
	}
	for rel := range recorded {
		moved = append(moved, rel)
	}
	return moved
}

// outputReads reports whether the file at abs is what rec records.
func (c *Cache) outputReads(abs string, rec OutputRecord) bool {
	if rec.Symlink != "" {
		target, err := os.Readlink(abs)
		return err == nil && target == rec.Symlink
	}
	d, err := c.hashFileWithMtime(abs)
	return err == nil && d == rec.Blob
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
