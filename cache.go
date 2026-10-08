package magus

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/observability/otlp"
	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// CacheDescription returns the cache header's facts, for [Sink.EmitCache]: which tiers
// a run can reach ("local", or "<remote> + local") and whether it may write to them
// ("read-only" or "read+write"). Empty on an Inspect workspace, which has no cache.
func (m *Magus) CacheDescription() (tier, mode string) {
	if m.cache == nil {
		return "", ""
	}
	return m.cache.Description()
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
	return m.cache.PruneRemote(m.ContextWithSecrets(ctx), cache.RetentionPolicy{OlderThan: olderThan, KeepLast: keepLast, DryRun: dryRun})
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

// SpellCache is what saving or restoring one spell's declared caches did.
type SpellCache struct {
	Spell string
	// Key is the remote key stored, found stored, or restored; "" when a restore found
	// no verified bundle.
	Key string
	// Exact reports a restore whose lockfiles matched the workspace's.
	Exact bool
	// Present reports a save that found the day's bundle already stored.
	Present bool
	// Inactive reports a restore that found the remote backend inactive here.
	Inactive bool
	Files    int
	Bytes    int64
	// Skipped is the unused entries a save left out, or the files a restore found present.
	Skipped     int
	Transferred int64
	// Refused names each bundle that failed verification, with the reason.
	Refused []string
	// Dirs are the directories bundled or restored into, as ENV=path.
	Dirs []string
}

// SaveSpellCaches signs the caches every spell the workspace resolves declares, one
// bundle per spell, and stores them in the remote tier. Of a cache whose tool dates
// what it uses, only entries dated since the last restore are kept. It errors when no
// remote is wired or this run may not write it; a workspace whose spells declare no
// cache saves nothing.
func (m *Magus) SaveSpellCaches(ctx context.Context) ([]SpellCache, error) {
	return m.eachSpellCache(ctx, m.cache.SaveSpellCache)
}

// RestoreSpellCaches restores, for every spell the workspace resolves that declares
// caches, the newest verified bundle from the remote tier. Finding none is not an
// error; a bundle that fails verification is never restored and is named in Refused.
func (m *Magus) RestoreSpellCaches(ctx context.Context) ([]SpellCache, error) {
	return m.eachSpellCache(ctx, m.cache.RestoreSpellCache)
}

func (m *Magus) eachSpellCache(ctx context.Context, do func(context.Context, cache.SpellCacheKey, []cache.SpellCacheRoot) (cache.SpellCacheResult, error)) ([]SpellCache, error) {
	if m.cache == nil {
		return nil, types.ErrNoCache
	}
	declared, err := m.spellCaches(ctx)
	if err != nil {
		return nil, err
	}
	ctx = m.ContextWithSecrets(ctx)
	out := make([]SpellCache, 0, len(declared))
	for _, d := range declared {
		res, err := do(ctx, d.key, d.roots)
		out = append(out, SpellCache{
			Spell: d.key.Spell, Key: res.Key, Exact: res.Exact, Present: res.Present, Inactive: res.Inactive,
			Files: res.Files, Bytes: res.Bytes, Skipped: res.Skipped, Transferred: res.Transferred,
			Refused: res.Refused, Dirs: d.dirs,
		})
		if err != nil {
			return out, fmt.Errorf("magus: %s's caches: %w", d.key.Spell, err)
		}
	}
	return out, nil
}

type declaredCaches struct {
	key   cache.SpellCacheKey
	roots []cache.SpellCacheRoot
	dirs  []string
}

// spellCaches resolves, for each spell a project resolved that declares caches, the
// bundle key and where each cache lives as the sandbox locates it under this process's
// environment. The key is the host platform and the spell's tool versions, as they key
// its targets, and the lockfiles its manifests name in those projects.
func (m *Magus) spellCaches(ctx context.Context) ([]declaredCaches, error) {
	bySpell := map[string]*spells.Spell{}
	projects := map[string][]*types.Project{}
	for _, p := range m.All() {
		for _, s := range p.ResolvedSpells {
			if sb := s.Sandbox(); sb == nil || len(sb.Caches) == 0 {
				continue
			}
			bySpell[s.Name()] = s
			projects[s.Name()] = append(projects[s.Name()], p)
		}
	}
	home, _ := os.UserHomeDir()
	var out []declaredCaches
	for _, name := range slices.Sorted(maps.Keys(bySpell)) {
		s, sb := bySpell[name], bySpell[name].Sandbox()
		tools := []string{"platform:" + runtime.GOOS + "/" + runtime.GOARCH}
		byProject, err := m.toolVersionsByProject(ctx, projects[name])
		if err != nil {
			return nil, err
		}
		for _, lines := range byProject {
			for _, l := range lines {
				if strings.HasPrefix(l, name+":") {
					tools = append(tools, l)
				}
			}
		}
		locks, err := m.spellLocks(s, projects[name])
		if err != nil {
			return nil, err
		}
		located := sandbox.CacheDirs(*sb, os.Environ(), home)
		d := declaredCaches{key: cache.NewSpellCacheKey(name, tools, locks)}
		for _, c := range sb.Caches {
			dir := located[c.Env]
			if dir == "" {
				return nil, fmt.Errorf("magus: spell %s declares the cache %s, which locates no directory here", name, c.Env)
			}
			d.roots = append(d.roots, cache.SpellCacheRoot{Name: c.Env, Dir: dir, StampsUse: c.StampsUse, Skip: c.Skip})
			d.dirs = append(d.dirs, c.Env+"="+dir)
		}
		out = append(out, d)
	}
	return out, nil
}

// spellLocks reads the lockfiles s's manifests name in each of projects, keyed by
// workspace-relative path.
func (m *Magus) spellLocks(s *spells.Spell, projects []*types.Project) (map[string][]byte, error) {
	locks := map[string][]byte{}
	for _, p := range projects {
		for _, man := range s.Manifests() {
			if _, err := os.Stat(filepath.Join(p.Dir, man.Value)); err != nil {
				continue
			}
			for _, lock := range man.LockCandidates {
				path := filepath.Join(p.Dir, lock)
				data, err := os.ReadFile(path)
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				if err != nil {
					return nil, fmt.Errorf("magus: %w", err)
				}
				rel, err := filepath.Rel(m.ws.Root, path)
				if err != nil {
					return nil, err
				}
				locks[filepath.ToSlash(rel)] = data
			}
		}
	}
	return locks, nil
}

// CacheStats is this workspace's live cache counters (hits/misses/errors), a caller-facing
// projection of [cache.Stats] that carries no type from an internal/ package.
type CacheStats struct {
	Hit   int
	Miss  int
	Error int
	// SavedMs is the summed recorded duration of the runs those hits replayed: work the cache
	// avoided, measured per entry rather than averaged. Understates when an entry predates the
	// recorded duration; never overstates.
	SavedMs int64
}

// CacheStats returns this workspace's live cache counters (hits/misses/errors) accumulated
// since the cache was opened. In server mode the cache is long-lived, so these grow across
// adopted runs: the source for the /dashboard cache-activity panel. Zero value when no cache
// is attached (an Inspect workspace).
func (m *Magus) CacheStats() CacheStats {
	if m.cache == nil {
		return CacheStats{}
	}
	s := m.cache.Stats()
	return CacheStats{Hit: s.Hit, Miss: s.Miss, Error: s.Error, SavedMs: s.SavedMs}
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
// of what to go after, and magus does not store secrets in the first place: it reads them
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
// ManualReader for the server's derived-dashboard aggregation, or (nil, false) when metrics
// collection was not enabled at Open (the CLI default). Unlike [Magus.MetricsSnapshot] (OTLP
// bytes for external export), this reads raw metricdata (histogram buckets and counters)
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

// CacheDir returns the resolved workspace cache directory: the same location the
// journal run logs and per-ref output store live under. Callers that persist their own
// adjacent stores (e.g. the MCP audit log) hang them off this so everything shares one
// cache root and one retention regime.
func (m *Magus) CacheDir() string {
	return resolveCacheDir(m.ws.Root, m.cfg)
}

// ResolveCacheDir returns the cache directory that an Open or Inspect workspace rooted at root
// would use without discovering projects or evaluating magusfiles. Auxiliary writers that need the
// shared cache location before a command runs use this narrow path so instrumentation does not pay
// workspace-load cost merely to append an event.
func ResolveCacheDir(root string, opts ...Option) (string, error) {
	cfg, err := loadConfig(root, opts...)
	if err != nil {
		return "", err
	}
	return resolveCacheDir(root, cfg), nil
}

// CacheKeyVersion is the hashing-recipe version this binary computes cache keys
// with. Two keys from different recipes are not comparable, which is what makes a
// mismatch worth reporting rather than treating as changed inputs.
const CacheKeyVersion = cache.KeyVersion

// OutputDescriptor is a stored target execution's identity and outcome, the metadata
// behind a target-output ref.
type OutputDescriptor = types.OutputDescriptor

func newOutputDescriptor(d cache.OutputDescriptor) OutputDescriptor {
	return OutputDescriptor{
		Ref: d.Ref, Project: d.Project, Target: d.Target, Inv: d.Inv,
		Failed: d.Failed, ErrMsg: d.ErrMsg, TimestampMs: d.TimestampMs, DurationMs: d.DurationMs,
		Key: d.Key, KeyVersion: d.KeyVersion, Attempt: d.Attempt, MagusVersion: d.MagusVersion,
		Revision: d.Revision, Dirty: d.Dirty,
		Spell: d.Spell, ExtraArgs: d.ExtraArgs, VCSName: d.VCSName, Platform: d.Platform,
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

// OutputAttempts lists every stored execution of the step ref names, newest first: the
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
// output already travels with its cache artifact; this is what makes a FAILING run
// (never cached, never pushed) shareable, and it is always an explicit act because
// captured output can contain anything the target printed. The bundle carries no
// manifest and no blobs, so it can never be replayed as a cache hit. Requires a
// remote backend and a signing key; [types.ErrNoCache] on an Inspect workspace.
func (m *Magus) PublishOutput(ctx context.Context, ref string) (string, error) {
	if m.cache == nil {
		return "", types.ErrNoCache
	}
	return m.cache.PublishOutput(m.ContextWithSecrets(ctx), ref)
}

// OutputByRefRemote resolves a ref to its captured bytes and descriptor, falling back
// to the remote published-output namespace when the ref is unknown locally, so an
// inspect line pasted from CI or a teammate resolves even on a machine that never ran
// the target. Requires a live cache (the remote backend and trust set live there); on
// an Inspect workspace it degrades to the local-only path.
func (m *Magus) OutputByRefRemote(ctx context.Context, ref string) ([]byte, OutputDescriptor, error) {
	if m.cache == nil {
		return m.OutputByRef(ref)
	}
	data, d, err := m.cache.OutputByRef(m.ContextWithSecrets(ctx), ref)
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

// OutputKeyInputs returns the pre-hash key inputs stored behind ref: the deterministic
// label:value lines hashStep consumed to mint the step's cache key, secret-redacted at
// write. They feed `magus query output <ref> --meta`
// (component-class digests) and `describe target --cache --against <ref>` (the exact
// disagreeing line). Returns fs.ErrNotExist when the ref resolves but the run predates
// key-input persistence.
func (m *Magus) OutputKeyInputs(ref string) ([]string, error) {
	return cache.NewOutputStore(resolveCacheDir(m.ws.Root, m.cfg)).KeyInputsByRef(ref)
}

// LastRecordedRun returns the most recent cache entry recorded for target in projectPath
// together with the key inputs behind it: the peer `describe target --cache` compares a
// live key against to explain why a run here would MISS. Wraps fs.ErrNotExist when
// nothing is recorded for that target; [types.ErrNoCache] on an Inspect workspace.
func (m *Magus) LastRecordedRun(projectPath, target string) (cache.RecordedRun, error) {
	if m.cache == nil {
		return cache.RecordedRun{}, types.ErrNoCache
	}
	return m.cache.LastRecordedRun(projectPath, target)
}

// InvocationByID resolves an invocation id (OutputDescriptor.Inv) to its run header (the command
// lineage (subcommand/args/trigger), timing, and outcome) read from the union run log. It is the
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
