package magus

import (
	"context"
	"io"
	"time"

	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/observability/otlp"
	"github.com/egladman/magus/types"
)

// LogScope emits a scope header through the cache logger. No-op on Inspect workspaces.
func (m *Magus) LogScope(ctx context.Context, label, source string) {
	if m.cache == nil {
		return
	}
	m.cache.LogScope(ctx, label, source)
}

// LogCharms emits the active-charm header through the cache logger. No-op on Inspect
// workspaces.
func (m *Magus) LogCharms(ctx context.Context, charms string) {
	if m.cache == nil {
		return
	}
	m.cache.LogCharms(ctx, charms)
}

// LogCache emits the cache-tier header through the cache logger. No-op on Inspect
// workspaces, which have no cache to describe.
func (m *Magus) LogCache(ctx context.Context) {
	if m.cache == nil {
		return
	}
	m.cache.LogCache(ctx)
}

// LogBase emits the affected-set base header through the cache logger. No-op on Inspect
// workspaces.
func (m *Magus) LogBase(ctx context.Context, base, vcs string) {
	if m.cache == nil {
		return
	}
	m.cache.LogBase(ctx, base, vcs)
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

// CacheStats is this workspace's live cache counters (hits/misses/errors), a caller-facing
// projection of [cache.Stats] that carries no type from an internal/ package.
type CacheStats struct {
	Hit   int
	Miss  int
	Error int
}

// CacheStats returns this workspace's live cache counters (hits/misses/errors) accumulated
// since the cache was opened. In daemon mode the cache is long-lived, so these grow across
// adopted runs - the source for the /dashboard cache-activity panel. Zero value when no cache
// is attached (an Inspect workspace).
func (m *Magus) CacheStats() CacheStats {
	if m.cache == nil {
		return CacheStats{}
	}
	s := m.cache.Stats()
	return CacheStats{Hit: s.Hit, Miss: s.Miss, Error: s.Error}
}

// CacheDiskBytes returns the approximate on-disk size of this workspace's cache in bytes
// (memoized; cheap to poll). Zero when no cache is attached.
func (m *Magus) CacheDiskBytes() int64 {
	if m.cache == nil {
		return 0
	}
	return m.cache.DiskBytes()
}

// SecretProvider returns the NAME of the secret-provider spell this workspace's magusfile
// selected, or "" when none is declared and the built-in environment provider applies.
//
// The name only. There is deliberately no accessor for the references a workspace can
// reach, let alone their values: a standing inventory of what a build can fetch is a map
// of what to go after, and magus does not store secrets in the first place - it reads them
// through a provider. Which provider is loaded is configuration a reader should be able to
// see; what it can reach is not magus's to publish.
func (m *Magus) SecretProvider() string { return m.resolver.ProviderName() }

// MetricsSnapshot returns this workspace's current metrics as standard OTLP protobuf (an
// ExportMetricsServiceRequest), or (nil, nil) when metrics collection was not enabled at Open
// (the CLI default). A workspace opened with [WithMetricsCollection] can export this to any
// OTLP-compatible collector. Reuses magus's existing OTel instruments; no bespoke metrics
// contract. Its only caller today is the test suite.
func (m *Magus) MetricsSnapshot(ctx context.Context) ([]byte, error) {
	if m.tel == nil {
		return nil, nil
	}
	return m.tel.Snapshot(ctx)
}

// MetricsCollector wraps [otlp.Collector] for the SDK boundary: the same one in-process
// metricdata read, exposed without naming an internal/ type.
type MetricsCollector struct{ c *otlp.Collector }

// Collect gathers the current metricdata from the underlying reader. See
// [otlp.Collector.Collect].
func (c *MetricsCollector) Collect(ctx context.Context) (metricdata.ResourceMetrics, error) {
	return c.c.Collect(ctx)
}

// MetricsCollector returns a narrow accessor over this workspace's in-process metrics
// ManualReader for the daemon's derived-dashboard aggregation, or (nil, false) when metrics
// collection was not enabled at Open (the CLI default). Unlike [Magus.MetricsSnapshot] (OTLP
// bytes for external export), this reads raw metricdata - histogram buckets and counters -
// with no exporter hop and without exposing the generated dashboard proto here.
func (m *Magus) MetricsCollector() (*MetricsCollector, bool) {
	if m.tel == nil {
		return nil, false
	}
	c, ok := otlp.CollectorFrom(m.tel)
	if !ok {
		return nil, false
	}
	return &MetricsCollector{c: c}, true
}

// CacheDir returns the resolved workspace cache directory - the same location the
// journal run logs and per-ref output store live under. Callers that persist their own
// sidecar stores (e.g. the MCP audit log) hang them off this so everything shares one
// cache root and one retention regime.
func (m *Magus) CacheDir() string {
	return resolveCacheDir(m.ws.Root, m.cfg)
}

// ResolveCacheDir returns the cache directory that an Open or Inspect workspace rooted at root
// would use without discovering projects or evaluating magusfiles. Sidecar writers that need the
// shared cache location before a command runs use this narrow path so instrumentation does not pay
// workspace-load cost merely to append an event.
func ResolveCacheDir(root string, opts ...Option) (string, error) {
	cfg, err := loadConfig(root, opts...)
	if err != nil {
		return "", err
	}
	return resolveCacheDir(root, cfg), nil
}

// OutputDescriptor is a stored target execution's identity and outcome - the caller-facing
// projection of [cache.OutputDescriptor], the metadata behind a target-output ref.
// Field tags match [cache.OutputDescriptor]'s exactly, so embedding this in a CLI JSON
// record (`magus query output <ref> -o json`) reproduces the same wire shape.
type OutputDescriptor struct {
	Ref         string `json:"ref"`
	Project     string `json:"project"`
	Target      string `json:"target,omitempty"`
	Inv         string `json:"inv,omitempty"` // invocation id of the run that produced this output
	Failed      bool   `json:"failed"`
	ErrMsg      string `json:"error,omitempty"` // failure message; empty on success
	TimestampMs int64  `json:"timestamp_ms"`    // unix milliseconds, matching DurationMs' unit
	DurationMs  int64  `json:"duration_ms"`

	Key          string `json:"key,omitempty"`         // full cache key hash (64 hex)
	KeyVersion   int    `json:"key_version,omitempty"` // hashStep KeyVersion that produced Key
	Attempt      string `json:"attempt,omitempty"`     // execution-unique id; the file stem
	MagusVersion string `json:"magus_version,omitempty"`

	Revision string `json:"revision,omitempty"` // full VCS revision hash inputs were read at; "" when unknown
	Dirty    bool   `json:"dirty,omitempty"`    // working tree had uncommitted changes at capture time

	Spell     string   `json:"spell,omitempty"`      // spell::op filter that selected the definition
	ExtraArgs []string `json:"extra_args,omitempty"` // trailing args forwarded after --
	VCSName   string   `json:"vcs,omitempty"`        // provider Revision came from: git, hg, jj
}

func newOutputDescriptor(d cache.OutputDescriptor) OutputDescriptor {
	return OutputDescriptor{
		Ref: d.Ref, Project: d.Project, Target: d.Target, Inv: d.Inv,
		Failed: d.Failed, ErrMsg: d.ErrMsg, TimestampMs: d.TimestampMs, DurationMs: d.DurationMs,
		Key: d.Key, KeyVersion: d.KeyVersion, Attempt: d.Attempt, MagusVersion: d.MagusVersion,
		Revision: d.Revision, Dirty: d.Dirty,
		Spell: d.Spell, ExtraArgs: d.ExtraArgs, VCSName: d.VCSName,
	}
}

// OutputByRef resolves a target-output reference id (or a unique prefix, git-style)
// to its reconstructed raw text and metadata. It reads the output store directly from
// the resolved cache dir, so it works on Inspect workspaces too (no live cache needed)
// - the retrieval path for `magus query output <ref>` (print). Returns fs.ErrNotExist when no
// ref matches, or *cache.AmbiguousRefError when a prefix matches several.
func (m *Magus) OutputByRef(ref string) ([]byte, OutputDescriptor, error) {
	data, d, err := cache.NewOutputStore(resolveCacheDir(m.ws.Root, m.cfg)).ByRef(ref)
	return data, newOutputDescriptor(d), err
}

// OutputAttempts lists every stored execution of the step ref names, newest first - the
// keep-last-K history behind one portable ref, for `magus query output <ref> --attempts`.
// Like OutputByRef it reads the store straight off the resolved cache dir, so Inspect
// workspaces work too. Returns fs.ErrNotExist when no ref matches, or
// *cache.AmbiguousRefError when a prefix matches several.
func (m *Magus) OutputAttempts(ref string) ([]OutputDescriptor, error) {
	list, err := cache.NewOutputStore(resolveCacheDir(m.ws.Root, m.cfg)).Attempts(ref)
	if err != nil {
		return nil, err
	}
	out := make([]OutputDescriptor, len(list))
	for i, d := range list {
		out[i] = newOutputDescriptor(d)
	}
	return out, nil
}

// PublishOutput uploads the run behind ref to the configured remote cache as a signed
// OUTPUT BUNDLE, and returns the ref a teammate can then resolve. A passing run's
// output already travels with its cache artifact; this is what makes a FAILING run -
// never cached, never pushed - shareable, and it is always an explicit act because
// captured output can contain anything the target printed. The bundle carries no
// manifest and no blobs, so it can never be replayed as a cache hit. Requires a
// remote backend and a signing key; [types.ErrNoCache] on an Inspect workspace.
func (m *Magus) PublishOutput(ctx context.Context, ref string) (string, error) {
	if m.cache == nil {
		return "", types.ErrNoCache
	}
	return m.cache.PublishOutput(ctx, ref)
}

// OutputByRefRemote resolves a ref to its captured bytes and descriptor, falling back
// to the remote published-output namespace when the ref is unknown locally - so an
// inspect line pasted from CI or a teammate resolves even on a machine that never ran
// the target. Requires a live cache (the remote backend and trust set live there); on
// an Inspect workspace it degrades to the local-only path.
func (m *Magus) OutputByRefRemote(ctx context.Context, ref string) ([]byte, OutputDescriptor, error) {
	if m.cache == nil {
		return m.OutputByRef(ref)
	}
	data, d, err := m.cache.OutputByRef(ctx, ref)
	return data, newOutputDescriptor(d), err
}

// OutputDescriptorByRef resolves a ref to just its stored descriptor, without reading
// the output blob. The metadata views (`query output <ref> --meta`, `describe target
// --cache --against <ref>`) want the identity, not the bytes, and a captured log can
// be large.
func (m *Magus) OutputDescriptorByRef(ref string) (OutputDescriptor, error) {
	d, err := cache.NewOutputStore(resolveCacheDir(m.ws.Root, m.cfg)).DescriptorByRef(ref)
	return newOutputDescriptor(d), err
}

// OutputKeyInputs returns the pre-hash key inputs stored behind ref - the deterministic
// label:value lines hashStep consumed to mint the step's cache key, secret-redacted at
// write. They are the explanation surface for `magus query output <ref> --meta`
// (component-class digests) and `describe target --cache --against <ref>` (the exact
// disagreeing line). Returns fs.ErrNotExist when the ref resolves but the run predates
// key-input persistence.
func (m *Magus) OutputKeyInputs(ref string) ([]string, error) {
	return cache.NewOutputStore(resolveCacheDir(m.ws.Root, m.cfg)).KeyInputsByRef(ref)
}

// InvocationByID resolves an invocation id (OutputDescriptor.Inv) to its run header - the command
// lineage (subcommand/args/trigger), timing, and outcome - read from the union run log. It is the
// lineage source for `magus query output <ref> --meta` and the viewer. Returns fs.ErrNotExist when
// the run log has aged out.
func (m *Magus) InvocationByID(inv string) (Invocation, error) {
	raw, err := cache.NewOutputStore(resolveCacheDir(m.ws.Root, m.cfg)).InvocationByID(inv)
	return newInvocation(raw), err
}

// InvocationEventsByID resolves an invocation id to its run header AND the events behind it.
// [Magus.InvocationByID] answers "what was this run"; this answers "what happened during it",
// which is what an audit of a run's credential reads needs. Returns fs.ErrNotExist when the run
// log has aged out.
func (m *Magus) InvocationEventsByID(inv string) (Invocation, []Event, error) {
	raw, events, err := cache.NewOutputStore(resolveCacheDir(m.ws.Root, m.cfg)).InvocationEventsByID(inv)
	return newInvocation(raw), newEvents(events), err
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

// ListArtifacts returns every cached version of the workspace-relative wsPath,
// newest first, with identical consecutive content collapsed.
//
// Returns types.ErrNoCache on an Inspect workspace: "no versions" and "no store to
// look in" are different answers.
func (m *Magus) ListArtifacts(ctx context.Context, projectPath, wsPath string) ([]cache.ArtifactVersion, error) {
	if m.cache == nil {
		return nil, types.ErrNoCache
	}
	return m.cache.ListArtifacts(ctx, projectPath, wsPath)
}

// GetArtifact writes a cached version to dst, cloning from the store when
// the filesystem supports reflink.
func (m *Magus) GetArtifact(ctx context.Context, v cache.ArtifactVersion, dst string) error {
	if m.cache == nil {
		return types.ErrNoCache
	}
	return m.cache.GetArtifact(ctx, v, dst)
}
