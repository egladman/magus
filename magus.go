package magus

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	buzz "github.com/egladman/gopherbuzz"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/ci/flake"
	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/config"
	configgen "github.com/egladman/magus/internal/config/gen"
	"github.com/egladman/magus/internal/depgraph"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/observability"
	ispell "github.com/egladman/magus/internal/spell"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// Magus is the high-level orchestrator. Not safe for concurrent use. Inspect-constructed workspaces have no cache.
type Magus struct {
	ws    *types.Workspace
	cfg   config.Config
	cache *cache.Cache

	limOnce sync.Once
	lim     *cache.Limiter

	buzzPoolOnce sync.Once
	buzzPoolReg  *buzz.PoolRegistry

	wsReg *WorkspaceRegistry

	tel observability.Provider
}

// rootMarkers lists workspace-root markers in priority order; magus markers precede go.mod.
var rootMarkers = []string{
	"magusfiles",
	"magusfile.buzz",
	"magus.yaml",
	"go.mod",
}

// FindRoot walks up from dir (or cwd when empty) to find the nearest workspace root.
func FindRoot(dir string) (string, error) {
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = cwd
	}
	cur, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	markerSet := make(map[string]struct{}, len(rootMarkers))
	for _, m := range rootMarkers {
		markerSet[m] = struct{}{}
	}
	for {
		entries, err := os.ReadDir(cur)
		if err == nil {
			for _, e := range entries {
				if _, ok := markerSet[e.Name()]; ok {
					return cur, nil
				}
			}
		} else {
			for _, marker := range rootMarkers {
				if _, err := os.Stat(filepath.Join(cur, marker)); err == nil {
					return cur, nil
				}
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", errors.New("magus: could not locate workspace root (no magusfiles/, magusfile.buzz, magus.yaml, or go.mod found)")
		}
		cur = parent
	}
}

// Inspect discovers the workspace without opening the cache (for introspection commands).
func Inspect(ctx context.Context, root string, opts ...Option) (types.WorkspaceRepository, error) {
	m, err := inspect(ctx, root, opts...)
	if err != nil {
		return nil, err
	}
	if err := m.finishConstruction(ctx); err != nil {
		return nil, err
	}
	return m, nil
}

// finishConstruction completes workspace setup shared by Inspect and Open:
// magusfile preloading, workspace-registry application, and magusfile spell autobind.
func (m *Magus) finishConstruction(ctx context.Context) error {
	if err := preloadMagusfiles(ctx, m); err != nil {
		return err
	}
	if err := m.wsReg.Apply(m); err != nil {
		return err
	}
	// Fold target-level cross-project deps (project imports) into DependsOn so
	// they count toward the affected set, just like a project-level depends_on.
	if err := m.applyCrossProjectDependencies(ctx); err != nil {
		return err
	}
	m.autobindMagusfileSpell()
	return nil
}

func inspect(ctx context.Context, root string, opts ...Option) (*Magus, error) { //nolint:revive // intentional exported/unexported pair, not a typo
	cfg, err := loadConfig(root, opts...)
	if err != nil {
		return nil, err
	}
	ws, err := project.Discover(ctx, root)
	if err != nil {
		return nil, err
	}
	m := &Magus{ws: ws, cfg: cfg}
	var o workspace.Load
	for _, fn := range opts {
		fn(&o)
	}
	if o.Limiter != nil {
		m.limOnce.Do(func() { m.lim = o.Limiter })
	}
	if o.Registry != nil {
		m.wsReg = o.Registry
	} else {
		m.wsReg = NewWorkspaceRegistry()
	}
	return m, nil
}

func loadConfig(root string, opts ...Option) (config.Config, error) {
	var o workspace.Load
	for _, fn := range opts {
		fn(&o)
	}
	if o.Preloaded != nil {
		return *o.Preloaded, nil
	}
	path := o.ConfigPath
	if path == "" {
		path = filepath.Join(root, "magus.yaml")
	}
	cfg, err := config.LoadFile(path, false)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return config.Config{}, nil
		}
		return config.Config{}, err
	}
	configgen.ApplyEnv(&cfg, os.Getenv)
	return cfg, nil
}

// preloadMagusfiles parses magusfiles in each project so magus.project() calls populate m.wsReg.
func preloadMagusfiles(ctx context.Context, m *Magus) error {
	if !interp.Available() {
		return nil
	}
	ctx = installWorkspaceRegistry(ctx, m.wsReg)
	for _, p := range m.All() {
		srcs, err := interp.FindAll(p.Dir)
		if err != nil {
			if errors.Is(err, interp.ErrNoMagusfile) {
				continue
			}
			return fmt.Errorf("magus: preload %q: %w", p.Path, err)
		}
		pctx := interp.WithProjectPath(ctx, p.Path)
		for _, src := range srcs {
			if _, err := interp.Parse(pctx, src); err != nil {
				return fmt.Errorf("magus: preload %q: %w", p.Path, err)
			}
		}
	}
	return nil
}

// autobindMagusfileSpell binds the "magusfile" spell to projects with a magusfile but no resolved spell.
func (m *Magus) autobindMagusfileSpell() {
	if !interp.Available() {
		return
	}
	magusfileSpell, ok := project.DefaultSpellRegistry().Lookup("magusfile")
	if !ok {
		return
	}
	for _, p := range m.All() {
		if len(p.ResolvedSpells) > 0 {
			continue
		}
		if _, err := interp.Find(p.Dir); err != nil {
			continue
		}
		p.AttachSpell(magusfileSpell)
		p.ResolvedSpells = []*types.Spell{magusfileSpell}
	}
}

// Open opens a Magus orchestrator rooted at root with cache and telemetry.
// Telemetry init failure falls back to a no-op provider. Use Inspect for read-only callers.
// signingKeyEnv carries the base64 Ed25519 seed used to sign remote cache entries:
// a secret, set only in trusted CI.
const signingKeyEnv = "MAGUS_CACHE_SIGNING_KEY"

// remoteCacheSigningOpts turns the declared trust set (base64 public keys) plus the
// signing-key env var into cache options, enforcing that a wired remote backend
// declares a non-empty trust set so a shared cache never comes up unverified —
// unless insecure is set, the explicit opt-out that accepts and produces unsigned
// artifacts (no trust set, no signing key) for trusted single-repo CI or backend
// validation.
func remoteCacheSigningOpts(trustedB64 []string, insecure bool) ([]cache.Option, error) {
	if insecure {
		return []cache.Option{cache.WithInsecureRemote()}, nil
	}
	if len(trustedB64) == 0 {
		return nil, fmt.Errorf("magus: a remote cache backend is wired (magus.cache.remote) but no trust set is declared; " +
			"set cache.remote.trusted_keys in magus.yaml to the Ed25519 public key(s) that sign artifacts (or set " +
			"cache.remote.insecure / MAGUS_CACHE_REMOTE_INSECURE to accept unsigned artifacts) — " +
			"a shared cache with no signature verification is a supply-chain hazard and is not allowed by default")
	}
	pubkeys := make([][]byte, 0, len(trustedB64))
	for i, k := range trustedB64 {
		raw, err := base64.StdEncoding.DecodeString(k)
		if err != nil {
			return nil, fmt.Errorf("magus: trusted key %d is not valid base64: %w", i, err)
		}
		pubkeys = append(pubkeys, raw)
	}
	opts := []cache.Option{cache.WithTrustedKeys(pubkeys)}

	if seedB64 := os.Getenv(signingKeyEnv); seedB64 != "" {
		seed, err := base64.StdEncoding.DecodeString(seedB64)
		if err != nil {
			return nil, fmt.Errorf("magus: %s is not valid base64: %w", signingKeyEnv, err)
		}
		opts = append(opts, cache.WithSigningKey(seed))
	}
	return opts, nil
}

func Open(ctx context.Context, root string, opts ...Option) (*Magus, error) {
	m, err := inspect(ctx, root, opts...)
	if err != nil {
		return nil, err
	}
	// Evaluate magusfiles before building the cache: project registration, spell
	// autobind, and any magus.cache.remote() backend wiring all happen here, so a
	// magusfile-chosen remote backend can be attached at cache construction.
	if err := m.finishConstruction(ctx); err != nil {
		return nil, err
	}

	cacheDir := filepath.Join(m.ws.Root, ".magus")
	if m.cfg.Cache.Dir != "" {
		if filepath.IsAbs(m.cfg.Cache.Dir) {
			cacheDir = filepath.Clean(m.cfg.Cache.Dir)
		} else {
			cacheDir = filepath.Join(m.ws.Root, m.cfg.Cache.Dir)
		}
	} else if override := os.Getenv("MAGUS_CACHE_DIR"); override != "" {
		if filepath.IsAbs(override) {
			cacheDir = filepath.Clean(override)
		} else {
			cacheDir = filepath.Join(m.ws.Root, override)
		}
	}
	cfgOpts := []cache.Option{cache.WithMutable(!m.cfg.Cache.Immutable)}
	if m.cfg.Cache.SizeMB != 0 {
		cfgOpts = append(cfgOpts, cache.WithSizeMB(m.cfg.Cache.SizeMB))
	}
	cfgOpts = append(cfgOpts, cache.WithLog(m.cfg.Log.Format, m.cfg.Log.SlogLevel()))
	cfgOpts = append(cfgOpts, cache.WithSilent(m.cfg.Log.IsSilent()))
	// Build the telemetry provider before the cache so a wired remote backend can
	// be instrumented as it is attached. Init failure falls back to a no-op.
	tel, err := observability.New(ctx, observability.ConfigFromTelemetry(m.cfg.Telemetry, "", m.ws.Root))
	if err != nil {
		slog.Warn("magus: telemetry init failed; falling back to no-op", "err", err)
		tel, _ = observability.New(ctx, observability.Config{})
	}
	m.tel = tel
	// A magusfile may wire a remote cache backend via magus.cache.remote(<spell>);
	// resolve it through the bindings-registered opener and attach it. The backend
	// self-gates, so wiring it is harmless locally; InstrumentRemoteBackend is a
	// no-op wrapper when telemetry is off. A shared cache is a trust boundary, so it
	// REQUIRES a trust set (cache.remote.trusted_keys in magus.yaml), enforced at load on
	// every machine so the misconfiguration can't silently go live.
	if name := m.wsReg.RemoteBackend(); name != "" {
		trusted, sErr := remoteCacheSigningOpts(m.cfg.Cache.Remote.TrustedKeys, m.cfg.Cache.Remote.Insecure)
		if sErr != nil {
			return nil, sErr
		}
		cfgOpts = append(cfgOpts, trusted...)
		if rb, rErr := cache.OpenRemoteBackend(ctx, name); rErr != nil {
			slog.WarnContext(ctx, "magus: remote cache backend init failed; continuing local-only", slog.String("error", rErr.Error()))
		} else if rb != nil {
			cfgOpts = append(cfgOpts, cache.WithRemoteBackend(observability.InstrumentRemoteBackend(rb, tel)))
		}
	}
	c, err := cache.Open(cacheDir, cfgOpts...)
	if err != nil {
		return nil, err
	}
	m.cache = c
	m.limiter().SetHooks(
		func(waitNs int64, n int) {
			m.tel.RecordPoolAcquire(ctx, float64(waitNs)/1e9, int64(n))
		},
		func(n int) {
			m.tel.RecordPoolRelease(ctx, int64(n))
		},
	)
	return m, nil
}

func (m *Magus) Root() string                   { return m.ws.Root }
func (m *Magus) All() []*types.Project          { return m.ws.All() }
func (m *Magus) Get(path string) *types.Project { return m.ws.Get(path) }
func (m *Magus) Graph() (*types.Graph, error)   { return depgraph.Build(m.ws) }

// SetGraphObserver installs an observer on the workspace; pass nil to clear.
func (m *Magus) SetGraphObserver(o types.Observer) {
	m.ws.SetGraphObserver(o)
}

func (m *Magus) VCSOptions() types.VCSOptions { return m.ws.VCSOptions }

func (m *Magus) Where(dir string) (*types.Project, bool) {
	return project.Where(m.ws, dir)
}

// Affected computes projects touched by VCS changes since base.
func (m *Magus) Affected(ctx context.Context, base string) (*types.AffectedResult, error) {
	return project.Affected(ctx, m.ws, base)
}

// AffectedFromPaths computes the affected set from an explicit file list.
func (m *Magus) AffectedFromPaths(ctx context.Context, paths []string) (*types.AffectedResult, error) {
	return project.AffectedFromPaths(ctx, m.ws, paths)
}

// insightScan defaults opts.Dir to the workspace root and runs the shared history
// scan every insight lens aggregates from.
func (m *Magus) insightScan(ctx context.Context, opts *types.InsightOptions) ([]project.ScannedCommit, error) {
	if opts.Dir == "" {
		opts.Dir = m.ws.Root
	}
	return project.Scan(ctx, m.ws, opts.Dir, opts.Commits, opts.Since)
}

// Hotspots is the churn × complexity lens. The project view is the dependency graph
// heat-coloured by churn (with authors, recency, blast radius, and CI duration on each
// node); --files ranks individual files by edit frequency weighted by complexity.
func (m *Magus) Hotspots(ctx context.Context, opts types.InsightOptions) (types.HotspotOutput, error) {
	scan, err := m.insightScan(ctx, &opts)
	if err != nil {
		return types.HotspotOutput{}, err
	}
	g, err := m.Graph()
	if err != nil {
		return types.HotspotOutput{}, err
	}
	// Pull per-project CI duration onto the nodes (the "× CI cost" signal) when a
	// history file is configured; best-effort, silently skipped when unavailable.
	compose := []ComposeOption{WithGraphInput(g)}
	if path := m.cfg.HistoryPath; path != "" {
		var hist forecast.History
		if err := hist.Load(ctx, path); err == nil {
			compose = append(compose, WithGraphHistory(&hist, "ci"))
		}
	}
	out := ComposeGraph(m, compose...)
	stats := project.ProjectStats(scan)
	for i := range out.Nodes {
		st, ok := stats[out.Nodes[i].Path]
		if !ok {
			continue
		}
		out.Nodes[i].Churn = st.Commits
		out.Nodes[i].Authors = st.Authors
		if !st.Last.IsZero() {
			last := st.Last
			out.Nodes[i].LastCommit = &last
		}
	}
	res := types.HotspotOutput{
		Definition: types.HotspotDefinition,
		Commits:    opts.Commits,
		Since:      opts.Since,
		Nodes:      out.Nodes,
	}
	if opts.Files {
		res.Files = project.FileHotspots(scan, func(rel string) int {
			return project.Complexity(filepath.Join(m.ws.Root, rel))
		})
	}
	return res, nil
}

// Affinity is the temporal-coupling lens: projects that change together, with the
// pairs that lack any declared dependency between them flagged as hidden affinity.
func (m *Magus) Affinity(ctx context.Context, opts types.InsightOptions) (types.AffinityOutput, error) {
	scan, err := m.insightScan(ctx, &opts)
	if err != nil {
		return types.AffinityOutput{}, err
	}
	pairs := project.Affinity(scan)
	declared := m.declaredDeps()
	for i := range pairs {
		if !declared[pairs[i].A][pairs[i].B] && !declared[pairs[i].B][pairs[i].A] {
			pairs[i].Hidden = true
		}
	}
	return types.AffinityOutput{
		Definition: types.AffinityDefinition,
		Commits:    opts.Commits,
		Since:      opts.Since,
		Pairs:      pairs,
	}, nil
}

// declaredDeps returns each project's direct dependency set (both directions are
// looked up by the affinity lens to decide whether a co-change pair is hidden).
func (m *Magus) declaredDeps() map[string]map[string]bool {
	out := make(map[string]map[string]bool, len(m.ws.Projects))
	for _, p := range m.All() {
		set := make(map[string]bool, len(p.DependsOn))
		for _, d := range p.DependsOn {
			set[d] = true
		}
		out[p.Path] = set
	}
	return out
}

// Ownership is the knowledge-risk lens: author concentration, bus factor, and
// abandonment (projects gone quiet in the recent half of the window).
func (m *Magus) Ownership(ctx context.Context, opts types.InsightOptions) (types.OwnershipOutput, error) {
	scan, err := m.insightScan(ctx, &opts)
	if err != nil {
		return types.OwnershipOutput{}, err
	}
	return types.OwnershipOutput{
		Definition: types.OwnershipDefinition,
		Commits:    opts.Commits,
		Since:      opts.Since,
		Projects:   project.Ownership(scan, project.Midpoint(scan)),
	}, nil
}

// Trend is the rising/cooling lens: each project's churn in the recent vs earlier
// half of the window.
func (m *Magus) Trend(ctx context.Context, opts types.InsightOptions) (types.TrendOutput, error) {
	scan, err := m.insightScan(ctx, &opts)
	if err != nil {
		return types.TrendOutput{}, err
	}
	return types.TrendOutput{
		Definition: types.TrendDefinition,
		Commits:    opts.Commits,
		Since:      opts.Since,
		Projects:   project.Trend(scan),
	}, nil
}

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
		return 0, 0, ErrNoCache
	}
	return m.cache.Prune(ctx, cutoff, dryRun)
}

// PruneRemoteCache evicts entries from the configured remote cache backend per a
// retention policy (age and/or newest-N count). It errors when no remote backend is
// wired (magus.cache.remote(...)), the backend can't prune, or it's inactive here.
//
// The policy is taken as scalars rather than a cache.RetentionPolicy so this public
// facade stays free of the internal cache type (which an external importer can't
// name) — mirroring PruneCache's time.Time/bool signature; the struct is assembled
// here, one call from where it's consumed.
func (m *Magus) PruneRemoteCache(ctx context.Context, olderThan time.Duration, keepLast int, dryRun bool) error {
	if m.cache == nil {
		return ErrNoCache
	}
	return m.cache.PruneRemote(ctx, cache.RetentionPolicy{OlderThan: olderThan, KeepLast: keepLast, DryRun: dryRun})
}

// ExportCache writes the entire cache to w as a gzip-compressed tar archive.
// Returns [ErrNoCache] on Inspect workspaces.
func (m *Magus) ExportCache(ctx context.Context, w io.Writer) error {
	if m.cache == nil {
		return ErrNoCache
	}
	return m.cache.Export(ctx, w)
}

// ImportCache extracts a gzip-compressed tar archive produced by [Magus.ExportCache].
// Returns [ErrNoCache] on Inspect workspaces.
func (m *Magus) ImportCache(ctx context.Context, r io.Reader) error {
	if m.cache == nil {
		return ErrNoCache
	}
	return m.cache.Import(ctx, r)
}

// TailLog returns the log-file path of the most recent cache entry for projectPath,
// optionally restricted to target. Wraps fs.ErrNotExist when not found; [ErrNoCache] on Inspect.
func (m *Magus) TailLog(projectPath, target string) (logPath string, err error) {
	if m.cache == nil {
		return "", ErrNoCache
	}
	if target != "" {
		_, logPath, err = m.cache.LastEntryForTarget(projectPath, target)
		return logPath, err
	}
	_, logPath, err = m.cache.LastEntry(projectPath)
	return logPath, err
}

func (m *Magus) limiter() *cache.Limiter {
	m.limOnce.Do(func() {
		n := m.cfg.Concurrency
		if n <= 0 {
			n = cache.DefaultConcurrency()
		}
		m.lim = cache.NewLimiter(n)
	})
	return m.lim
}

// buzzPoolRegistry returns the shared Buzz session pool registry.
// The semaphore is derived from context at execution time (the workspace
// limiter is stored in ctx by the RunAll scheduler), so individual pools
// in the registry do not hold their own semaphore.
func (m *Magus) buzzPoolRegistry() *buzz.PoolRegistry {
	m.buzzPoolOnce.Do(func() {
		lim := m.limiter()
		getSem := func(ctx context.Context) buzz.Semaphore {
			l := cache.LimiterFromContext(ctx)
			if l == nil {
				return nil
			}
			return l
		}
		m.buzzPoolReg = buzz.NewPoolRegistry(getSem, lim.Capacity())
	})
	return m.buzzPoolReg
}

// Close releases workspace resources (VM pools); cache and limiter are caller-owned.
func (m *Magus) Close() error {
	var errs []error
	if m.buzzPoolReg != nil {
		errs = append(errs, m.buzzPoolReg.Close())
	}
	var joined error
	for _, e := range errs {
		if e != nil {
			joined = errors.Join(joined, e)
		}
	}
	return joined
}

func (m *Magus) flakeConfig() flake.Config {
	return flake.Config{
		Enabled:          m.cfg.Flake.Enabled,
		BootstrapSamples: m.cfg.Flake.BootstrapSamples,
		MinSamples:       m.cfg.Flake.MinSamples,
		Threshold:        m.cfg.Flake.Threshold,
	}
}

// baseStep returns the cache.Step for p; always includes magusfiles so edits produce a miss.
func (m *Magus) baseStep(p *types.Project) cache.Step {
	sources := make([]string, 0, len(p.Sources))
	for _, glob := range p.Sources {
		sources = append(sources, joinGlob(p.Path, glob))
	}
	sources = append(sources, magusfileGlobs(p.Path)...)
	if p.Path != "." {
		sources = append(sources, magusfileGlobs(".")...)
	}
	outputs := make([]string, 0, len(p.Outputs))
	for _, o := range p.Outputs {
		outputs = append(outputs, joinGlob(p.Path, o))
	}
	return cache.Step{
		ProjectPath:     p.Path,
		Sources:         sources,
		Outputs:         outputs,
		WorkspaceRoot:   m.ws.Root,
		SpellDefVersion: ispell.BuiltinsHash(),
	}
}

func magusfileGlobs(projectPath string) []string {
	names := []string{
		"magusfile.buzz",
		"magusfiles/**/*.buzz",
	}
	if projectPath == "." {
		return names
	}
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = projectPath + "/" + n
	}
	return out
}

func joinGlob(path, glob string) string {
	if path == "." {
		return glob
	}
	return path + "/" + glob
}

// ExpandPath resolves the target pattern to concrete per-project targets; empty or "/" fans out to all.
func (m *Magus) ExpandPath(t types.Target) ([]types.Target, error) {
	path := t.Path
	if path == "" || path == "/" {
		all := m.All()
		out := make([]types.Target, len(all))
		for i, p := range all {
			out[i] = types.Target{Path: p.Path, Name: t.Name}
		}
		return out, nil
	}
	if strings.HasPrefix(path, "ws:") {
		return nil, fmt.Errorf("magus: expand: unknown project %q: use \":\" for all projects", path)
	}
	if m.Get(path) == nil {
		return nil, fmt.Errorf("magus: expand: %w: %q", types.ErrUnknownProject, path)
	}
	return []types.Target{{Path: path, Name: t.Name}}, nil
}

// ExpandCwd resolves t for the project containing cwd; found=false when cwd is not inside any project.
func (m *Magus) ExpandCwd(t types.Target) (targets []types.Target, found bool, err error) {
	cwd, cwdErr := os.Getwd()
	if cwdErr != nil {
		return nil, false, fmt.Errorf("magus: getwd: %w", cwdErr)
	}
	p, ok := m.Where(cwd)
	if !ok {
		return nil, false, nil
	}
	return []types.Target{{Path: p.Path, Name: t.Name}}, true, nil
}

// ExpandAffected resolves targets for VCS-affected projects; falls back to all
// projects on VCS failure. fellBack is true precisely when the VCS couldn't compute
// a definitive set and every project was selected as a safety net — a typed signal
// callers can act on (e.g. annotate the plan) rather than parsing the free-text
// source string, which on the fallback path carries the underlying error message.
func (m *Magus) ExpandAffected(ctx context.Context, target string, baseRef string) (targets []types.Target, source string, fellBack bool, err error) {
	r, err := m.Affected(ctx, baseRef)
	if errors.Is(err, types.ErrAffectedFallback) {
		all, allErr := m.ExpandPath(types.Target{Name: target})
		if allErr != nil {
			return nil, "", false, allErr
		}
		return all, err.Error(), true, nil
	}
	if err != nil {
		return nil, "", false, err
	}

	res, err := vcs.Resolve(ctx, m.ws.Root, r.Base, m.ws.VCSOptions)
	if err != nil {
		return nil, "", false, err
	}
	source = res.Name + " diff vs " + r.Base
	if res.Source == types.VCSSourceDisabled {
		source = "vcs disabled vs " + r.Base
	}

	out := make([]types.Target, len(r.Affected))
	for i, path := range r.Affected {
		out[i] = types.Target{
			Path:  path,
			Name:  target,
			Files: r.FilesBySeed[path],
		}
	}
	return out, source, false, nil
}

// TargetLabel returns a one-line summary of a target slice suitable for log headers.
func TargetLabel(targets []types.Target, source string) string {
	if len(targets) == 0 {
		label := "no projects"
		if source != "" {
			label += " (" + source + ")"
		}
		return label
	}

	switch len(targets) {
	case 1:
		label := targets[0].Path
		if source != "" {
			label += " (" + source + ")"
		}
		return label
	default:
		label := fmt.Sprintf("%d projects", len(targets))
		if source != "" {
			label += " (" + source + ")"
		}
		return label
	}
}

// forEachSpell runs fn against every spell on p. Spells run in parallel unless
// p.Exclusive is set; all run to completion so one failure does not mask others.
// When the context carries a [cache.Limiter] and the caller holds a slot, the
// parallel branch yields the slot and each spell acquires its own, keeping total
// concurrent spells bounded by the workspace concurrency cap.
func forEachSpell(ctx context.Context, p *types.Project, target string, fn func(context.Context, *types.Spell) error) error {
	spells := p.ResolvedSpells
	if len(spells) == 0 {
		return nil
	}
	dispatch := func(ctx context.Context, i int, s *types.Spell) error {
		effective := project.EffectiveClaims(p, i)
		pctx := ctx
		if effective != nil {
			pctx = types.WithEffectiveClaims(ctx, effective)
		}
		return fn(pctx, s)
	}
	if len(spells) == 1 {
		if err := dispatch(ctx, 0, spells[0]); err != nil {
			return &types.SpellErrors{Project: p.Path, Target: target, Failed: []types.SpellError{{Spell: spells[0].Name(), Err: err}}}
		}
		return nil
	}
	if p.Exclusive {
		var failed []types.SpellError
		for i, s := range spells {
			if err := dispatch(ctx, i, s); err != nil {
				failed = append(failed, types.SpellError{Spell: s.Name(), Err: err})
			}
		}
		if len(failed) == 0 {
			return nil
		}
		return &types.SpellErrors{Project: p.Path, Target: target, Failed: failed}
	}

	lim := cache.LimiterFromContext(ctx)
	slotHeld := lim != nil && cache.SlotHeld(ctx)
	bounded := lim != nil

	type result struct {
		name string
		err  error
	}
	results := make([]result, len(spells))
	var wg sync.WaitGroup

	fanOut := func() {
		for i, s := range spells {
			wg.Add(1)
			go func(i int, s *types.Spell) {
				defer wg.Done()
				spellCtx := ctx
				if bounded {
					if err := lim.Acquire(ctx); err != nil {
						results[i] = result{name: s.Name(), err: err}
						return
					}
					spellCtx = cache.WithSlotHeld(ctx)
					defer lim.Release()
				}
				results[i] = result{name: s.Name(), err: dispatch(spellCtx, i, s)}
			}(i, s)
		}
		wg.Wait()
	}

	if slotHeld {
		// Yield our held slot so per-spell acquisitions cannot deadlock at low budgets.
		_ = lim.Yield(ctx, func() error { fanOut(); return nil })
	} else {
		fanOut()
	}

	var failed []types.SpellError
	for _, r := range results {
		if r.err != nil {
			failed = append(failed, types.SpellError{Spell: r.name, Err: r.err})
		}
	}
	if len(failed) == 0 {
		return nil
	}
	return &types.SpellErrors{Project: p.Path, Target: target, Failed: failed}
}

// forSpellNamed is like forEachSpell but targets only the spell whose Name
// equals name. If no matching spell is registered the call is a no-op.
func forSpellNamed(ctx context.Context, p *types.Project, target, name string, fn func(context.Context, *types.Spell) error) error {
	for i, s := range p.ResolvedSpells {
		if s.Name() != name {
			continue
		}
		effective := project.EffectiveClaims(p, i)
		pctx := ctx
		if effective != nil {
			pctx = types.WithEffectiveClaims(ctx, effective)
		}
		if err := fn(pctx, s); err != nil {
			return &types.SpellErrors{Project: p.Path, Target: target, Failed: []types.SpellError{{Spell: s.Name(), Err: err}}}
		}
		return nil
	}
	return nil
}
