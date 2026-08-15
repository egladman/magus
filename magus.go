package magus

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/ci/volatility"
	"github.com/egladman/magus/internal/config"
	configgen "github.com/egladman/magus/internal/config/gen"
	"github.com/egladman/magus/internal/graph/dependency"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/observability/otlp"
	"github.com/egladman/magus/internal/secret"
	"github.com/egladman/magus/internal/spellruntime"
	"github.com/egladman/magus/internal/ward"
	"github.com/egladman/magus/internal/workspace"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// collapseOnSuccess decides whether per-project subprocess output is withheld until a
// failure. It is the default for human output; -v streams live.
func collapseOnSuccess(l config.Log) bool {
	switch strings.ToLower(l.Format) {
	case "pretty", "plain", "":
		// human formats can collapse
	default:
		return false
	}
	// Keyed on Stream, not on Level. Raising the log level asks for more RECORDS;
	// it is not a request to stop withholding each target's own output. -vv sets
	// Stream explicitly for callers who do want that.
	if l.IsSilent() || l.IsStream() {
		return false
	}
	return true
}

// Magus is the high-level orchestrator. Read paths and the daemon's warm caches
// (warmGraph, symbolStatus) are safe for concurrent use - that is what backs
// daemon mode, where one Magus is shared across goroutines; concurrent callers
// should use types.ContextWithGraphObserver rather than a shared default observer.
// SetGraphObserver and SetDaemon are NOT concurrency-safe - each mutates shared
// state with no lock and is meant to be called once, before the workspace is
// shared (SetGraphObserver mutates the underlying *types.Workspace, which
// documents itself as safe only for a sole owner). Inspect-constructed
// workspaces have no cache.
type Magus struct {
	ws    *types.Workspace
	cfg   config.Config
	cache *cache.Cache
	// version is the running build, as the caller reported it via WithVersion. Kept so
	// a workspace-load failure can say what this binary IS - the one thing an
	// out-of-date binary can state about itself. See explainStale.
	version string

	limOnce sync.Once
	lim     *cache.Limiter

	buzzPoolOnce sync.Once
	buzzPoolReg  *buzz.PoolRegistry

	warmGraphOnce sync.Once
	warmGraph     *warmGraph

	symbolStatus symbolStatusCache

	wsReg *WorkspaceRegistry

	// resolver is shared with preloadMagusfiles, so a magusfile with a top-level
	// magus\secret.read costs one provider invocation rather than two.
	resolver *secret.Resolver

	// hostMemBytes caches the machine's memory for slotsForPolicy; see hostTotalBytes.
	hostMemOnce  sync.Once
	hostMemBytes int64

	tel            observability.Provider
	injectedTel    observability.Provider // shared provider supplied via WithProvider; adopted verbatim in Open
	metricsCollect bool                   // daemon: build an always-on local metrics collector for the dashboard
	skipProviders  bool                   // open without running wired workspace providers (see WithoutWorkspaceProviders)

	daemon Daemon
}

// Daemon is the long-running server this workspace hosts (the MCP HTTP endpoint plus the
// console API routes, and whatever else the daemon grows to serve). It is injected by
// the CLI in daemon mode ONLY - so ordinary command paths never construct one - and held
// as an interface so the root magus package need not import the daemon/handler packages
// (which depend on magus), breaking that cycle. The concrete *daemon.Daemon satisfies it.
type Daemon interface {
	Serve(ctx context.Context) error
}

// SetDaemon installs the daemon that ServeDaemon delegates to. Called once, in daemon
// mode; other command paths leave it nil so no server is ever constructed.
func (m *Magus) SetDaemon(d Daemon) { m.daemon = d }

// ServeDaemon runs the injected daemon, blocking until ctx is cancelled or the server
// fails. It errors if no daemon was installed via SetDaemon.
func (m *Magus) ServeDaemon(ctx context.Context) error {
	if m.daemon == nil {
		return errors.New("magus: no daemon configured")
	}
	return m.daemon.Serve(ctx)
}

// workspaceMarker is the ONE file that declares a workspace root.
//
// Deliberately a single name, not a set. The earlier attempt listed go.mod here too,
// on the theory that it only appears at a repo's top - false in any multi-module repo,
// including this one (libs/diagnostics/go.mod, libs/gopherbuzz/go.mod). That reintroduced the
// exact defect it was meant to fix, one level down: running from libs/diagnostics made
// libs/diagnostics the workspace, so it locked libs/diagnostics/.magus/locks while a root run locked
// the root's, and the two stopped excluding each other despite touching the same files.
const workspaceMarker = "magus.yaml"

// projectMarkers mark a PROJECT, a unit INSIDE a workspace, and say nothing about
// where the workspace begins.
//
// Separating these from the workspace marker is the fix. Treating a magusfile as a
// root meant the walk halted at the first one it met, so running from a subproject
// silently redefined the workspace as that subproject: `magus ls` from a nested
// directory reported one project instead of the real set, and `affected` answered
// confidently about a workspace that did not exist.
var projectMarkers = []string{"magusfiles", "magusfile.buzz", "go.mod"}

// dirHasMarker reports whether dir carries any of the named markers. A stat error
// other than "not found" is reported as PRESENT: refusing to walk past a directory we
// cannot read is safer than silently continuing into whatever is above it and
// adopting an unrelated tree as the workspace.
func dirHasMarker(dir string, markers []string) bool {
	for _, m := range markers {
		if _, err := os.Stat(filepath.Join(dir, m)); err == nil || !errors.Is(err, fs.ErrNotExist) {
			return true
		}
	}
	return false
}

// FindRoot walks up from dir (or cwd when empty) to find the workspace root.
//
// The NEAREST magus.yaml wins, because it is the only file that declares "the
// workspace starts here" and the closest declaration is the governing one - the same
// rule .git follows, and the reason a git worktree nested inside its parent repo
// resolves to itself rather than being swallowed by the parent.
//
// Absent any magus.yaml, the root is the outermost CONTIGUOUS project marker.
// Contiguous so a stray magusfile in an unrelated ancestor (a home directory, /tmp)
// cannot silently adopt everything beneath it, which an unbounded walk would allow.
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

	var contiguous string
	runBroken := false
	for {
		// Nearest wins: return the moment a declaration is found.
		if dirHasMarker(cur, []string{workspaceMarker}) {
			return cur, nil
		}
		if !runBroken {
			if dirHasMarker(cur, projectMarkers) {
				contiguous = cur
			} else if contiguous != "" {
				runBroken = true
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}

	if contiguous != "" {
		return contiguous, nil
	}
	return "", errors.New("magus: could not locate workspace root (no magus.yaml, magusfiles/, magusfile.buzz, or go.mod found)")
}

// Inspect discovers the workspace without opening the cache (for introspection commands).
func Inspect(ctx context.Context, root string, opts ...Option) (types.WorkspaceRepository, error) {
	m, err := inspect(ctx, root, opts...)
	if err != nil {
		return nil, err
	}
	if err := m.load(ctx); err != nil {
		return nil, m.explainStale(err)
	}
	return m, nil
}

// explainStale annotates a workspace-load failure that looks like an out-of-date
// binary. load() is the right place: it is where magusfiles and local spells are
// EVALUATED, and therefore where a name this build does not provide actually surfaces.
// Discover, above, only walks the tree - it cannot fail this way.
func (m *Magus) explainStale(err error) error {
	return ward.ExplainStaleBinary(err, m.version, m.cfg.RequiredVersion)
}

// load completes workspace setup shared by Inspect and Open: magusfile preloading,
// workspace-registry application, and magusfile spell autobind.
func (m *Magus) load(ctx context.Context) error {
	// Thread the workspace into ctx for the whole preload, so a magusfile's import
	// resolver (and magusSearchPaths) can read the workspace root. Without this the
	// root is only present on the run path (Magus.Run), so preload-time resolution
	// (describe, affected, ls) could not walk spell imports up to the root.
	ctx = types.WithWorkspace(ctx, m)
	customTargets, err := preloadMagusfiles(ctx, m)
	if err != nil {
		return err
	}
	// Workspace providers run HERE, in the one window where both facts they need are
	// true: the magusfiles have been evaluated (so magus\workspace.provider has named
	// its spells, and those spells are registered), and the registry has not been
	// applied yet (so magus\project("libs/foo", {...}) still layers on top of a
	// provided project rather than being overwritten by it).
	if !m.skipProviders {
		if err := workspace.AddProvidedProjects(ctx, m.ws, m.wsReg.Providers(), workspace.ProviderCache{
			Dir:       resolveCacheDir(m.ws.Root, m.cfg),
			Immutable: cacheImmutable(m.cfg),
		}); err != nil {
			return err
		}
	}
	if err := m.wsReg.Apply(m); err != nil {
		return err
	}
	// Only enforceable when the Buzz interpreter is linked in (interp.Available):
	// without it, preloadMagusfiles cannot discover a project's custom (export fun)
	// targets, so customTargets is always empty and every per-target policy would
	// falsely look unknown. cmd/magus links the interpreter unconditionally
	// (packs_interp.go); a bare library caller that doesn't gets no enforcement here,
	// same as it gets no magus.project() evaluation at all.
	if interp.Available() {
		if err := validateTargetPolicies(m, customTargets); err != nil {
			return err
		}
	}
	// Fold target-level static facts (cross-project deps, and per-target
	// magus.inputs/outputs cache footprint) into the project model.
	if err := m.applyTargetDepsAndFootprint(ctx); err != nil {
		return err
	}
	m.autobindMagusfileSpell()
	// Shadow ward: a nested spells/<name> that a root-wins ancestor already defines
	// is dead code (its import always resolves to the ancestor). Block it unless the
	// author acknowledged the shadow in magus.yaml, so the footgun is visible without
	// removing the escape hatch for a deliberate override.
	if diags, err := ward.SpellShadows(m.ws.Root, m.shadowAcknowledged); err != nil {
		return err
	} else if len(diags) > 0 {
		return diags[0]
	}
	return nil
}

// shadowAcknowledged reports whether a spell-import shadow is deliberately allowed
// by a spells.allow_shadow entry in this workspace's config. A reason is required,
// so an entry without one does not acknowledge: the shadow keeps blocking (MGS1002)
// until the author records why, keeping the escape hatch auditable at load time
// even though config schema validation runs only on save.
func (m *Magus) shadowAcknowledged(importPath string) bool {
	for _, a := range m.cfg.Spells.AllowShadow {
		if a.Name == importPath && a.Reason != "" {
			return true
		}
	}
	return false
}

func inspect(ctx context.Context, root string, opts ...Option) (*Magus, error) {
	cfg, err := loadConfig(root, opts...)
	if err != nil {
		return nil, err
	}
	// Before project discovery, and long before any magusfile is evaluated. The
	// magusfile is the thing that explodes when the binary is too old, so a floor
	// checked after it has already failed reports nothing anyone can act on.
	var vo workspace.Load
	for _, fn := range opts {
		fn(&vo)
	}
	if d := ward.CheckRequiredVersion(cfg.RequiredVersion, vo.Version); d != nil {
		return nil, d
	}
	ws, err := project.Discover(ctx, root)
	if err != nil {
		return nil, err
	}
	m := &Magus{ws: ws, cfg: cfg, version: vo.Version}
	var o workspace.Load
	for _, fn := range opts {
		fn(&o)
	}
	if o.Limiter != nil {
		m.limOnce.Do(func() { m.lim = o.Limiter })
	}
	m.metricsCollect = o.MetricsCollect
	m.injectedTel = o.Provider
	m.skipProviders = o.SkipWorkspaceProviders
	if o.Registry != nil {
		m.wsReg = o.Registry
	} else {
		m.wsReg = NewWorkspaceRegistry()
	}
	m.resolver = secret.New(secret.WithTimeouts(secret.Timeouts{
		Interactive: m.cfg.Secret.Interactive,
		Unattended:  m.cfg.Secret.Unattended,
	}))
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

// preloadMagusfiles parses magusfiles in each project so magus.project() calls
// populate m.wsReg, and returns each project's custom (export fun) target names,
// keyed by project path — used afterward by validateTargetPolicies to confirm a
// project's per-target policy table names only targets that actually exist.
func preloadMagusfiles(ctx context.Context, m *Magus) (map[string][]string, error) {
	customTargets := make(map[string][]string)
	if !interp.Available() {
		return customTargets, nil
	}
	ctx = installWorkspaceRegistry(ctx, m.wsReg)
	// The workspace's resolver, not a fresh one: this path evaluates magusfile top levels,
	// so a top-level read here must be the SAME read the run sees.
	ctx = secret.ContextWithResolver(ctx, m.resolver)
	for _, p := range m.All() {
		srcs, err := interp.FindAll(p.Dir)
		if err != nil {
			if errors.Is(err, interp.ErrNoMagusfile) {
				continue
			}
			return nil, fmt.Errorf("magus: %s: %w", types.ProjectLabel(p.Path, p.Dir), err)
		}
		pctx := interp.WithProjectPath(ctx, p.Path)
		for _, src := range srcs {
			targets, err := interp.Parse(pctx, src)
			if err != nil {
				return nil, fmt.Errorf("magus: %s: %w", types.ProjectLabel(p.Path, p.Dir), err)
			}
			for _, t := range targets {
				customTargets[p.Path] = append(customTargets[p.Path], t.Key)
			}
		}
	}
	return customTargets, nil
}

// validateTargetPolicies errors when a project's per-target policy table
// (magus.project's "targets" map) names a target that doesn't exist: neither a
// spell-contributed op nor a custom export fun in that project's magusfile.
// Without this, a typo (or a target later removed from the magusfile) silently
// produces a phantom "custom" entry in `magus describe targets` instead of
// surfacing as a load error. customTargets comes from preloadMagusfiles; spell
// targets come from each project's already-resolved spells (wsReg.Apply having
// just run), so both "kinds" of known target (per describe.go's byName) count.
func validateTargetPolicies(m *Magus, customTargets map[string][]string) error {
	for _, p := range m.All() {
		if len(p.TargetPolicies) == 0 {
			continue
		}
		known := make(map[string]struct{})
		for _, s := range p.ResolvedSpells {
			for _, t := range s.Targets() {
				known[t] = struct{}{}
			}
		}
		for _, t := range customTargets[p.Path] {
			known[t] = struct{}{}
		}
		declared := make([]string, 0, len(known))
		for t := range known {
			declared = append(declared, t)
		}
		sort.Strings(declared)

		policyNames := make([]string, 0, len(p.TargetPolicies))
		for name := range p.TargetPolicies {
			policyNames = append(policyNames, name)
		}
		sort.Strings(policyNames)

		for _, name := range policyNames {
			if _, ok := known[name]; ok {
				continue
			}
			msg := fmt.Sprintf("magus: project %q: per-target policy names unknown target %q", p.Path, name)
			if hint := interactive.SuggestNearest(name, declared); hint != "" {
				msg += fmt.Sprintf("; did you mean %q?", hint)
			}
			if len(declared) > 0 {
				msg += fmt.Sprintf(" (declared targets: %s)", strings.Join(declared, ", "))
			} else {
				msg += " (this project declares no targets)"
			}
			return errors.New(msg)
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
		p.ResolvedSpells = []*spells.Spell{magusfileSpell}
	}
}

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

// Open opens a Magus orchestrator rooted at root with cache and telemetry. It evaluates
// magusfiles first, so project registration and any remote-cache wiring are set up
// before the cache is built. Use [Inspect] for read-only callers that need no cache.
func Open(ctx context.Context, root string, opts ...Option) (*Magus, error) {
	m, err := inspect(ctx, root, opts...)
	if err != nil {
		return nil, err
	}
	// Evaluate magusfiles before building the cache: project registration, spell
	// autobind, and any magus.cache.remote() backend wiring all happen here, so a
	// magusfile-chosen remote backend can be attached at cache construction.
	if err := m.load(ctx); err != nil {
		return nil, m.explainStale(err)
	}

	cacheDir := resolveCacheDir(m.ws.Root, m.cfg)
	cfgOpts := []cache.Option{cache.WithMutable(m.cfg.Cache.WriteEnabled())}
	if m.cfg.Cache.SizeMB != 0 {
		cfgOpts = append(cfgOpts, cache.WithSizeMB(m.cfg.Cache.SizeMB))
	}
	cfgOpts = append(cfgOpts, cache.WithLog(m.cfg.Log.Format, m.cfg.Log.SlogLevel()))
	cfgOpts = append(cfgOpts, cache.WithSilent(m.cfg.Log.IsSilent()))
	cfgOpts = append(cfgOpts, cache.WithCollapse(collapseOnSuccess(m.cfg.Log)))
	// Build the telemetry provider before the cache so a wired remote backend can
	// be instrumented as it is attached. When the caller injected a shared provider
	// (WithProvider), adopt it verbatim and skip construction: the daemon shares ONE
	// provider across its bridge Magus and every per-workspace registry Magus, so a
	// build routed to any workspace feeds the same instruments the dashboard reads,
	// and workspace eviction never discards the accumulated counters. Otherwise build
	// one here; init failure falls back to a no-op.
	var tel observability.Provider
	if m.injectedTel != nil {
		tel = m.injectedTel
	} else {
		telCfg := observability.ConfigFromTelemetry(m.cfg.Telemetry, "", m.ws.Root)
		telCfg.LocalCollect = m.metricsCollect // daemon: record metrics even when export is off
		built, err := otlp.New(ctx, telCfg)
		if err != nil {
			slog.WarnContext(ctx, "magus: telemetry init failed; falling back to no-op", "err", err)
			built, _ = otlp.New(ctx, observability.Config{})
		}
		tel = built
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
	c, err := cache.Open(ctx, cacheDir, cfgOpts...)
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
		func(delta int) {
			m.tel.RecordPoolWaiting(ctx, int64(delta))
		},
	)
	return m, nil
}

func (m *Magus) Root() string                   { return m.ws.Root }
func (m *Magus) All() []*types.Project          { return m.ws.All() }
func (m *Magus) Get(path string) *types.Project { return m.ws.Get(path) }
func (m *Magus) Graph() (*types.Graph, error)   { return dependency.Build(m.ws) }

// SetGraphObserver installs an observer on the workspace; pass nil to clear.
func (m *Magus) SetGraphObserver(o types.Observer) {
	m.ws.SetGraphObserver(o)
}

func (m *Magus) VCSOptions() types.VCSOptions { return m.ws.VCSOptions }

// Telemetry returns this workspace's observability provider (nil on an Inspect workspace,
// which builds no cache and no provider). When several Magus instances were opened with a
// shared provider via [WithProvider] this returns that same instance, so metrics recorded
// through one are visible through another's [Magus.MetricsCollector].
func (m *Magus) Telemetry() observability.Provider { return m.tel }

func (m *Magus) Where(dir string) (*types.Project, bool) {
	return project.Where(m.ws, dir)
}

// BaseLastPassed is the base ref that means "the commit this branch last completed a
// fully passing run at", read from the run history rather than from the VCS. Every
// entry point that takes a base ref accepts it, so `--base last-passed`,
// MAGUS_VCS_BASE_REF, and magus\affected() all reach the same resolution.
const BaseLastPassed = "last-passed"

// Affected computes projects touched by VCS changes since base.
func (m *Magus) Affected(ctx context.Context, base string) (*types.AffectedResult, error) {
	base, err := m.resolveLastPassed(ctx, base)
	if err != nil {
		return nil, err
	}
	r, err := project.Affected(ctx, m.ws, base)
	if err != nil {
		return nil, err
	}
	noteUndeclaredSeeds(r)
	return r, nil
}

// noteUndeclaredSeeds reports MGS1028: changed files that seeded a project through
// directory containment while no project declares them, so the run they cause moves
// no cache key.
//
// interactive.Emit, not types.EmitDiagnostic, and that is not a style choice. The
// run-scoped diagnostic sink is installed inside Run, AFTER the affected set has
// already been computed, so a sink emission from here reaches nobody at all - and
// this fires on `magus affected --impact` and the MCP handlers too, which never
// enter Run. MGS1010 is emitted the same way, for the same reason. Emit dedupes by
// message text, so a long-lived daemon says it once rather than on every request.
func noteUndeclaredSeeds(r *types.AffectedResult) {
	if len(r.UndeclaredBySeed) == 0 {
		return
	}
	files := map[string]struct{}{}
	for _, fs := range r.UndeclaredBySeed {
		for _, f := range fs {
			files[f] = struct{}{}
		}
	}
	shown := slices.Sorted(maps.Keys(files))
	// A changeset can be enormous; the first few name the shape of the problem and
	// `magus describe file` is the surface that explains any one of them in full.
	if len(shown) > undeclaredSeedHintCap {
		shown = append(shown[:undeclaredSeedHintCap:undeclaredSeedHintCap],
			fmt.Sprintf("and %d more", len(files)-undeclaredSeedHintCap))
	}
	interactive.Emit(os.Stderr, fmt.Sprintf(
		"[%s] %d changed file(s) seed a project by directory containment while no project declares them, "+
			"so the targets they rerun were already correct: %s. "+
			"Declare them in the owning project's sources, or leave them undeclared deliberately (see %s)",
		types.UndeclaredSeedingFile, len(files), strings.Join(shown, ", "),
		types.CodeURL(types.UndeclaredSeedingFile)))
}

// undeclaredSeedHintCap bounds how many undeclared paths MGS1028 names inline.
const undeclaredSeedHintCap = 5

// resolveLastPassed translates [BaseLastPassed] into the commit the current branch last
// passed at, and passes any other base through untouched.
//
// It resolves HERE rather than in vcs.Resolve's precedence chain because the answer does
// not come from the VCS at all - it comes from the run history, which the vcs package
// must not learn about. Affected is the single funnel every affected computation goes
// through (ExpandAffected and Plan both call it), so one resolution covers the CLI, the
// shard planner, and magus\affected in a magusfile.
//
// The fallback when nothing was recorded is the PARENT of HEAD, announced. It is not the
// configured default: on the branch that builds itself the default base is that same
// branch, so falling back to it compares a commit against itself, reports nothing
// affected, and runs nothing - a gate that passes having checked nothing. Diffing one
// commit is the wrong answer only when a preceding run failed, which is loud here rather
// than silent.
func (m *Magus) resolveLastPassed(ctx context.Context, base string) (string, error) {
	// Resolve FIRST and test the winner, rather than testing the argument. The sentinel
	// can arrive from --base, MAGUS_VCS_BASE_REF, magus.yaml, or a per-VCS env var, and
	// vcs.Resolve is what knows the precedence between them. Asking it who won means the
	// sentinel works from every source without this function restating that order - and
	// without a caller who set the env var getting `git diff last-passed`.
	res, err := vcs.Resolve(ctx, m.ws.Root, base, m.ws.VCSOptions)
	if err != nil {
		return "", err
	}
	if res.Base != BaseLastPassed {
		return base, nil
	}
	// Refused, not silently downgraded. With the run log off there is nothing to read,
	// and every fallback available here is a base that gates less than the caller asked
	// for - which is the failure this base ref exists to prevent. Naming the switch that
	// caused it beats a warning nobody reads under a green check.
	if !m.cfg.CI.RecordRuns {
		return "", fmt.Errorf("base %q needs the run log, and ci.record_runs is off; set it true or pass an explicit --base", BaseLastPassed)
	}
	if res.VCS == nil || res.Source == types.VCSSourceDisabled {
		return "", fmt.Errorf("base %q needs a VCS to resolve against, and none is active", BaseLastPassed)
	}
	meta, err := res.VCS.Metadata(ctx, m.ws.Root)
	if err != nil {
		return "", fmt.Errorf("base %q: read %s metadata: %w", BaseLastPassed, res.Name, err)
	}

	var hist forecast.History
	if err := hist.Load(ctx, m.cfg.HistoryPath); err != nil {
		return "", fmt.Errorf("base %q: %w", BaseLastPassed, err)
	}
	if commit, ok := hist.PassedCommit(meta.Ref, ""); ok {
		slog.DebugContext(ctx, "affected: base resolved from run history",
			slog.String("ref", meta.Ref), slog.String("commit", commit))
		return commit, nil
	}

	parent := res.VCS.ParentRef()
	slog.WarnContext(ctx, "affected: no passing run recorded for this ref; diffing its parent commit instead",
		slog.String("ref", meta.Ref),
		slog.String("base", parent),
		slog.String("history_path", m.cfg.HistoryPath),
		slog.String("consequence", "anything merged by a run that did not pass is NOT in this diff"))
	return parent, nil
}

// AffectedFromPaths computes the affected set from an explicit file list.
func (m *Magus) AffectedFromPaths(ctx context.Context, paths []string) (*types.AffectedResult, error) {
	r, err := project.AffectedFromPaths(ctx, m.ws, paths)
	if err != nil {
		return nil, err
	}
	noteUndeclaredSeeds(r)
	return r, nil
}

func (m *Magus) limiter() *cache.Limiter {
	m.limOnce.Do(func() {
		n := m.cfg.Concurrency
		if n <= 0 {
			n = cache.DefaultConcurrency()
		}
		// Announced, never silent: a run quietly narrower than asked for is as hard to
		// attribute as one that thrashes. Said once, at the moment it takes effect.
		if clamped, was := cache.ClampConcurrency(n); was {
			slog.WarnContext(context.Background(), "magus: concurrency capped to this machine",
				slog.Int("requested", n), slog.Int("running_with", clamped),
				slog.Int("cpus", cache.MachineCeiling()))
			n = clamped
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

// Close releases workspace resources (VM pools, telemetry); cache and limiter are
// caller-owned. A provider built by Open is shut down here so its spans/metrics
// flush rather than being lost on exit. An injected provider (WithProvider) is
// left running - it is shared across every workspace the daemon holds (and the
// bridge Magus), so one workspace's eviction must not stop telemetry for the
// rest; the daemon itself owns and shuts down that provider.
func (m *Magus) Close() error {
	var errs []error
	if m.tel != nil && m.injectedTel == nil {
		if err := m.tel.Shutdown(context.Background()); err != nil {
			errs = append(errs, fmt.Errorf("magus: shut down telemetry provider: %w", err))
		}
	}
	if m.buzzPoolReg != nil {
		if err := m.buzzPoolReg.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *Magus) volatilityConfig() volatility.Config {
	return volatility.Config{
		Enabled:          m.cfg.Volatility.Enabled,
		BootstrapSamples: m.cfg.Volatility.BootstrapSamples,
		MinSamples:       m.cfg.Volatility.MinSamples,
		Threshold:        m.cfg.Volatility.Threshold,
		Annotate:         m.cfg.Volatility.AnnotateGHA,
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
	// Union the non-source dirs every resolved spell declares (vendor, node_modules,
	// __pycache__, ...) so the source walk prunes them per-project instead of the cache
	// hardcoding language-specific names. Unioning ALL the project's spells (not just
	// one target's) keeps a polyglot project from descending into a sibling ecosystem's
	// build tree. A name project.IgnoreDirs already covers adds nothing here; the union
	// earns its keep for spells beyond the core defaults, including local ones.
	var ignoreDirs []string
	for _, sp := range p.ResolvedSpells {
		for _, d := range sp.IgnoreDirs() {
			if !slices.Contains(ignoreDirs, d) {
				ignoreDirs = append(ignoreDirs, d)
			}
		}
	}
	return cache.Step{
		ProjectPath:     p.Path,
		Sources:         sources,
		IgnoreDirs:      ignoreDirs,
		Outputs:         outputs,
		WorkspaceRoot:   m.ws.Root,
		SpellDefVersion: spellruntime.BuiltinsHash(),
		Label:           types.ProjectDisplayName(p.Path, p.Name, p.Dir),
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
		if hint := m.suggestProjectPath(path); hint != "" {
			return nil, fmt.Errorf("magus: expand: %w: %q; did you mean %q?", types.ErrUnknownProject, path, hint)
		}
		return nil, fmt.Errorf("magus: expand: %w: %q", types.ErrUnknownProject, path)
	}
	return []types.Target{{Path: path, Name: t.Name}}, nil
}

// suggestProjectPath returns the workspace project path closest to a typo'd path,
// or "" when nothing is near. It mirrors the did-you-mean the CLI gives for
// unknown subcommands and describe nouns, so a fat-fingered `magus run test aip`
// points at "api" instead of a bare "unknown project".
func (m *Magus) suggestProjectPath(path string) string {
	all := m.All()
	candidates := make([]string, 0, len(all))
	for _, p := range all {
		candidates = append(candidates, p.Path)
	}
	return interactive.SuggestNearest(path, candidates)
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

// magusfileOverride returns the index of the magusfile spell when this project's
// magusfile exports target AND some other spell also provides it, else -1. Only a
// genuine collision overrides: a magusfile target no spell claims needs no precedence,
// and neither does a spell op the magusfile never mentions.
func magusfileOverride(p *types.Project, resolved []*spells.Spell, target string) int {
	if !slices.Contains(p.MagusfileTargets, target) {
		return -1
	}
	idx, contested := -1, false
	for i, s := range resolved {
		if s.Name() == types.MagusfileSpellName {
			idx = i
			continue
		}
		if slices.Contains(s.Targets(), target) {
			contested = true
		}
	}
	if idx >= 0 && contested {
		return idx
	}
	return -1
}

// forEachSpell runs fn against every spell on p. Spells run in parallel unless
// p.Exclusive is set; all run to completion so one failure does not mask others.
// When the context carries a [cache.Limiter] and the caller holds a slot, the
// parallel branch yields the slot and each spell acquires its own, keeping total
// concurrent spells bounded by the workspace concurrency cap.
func forEachSpell(ctx context.Context, p *types.Project, target string, fn func(context.Context, *spells.Spell) error) error {
	resolved := p.ResolvedSpells
	if len(resolved) == 0 {
		return nil
	}
	// A magusfile target SHADOWS a spell op of the same name and runs alone. Without
	// this both ran: this repo exports go_build while the go spell provides an op
	// normalizing to the same name, so `magus run go-build` compiled the module twice -
	// once bare from the spell, once stamped from the magusfile - and the bare one was
	// waste, built with no -o, no ldflags, no trimpath. The magusfile is the workspace's
	// own definition, so it decides what the name means there.
	if only := magusfileOverride(p, resolved, target); only >= 0 {
		if err := fn(ctx, resolved[only]); err != nil {
			return spellErr(p, target, types.SpellFailure{Spell: resolved[only].Name(), Err: err})
		}
		return nil
	}
	if len(resolved) == 1 {
		if err := fn(ctx, resolved[0]); err != nil {
			return spellErr(p, target, types.SpellFailure{Spell: resolved[0].Name(), Err: err})
		}
		return nil
	}
	if p.Exclusive {
		var failed []types.SpellFailure
		for _, s := range resolved {
			if err := fn(ctx, s); err != nil {
				failed = append(failed, types.SpellFailure{Spell: s.Name(), Err: err})
			}
		}
		if len(failed) == 0 {
			return nil
		}
		return spellErr(p, target, failed...)
	}

	lim := cache.LimiterFromContext(ctx)
	slotHeld := lim != nil && cache.SlotHeld(ctx)
	bounded := lim != nil

	type result struct {
		name string
		err  error
	}
	results := make([]result, len(resolved))
	var wg sync.WaitGroup

	fanOut := func() {
		for i, s := range resolved {
			wg.Add(1)
			go func(i int, s *spells.Spell) {
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
				results[i] = result{name: s.Name(), err: fn(spellCtx, s)}
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

	var failed []types.SpellFailure
	for _, r := range results {
		if r.err != nil {
			failed = append(failed, types.SpellFailure{Spell: r.name, Err: r.err})
		}
	}
	if len(failed) == 0 {
		return nil
	}
	return spellErr(p, target, failed...)
}

// spellErr builds the aggregate error for a target's spell failures. It exists so
// every construction site carries the project's DISPLAY label alongside its path:
// the path of a root project is ".", and a message built from it read "magus lint
// .:", where the dot lands as punctuation rather than as the project it names.
func spellErr(p *types.Project, target string, failed ...types.SpellFailure) *types.SpellErrors {
	return &types.SpellErrors{
		Project:      p.Path,
		ProjectLabel: types.ProjectDisplayName(p.Path, p.Name, p.Dir),
		Target:       target,
		Failed:       failed,
	}
}

// forSpellNamed is like forEachSpell but targets only the spell whose Name
// equals name. If no matching spell is registered the call is a no-op.
func forSpellNamed(ctx context.Context, p *types.Project, target, name string, fn func(context.Context, *spells.Spell) error) error {
	for _, s := range p.ResolvedSpells {
		if s.Name() != name {
			continue
		}
		if err := fn(ctx, s); err != nil {
			return spellErr(p, target, types.SpellFailure{Spell: s.Name(), Err: err})
		}
		return nil
	}
	return nil
}

// ContextWithSecrets installs this workspace's secret resolver on ctx, so a caller
// outside the run path can redact against the credentials this workspace has resolved.
//
// It hands out a CONTEXT, not the resolver. The daemon needs redaction on its serving
// paths - internal/trail writes MCP request and response payloads verbatim, and those are
// the largest credential-shaped thing magus persists - but nothing outside this package
// needs to read or mutate the resolver to get it.
//
// The resolver is per workspace Open, so this is only meaningful for a caller already
// bound to one workspace. A daemon-wide action has no workspace and therefore nothing to
// redact against, which is the honest answer rather than a gap.
func (m *Magus) ContextWithSecrets(ctx context.Context) context.Context {
	if m == nil || m.resolver == nil {
		return ctx
	}
	return secret.ContextWithResolver(ctx, m.resolver)
}
