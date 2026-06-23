package magus

import (
	"context"
	"io"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/types"
)

// LogScope emits a scope header through the cache logger. No-op on Inspect workspaces.
func (m *Magus) LogScope(label, source string) {
	if m.cache == nil {
		return
	}
	m.cache.LogScope(label, source)
}

// PruneCache removes entries older than cutoff and GC-collects orphaned blobs.
func (m *Magus) PruneCache(ctx context.Context, cutoff time.Time, dryRun bool) (removed int, freed int64, err error) {
	if m.cache == nil {
		return 0, 0, types.ErrNoCache
	}
	return m.cache.Prune(ctx, cutoff, dryRun)
}

// PruneRemoteCache evicts entries from the configured remote cache backend per a
// retention policy (age and/or newest-N). Errors when no remote backend is wired, the
// backend can't prune, or it's inactive here. Scalar args keep this public facade free
// of the internal cache.RetentionPolicy type.
func (m *Magus) PruneRemoteCache(ctx context.Context, olderThan time.Duration, keepLast int, dryRun bool) error {
	if m.cache == nil {
		return types.ErrNoCache
	}
	return m.cache.PruneRemote(ctx, cache.RetentionPolicy{OlderThan: olderThan, KeepLast: keepLast, DryRun: dryRun})
}

// ExportCache writes the entire cache to w as a gzip-compressed tar archive.
// Returns [types.ErrNoCache] on Inspect workspaces.
func (m *Magus) ExportCache(ctx context.Context, w io.Writer) error {
	if m.cache == nil {
		return types.ErrNoCache
	}
	return m.cache.Export(ctx, w)
}

// ImportCache extracts a gzip-compressed tar archive produced by [Magus.ExportCache].
// Returns [types.ErrNoCache] on Inspect workspaces.
func (m *Magus) ImportCache(ctx context.Context, r io.Reader) error {
	if m.cache == nil {
		return types.ErrNoCache
	}
	return m.cache.Import(ctx, r)
}

// TailLog returns the log-file path of the most recent cache entry for projectPath,
// optionally restricted to target. Wraps fs.ErrNotExist when not found; [types.ErrNoCache] on Inspect.
func (m *Magus) TailLog(projectPath, target string) (logPath string, err error) {
	if m.cache == nil {
		return "", types.ErrNoCache
	}
	if target != "" {
		_, logPath, err = m.cache.LastEntryForTarget(projectPath, target)
		return logPath, err
	}
	_, logPath, err = m.cache.LastEntry(projectPath)
	return logPath, err
}
