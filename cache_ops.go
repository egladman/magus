package magus

import (
	"context"
	"io"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/types"
)

// LogScope emits a scope header through the cache logger. No-op on Inspect workspaces.
func (m *Magus) LogScope(label, source string) {
	if m.cache == nil {
		return
	}
	m.cache.LogScope(label, source)
}

// LogCharms emits the active-charm header through the cache logger. No-op on Inspect
// workspaces.
func (m *Magus) LogCharms(charms string) {
	if m.cache == nil {
		return
	}
	m.cache.LogCharms(charms)
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

// CacheStats returns this workspace's live cache counters (hits/misses/errors) accumulated
// since the cache was opened. In daemon mode the cache is long-lived, so these grow across
// adopted runs - the source for the /dashboard cache-activity panel. Zero value when no cache
// is attached (an Inspect workspace).
func (m *Magus) CacheStats() cache.Stats {
	if m.cache == nil {
		return cache.Stats{}
	}
	return m.cache.Stats()
}

// CacheDiskBytes returns the approximate on-disk size of this workspace's cache in bytes
// (memoized; cheap to poll). Zero when no cache is attached.
func (m *Magus) CacheDiskBytes() int64 {
	if m.cache == nil {
		return 0
	}
	return m.cache.DiskBytes()
}

// MetricsSnapshot returns this workspace's current metrics as standard OTLP protobuf (an
// ExportMetricsServiceRequest), or (nil, nil) when metrics collection was not enabled at Open
// (the CLI default). The daemon opens workspaces with [WithMetricsCollection] and relays this
// to the /dashboard. Reuses magus's existing OTel instruments; no bespoke metrics contract.
func (m *Magus) MetricsSnapshot(ctx context.Context) ([]byte, error) {
	if m.tel == nil {
		return nil, nil
	}
	return m.tel.Snapshot(ctx)
}

// CacheDir returns the resolved workspace cache directory - the same location the
// journal run logs and per-ref output store live under. Callers that persist their own
// sidecar stores (e.g. the MCP audit log) hang them off this so everything shares one
// cache root and one retention regime.
func (m *Magus) CacheDir() string {
	return resolveCacheDir(m.ws.Root, m.cfg)
}

// OutputByRef resolves a target-output reference id (or a unique prefix, git-style)
// to its reconstructed raw text and metadata. It reads the output store directly from
// the resolved cache dir, so it works on Inspect workspaces too (no live cache needed)
// - the retrieval path for `magus query ref...` (print). Returns fs.ErrNotExist when no
// ref matches, or *cache.AmbiguousRefError when a prefix matches several.
func (m *Magus) OutputByRef(ref string) ([]byte, cache.OutputMeta, error) {
	return cache.LookupOutput(resolveCacheDir(m.ws.Root, m.cfg), ref)
}

// OutputEventsByRef resolves a ref (or unique prefix) to the execution's domain
// events plus metadata - the structured form the handler layer maps onto the wire
// proto for `magus query ref... --open`. Same resolution semantics as OutputByRef.
func (m *Magus) OutputEventsByRef(ref string) ([]journal.Event, cache.OutputMeta, error) {
	return cache.LookupEvents(resolveCacheDir(m.ws.Root, m.cfg), ref)
}

// InvocationByID resolves an invocation id (OutputMeta.Inv) to its run header - the command
// lineage (verb/args/trigger), timing, and outcome - read from the union run log. It is the
// lineage source for `magus query <ref> --meta` and the viewer. Returns fs.ErrNotExist when
// the run log has aged out.
func (m *Magus) InvocationByID(inv string) (journal.Invocation, error) {
	return cache.LookupInvocation(resolveCacheDir(m.ws.Root, m.cfg), inv)
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
