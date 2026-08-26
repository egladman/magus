package magus

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
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
	"github.com/egladman/magus/internal/graph/knowledge"
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
	"github.com/egladman/magus/project/impact"
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

// DiffTUIEnabled reports whether `magus diff` may open its viewer, per workspace config.
//
// One question rather than an exported Config accessor: the caller needs this answer, not the
// whole configuration, and a broad getter is how a command ends up branching on settings that
// were never meant to reach it.
func (m *Magus) DiffTUIEnabled() bool { return m.cfg.Diff.TuiEnabled() }

// WorkingDiff returns the working tree's uncommitted changes as the backend's own unified
// diff, scoped to paths when non-empty and repository-wide otherwise. Empty when the tree
// is clean.
//
// It is the SELF-REVIEW half of the review surface: what you are about to commit, before
// any provider is involved. The committed-range half (base..head, a pull request) is a
// different question and deliberately not folded in here - a range diff has to name two
// revisions, and answering both through one signature would make the common case carry
// arguments it never uses.
//
// Every backend already implements DirtyDiff, so this is VCS-agnostic without a per-backend
// branch. The bytes are NOT identical across backends and are not meant to be: git, hg, and
// jj each emit their native diff header, and a wrapper that reconciled them would be lying
// about what ran. A reader parses the unified body, which they do share.
//
// A workspace with no VCS is not an error - it is a clean tree with nothing to review - so
// an unresolvable backend yields "" rather than failing the caller.
func (m *Magus) WorkingDiff(ctx context.Context, paths []string) (string, error) {
	res, err := vcs.Resolve(ctx, m.ws.Root, "", m.ws.VCSOptions)
	if err != nil || res.VCS == nil {
		//nolint:nilerr // a workspace with no VCS has nothing to review, which is a clean
		// tree rather than a failure; erroring would make the review surface unopenable in
		// exactly the workspaces where it has least to say.
		return "", nil
	}
	tracked, err := res.VCS.DirtyDiff(ctx, m.ws.Root, paths)
	if err != nil {
		return "", err
	}
	// A diff of tracked changes MISSES a brand-new file entirely, and a new file is the thing
	// a reviewer most wants to see. Every backend's dirty-diff is tree-against-index by
	// design - that is what a drift gate needs, and DirtyDiff must keep meaning exactly that -
	// so the untracked half is composed here rather than by widening a contract other callers
	// depend on.
	untracked, uerr := m.untrackedPatch(ctx, res.VCS, paths)
	if uerr != nil || untracked == "" {
		//nolint:nilerr // the tracked half is still worth showing: a permission error on one
		// scratch file must not make the whole changeset unreadable.
		return tracked, nil
	}
	if tracked == "" {
		return untracked, nil
	}
	// The newline between the halves is load-bearing. A patch whose last line is not
	// newline-terminated - which is exactly what a diff ending in "\ No newline at end of
	// file" produces - would otherwise have the first synthesized header glued onto it, so
	// that header stops starting a line, every reader misses it, and the first untracked file
	// silently disappears from the review while the rest show up fine. Measured: it ate
	// exactly one new file and nothing reported an error.
	if !strings.HasSuffix(tracked, "\n") {
		return tracked + "\n" + untracked, nil
	}
	return tracked + untracked, nil
}

// BranchChanges reports what other remote-tracking branches are changing, so a reader can be told
// that a file in front of them is also being edited elsewhere.
//
// Empty rather than an error whenever the answer cannot be had - no VCS, a backend without the
// capability, a repository with no other branches. The three are the same to the reader, and a
// surface that has nothing to say about competition should say nothing. That is also why a
// backend lacking BranchChangeReporter must not be reported as "nothing competes": those are
// different facts, and the caller can only tell them apart by getting nothing at all here.
//
// Read through the optional capability rather than by shelling a git command, for the reason
// ReviewOrigin reads the remote that way: the backend is asked, never its name.
func (m *Magus) BranchChanges(ctx context.Context, limit int) ([]types.BranchChange, error) {
	res, err := vcs.Resolve(ctx, m.ws.Root, "", m.ws.VCSOptions)
	if err != nil {
		return nil, fmt.Errorf("resolving the version control backend: %w", err)
	}
	if res.VCS == nil {
		// Version control disabled for this workspace. There are genuinely no other branches, so
		// this is an empty answer rather than a gap - the distinction the error below exists for.
		return nil, nil
	}
	reporter, ok := res.VCS.(types.BranchChangeReporter)
	if !ok {
		// NAMED, not swallowed. A backend that cannot answer and a repository where nothing
		// competes are different facts, and a surface shown the same emptiness for both tells
		// the reader "nothing competes" - reassurance magus has not earned. The caller reports
		// which backend fell short so the gap is legible rather than invisible.
		// Coded, so a surface can render the gap as a gap rather than as an empty answer, and
		// so the reader has a page explaining why an empty list here would have been a lie.
		// Still wraps ErrVCSUnsupported: callers match the sentinel, not the prose.
		return nil, fmt.Errorf("%w: %w",
			types.DiagnosticErrorf(types.VCSCapabilityMissing, "%s does not report branch changes", res.Name),
			types.ErrVCSUnsupported)
	}
	out, err := reporter.BranchChanges(ctx, m.ws.Root, res.Base, limit)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ReviewOrigin reports the branch this tree is on and the remote it would be pushed to, for a
// caller asking a provider which review is open.
//
// Never an error. A workspace with no VCS, a backend that cannot name a remote, a detached
// HEAD: all of them yield an empty field, and every one is an ordinary state of a tree rather
// than a failure. The caller's next question - "is a review open?" - has the same answer for
// all of them, so making this fail would only move a branch nobody needs up a layer.
//
// The remote is read through the optional RemoteReporter capability rather than by shelling a
// git command, so it works on every backend that implements one and degrades to empty on the
// ones that do not.
func (m *Magus) ReviewOrigin(ctx context.Context) types.ReviewOrigin {
	res, err := vcs.Resolve(ctx, m.ws.Root, "", m.ws.VCSOptions)
	if err != nil || res.VCS == nil {
		return types.ReviewOrigin{}
	}
	var out types.ReviewOrigin
	if meta, err := res.VCS.Metadata(ctx, m.ws.Root); err == nil {
		out.Branch = meta.Ref
	}
	if reporter, ok := res.VCS.(types.RemoteReporter); ok {
		if remote, err := reporter.RemoteURL(ctx, m.ws.Root); err == nil {
			out.Remote = remote
		}
	}
	return out
}

// untrackedPatch synthesizes a unified patch for files the VCS does not track yet: every line
// is an addition against /dev/null, which is exactly how git renders a new file.
//
// Untracked paths are derived from two capabilities the backends already expose rather than
// by parsing status output - DirtyFiles lists everything dirty, TrackedFiles says which of
// those the VCS knows - so this stays backend-agnostic instead of learning git's porcelain
// column format. A backend implementing neither yields no untracked half, which is the honest
// degradation.
func (m *Magus) untrackedPatch(ctx context.Context, driver types.VCSDriver, paths []string) (string, error) {
	tracker, ok := driver.(types.TrackedFileReporter)
	if !ok {
		return "", nil
	}
	lines, err := driver.DirtyFiles(ctx, m.ws.Root, paths)
	if err != nil || len(lines) == 0 {
		return "", err
	}
	dirty := make([]string, 0, len(lines))
	for _, l := range lines {
		if p := statusLinePath(l); p != "" {
			dirty = append(dirty, p)
		}
	}
	if len(dirty) == 0 {
		return "", nil
	}
	known, err := tracker.TrackedFiles(ctx, m.ws.Root, dirty)
	if err != nil {
		return "", err
	}
	isTracked := make(map[string]bool, len(known))
	for _, p := range known {
		isTracked[p] = true
	}

	var b strings.Builder
	for _, p := range dirty {
		if isTracked[p] {
			continue
		}
		body, rerr := os.ReadFile(filepath.Join(m.ws.Root, p))
		if rerr != nil {
			continue // unreadable or already gone; see the note in WorkingDiff
		}
		if bytes.IndexByte(body, 0) >= 0 {
			// A binary file gets git's own marker rather than a wall of mojibake.
			fmt.Fprintf(&b, "diff --git a/%s b/%s\nnew file mode 100644\nBinary files /dev/null and b/%s differ\n", p, p, p)
			continue
		}
		content := strings.TrimSuffix(string(body), "\n")
		rows := strings.Split(content, "\n")
		fmt.Fprintf(&b, "diff --git a/%s b/%s\nnew file mode 100644\n--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", p, p, p, len(rows))
		for _, r := range rows {
			b.WriteString("+" + r + "\n")
		}
	}
	return b.String(), nil
}

// statusLinePath strips a backend's status columns off one dirty-file line.
//
// Every backend prints "<status> <path>" with the status first and no spaces in it (git
// porcelain "?? a/b.go", hg "? a/b.go", jj "A a/b.go"), so splitting on the last run of
// leading non-space plus space recovers the path without knowing which backend wrote it. A
// rename arrow ("R old -> new") keeps the NEW name, which is the file that exists on disk.
func statusLinePath(line string) string {
	s := strings.TrimSpace(line)
	if s == "" {
		return ""
	}
	if i := strings.Index(s, " -> "); i >= 0 {
		return strings.TrimSpace(s[i+4:])
	}
	i := strings.IndexByte(s, ' ')
	if i < 0 {
		return s // no status column at all; the whole line is the path
	}
	return strings.TrimSpace(s[i+1:])
}

// Diff annotates a changed-path set with what the workspace already knows about each file:
// whether it is generated, which project owns it, how widely its changed symbols are
// referenced, and what coverage was observed on it.
//
// It is a JOIN, not a computation. Every input already exists - ClassifyFiles reads the same
// declared globs `magus describe file` reads, and impact.Compute/Enrich are what `magus
// affected --impact` prints. Assembling them here rather than in the console keeps one
// definition of review order (types.Diff.SortForReading), so a Buzz advisor writing a
// pull-request comment ranks files the same way the console scrolls them.
//
// EVERY overlay is best-effort and degrades to a Note rather than an error. A workspace with
// no symbol index still gets roles and ownership, which is most of the value; failing the
// whole review because coverage was never run would make the useful part unreachable. A
// reader must be able to tell "nothing depends on this" from "nothing was measured", which is
// what Notes is for.
func (m *Magus) Diff(ctx context.Context, paths []string) (types.Diff, error) {
	out := types.Diff{Base: "working"}

	entries, err := m.ClassifyFiles(ctx, paths)
	if err != nil {
		return types.Diff{}, err
	}
	byPath := make(map[string]*types.DiffFile, len(entries))
	out.Files = make([]types.DiffFile, 0, len(entries))
	for _, e := range entries {
		out.Files = append(out.Files, types.DiffFile{
			Path: e.Path, Project: e.Project, Role: e.Role, Hint: e.Hint,
		})
	}
	for i := range out.Files {
		byPath[out.Files[i].Path] = &out.Files[i]
	}

	// The blast radius and the symbol/coverage overlays. Computed from the SAME path set
	// rather than from a fresh VCS diff, so the annotations describe exactly the files the
	// caller is reviewing - re-diffing here would race an edit made since the patch was read
	// and annotate a file the reader cannot see.
	res, ierr := impact.ComputeFromPaths(ctx, m, paths)
	if ierr != nil {
		// Every overlay is best-effort and degrades to a Note. Roles and ownership are most
		// of the value and are already computed; failing the whole review because a blast
		// radius could not be walked would make the useful part unreachable, and the Note is
		// what keeps the absence visible rather than silent.
		out.Notes = append(out.Notes, "blast radius unavailable: "+ierr.Error())
		out.SortForReading()
		return out, nil //nolint:nilerr // reported as a Note, see above
	}
	graph, gerr := m.KnowledgeGraphWithSymbols(ctx)
	// indexed is the real question, and it is NOT "did a graph load". A graph loads fine with
	// no symbol shards in it, so gating on a non-nil graph reports every file's reach as a
	// measured zero on exactly the workspaces that have no index - which is the collapse the
	// nil is there to prevent. HasSymbols is the same predicate impact.Enrich gates its own
	// overlays on, so the ranking and the overlays can never disagree about whether anyone
	// looked.
	indexed := false
	if gerr == nil {
		store := impact.GraphStore(graph)
		indexed = store.HasSymbols()
		impact.Enrich(res, store)
	} else {
		out.Notes = append(out.Notes,
			"changed-symbol reach and coverage skipped (no symbol index loaded): "+gerr.Error())
	}
	out.Notes = append(out.Notes, res.Notes...)
	// SeedProjects is documented as "the ones the author actually edited", so a project whose
	// only changed file is a declared output does not belong in it - the whole premise of the
	// fold is that a regenerated file is a machine's restatement, not an edit. Counting it
	// has one line folding files and un-folding projects in the same breath: measured, a
	// background regeneration moves "3 projects edited" to 6 while the read list stays
	// byte-identical.
	//
	// AffectedProjects deliberately keeps the FULL closure over every changed path, generated
	// included, because a regenerated output really does invalidate a downstream cache key.
	// The two numbers answer different questions - who wrote something, and what has to run -
	// so they must not be conflated.
	out.SeedProjects = authorEditedProjects(res.SeedProjects, out.Files)
	out.AffectedProjects = res.AffectedProjects

	// Surface starts UNKNOWN everywhere and is only lowered to internal for a file the symbol
	// index actually covered. Defaulting to internal would report every unindexed file as
	// safe, which is the one wrong answer that costs something.
	for i := range out.Files {
		out.Files[i].Surface = types.DiffSurfaceUnknown
	}
	// Reach gets a measured baseline of zero ONLY when an index was loaded; otherwise it stays
	// nil and Review.Ranked() reports that there was no ranking key. The flag is workspace-wide
	// rather than per-file because that is the granularity magus actually knows: it can tell
	// whether an index loaded, not whether this particular path was in it.
	if indexed {
		for i := range out.Files {
			zero := 0 // one pointer PER FILE: a shared one would raise every file's reach at once
			out.Files[i].Reach = &zero
		}
	}
	for _, s := range res.ChangedSymbols {
		f, ok := byPath[s.File]
		if !ok {
			continue
		}
		sym := types.DiffSymbol{ID: s.Symbol, Label: s.Label, RefCount: s.RefCount, FileCount: s.FileCount}
		sym.ModuleAPI = exportedFromModule(s.File, s.Label)
		if graph != nil {
			sym.ExternalProjects, sym.ExternalFileCount = m.externalReferents(graph, s.Symbol, f.Project)
		}
		// Drop the locals. SCIP indexes every binding, so a changed function contributes its
		// parameters and temporaries - `signal0`, `headers1`, `body0` - and on a real file they
		// were roughly two thirds of the payload this surface serves to every MCP client. A
		// symbol that nothing references and that leaves neither the project nor the module
		// cannot change how anyone reads the diff, so carrying it costs an agent's context and
		// buys nothing.
		//
		// The exports are kept even at zero references, and that is the whole reason this is a
		// conjunction rather than `RefCount == 0`: a NEWLY ADDED public function has no
		// referents yet and is precisely the thing a reviewer must see.
		//
		// Only the APPEND is skipped. The reach and surface updates below still run for a
		// local, because a file whose changed symbols are all locals was still COVERED by the
		// index - and reporting its surface as unknown would say nobody looked when somebody
		// did.
		if sym.RefCount > 0 || sym.FileCount > 0 || sym.ModuleAPI || len(sym.ExternalProjects) > 0 {
			f.Symbols = append(f.Symbols, sym)
		}
		// Reach is the WIDEST file count among the file's changed symbols, not their sum: a
		// file is as dangerous as its most-depended-on export, and summing would rank a file
		// with many narrow symbols above one with a single load-bearing API.
		if s.FileCount > f.ReachOr(-1) {
			n := s.FileCount // a fresh pointer, never a write through the shared baseline
			f.Reach = &n
		}
		// One symbol crossing a project boundary makes the whole file public surface: a
		// reviewer needs to know the file contains something a consumer can see, and burying
		// that because its neighbors are internal is how the signal gets missed.
		switch {
		case len(sym.ExternalProjects) > 0 || sym.ModuleAPI:
			f.Surface = types.DiffSurfacePublic
		case f.Surface == types.DiffSurfaceUnknown:
			f.Surface = types.DiffSurfaceInternal
		}
	}
	for _, c := range res.ChangedFileCoverage {
		if f, ok := byPath[c.File]; ok {
			cov := c.Coverage
			f.Coverage = &cov
		}
	}

	out.SortForReading()
	return out, nil
}

// authorEditedProjects narrows a seed set to the projects a PERSON changed something in.
//
// A seed whose every changed file is a declared output was not edited; a target rewrote it.
// Keeping it made "N projects edited" count projects with nothing to read, which is
// indefensible in the same sentence that folds those files away. A seed with no files at all
// in the review is kept: that means the review does not know the file's project rather than
// that the project was untouched, and dropping it would silently shrink the count on the
// strength of an absence.
func authorEditedProjects(seeds []string, files []types.DiffFile) []string {
	authored := make(map[string]bool, len(seeds))
	known := make(map[string]bool, len(seeds))
	for _, f := range files {
		if f.Project == "" {
			continue
		}
		known[f.Project] = true
		if !f.Generated() {
			authored[f.Project] = true
		}
	}
	out := make([]string, 0, len(seeds))
	for _, s := range seeds {
		if authored[s] || !known[s] {
			out = append(out, s)
		}
	}
	return out
}

// exportedFromModule reports whether a symbol defined at path with the given label is
// reachable from OUTSIDE the module - the boundary a semver bump is actually about.
//
// It is deliberately per-language and deliberately narrow. Go is the only language answered
// here because Go states export in the language itself (an initial capital) and states
// unreachability in the path (an `internal/` segment the toolchain enforces), so the answer
// is a fact rather than a heuristic. Every other language returns false, which reads as "not
// known to be module API" and never as "internal" - the caller keeps ExternalProjects, which
// is language-neutral, and the surface stays honest about what was not checked.
//
// Adding a language here needs the same standard: a rule the toolchain ENFORCES, not a
// convention it merely encourages. TypeScript's `export` keyword does not qualify, because
// what a package actually publishes is decided by its entry points and its `exports` map,
// which this signature cannot see.
func exportedFromModule(path, label string) bool {
	if !strings.HasSuffix(path, ".go") || label == "" {
		return false
	}
	// `internal/` anywhere in the path makes the package unimportable outside the module,
	// whatever the symbol's case. Checked on segments so a directory merely CONTAINING the
	// word (say "internals/") is not mistaken for the enforced one.
	for _, seg := range strings.Split(path, "/") {
		if seg == "internal" {
			return false
		}
	}
	// A test file exports nothing a consumer can import.
	if strings.HasSuffix(path, "_test.go") {
		return false
	}
	r := rune(label[0])
	return r >= 'A' && r <= 'Z'
}

// externalReferents reports which OTHER projects reference symbolID, and how many of its
// referencing files sit outside owner. It is the semver-relevant half of the review: a symbol
// used only inside its own project cannot break a consumer the workspace does not also
// rebuild, and one used across a boundary can.
//
// Ownership is by directory containment, longest project path first, which is the same rule
// ClassifyFiles uses - so a file in a nested project is attributed to the nested one rather
// than to the root, and a nested project consuming its parent's symbol reads as external.
//
// A file owned by nothing is NOT counted as external. It affects no target and rebuilds
// nothing, so calling it a downstream consumer would inflate the surface with paths that
// cannot break.
func (m *Magus) externalReferents(g *knowledge.Graph, symbolID, owner string) ([]string, int) {
	refs, ok := g.Refs(symbolID)
	if !ok {
		return nil, 0
	}
	owners := slices.Clone(m.ws.All())
	slices.SortFunc(owners, func(a, b *types.Project) int {
		if c := cmp.Compare(len(b.Path), len(a.Path)); c != 0 {
			return c
		}
		return cmp.Compare(a.Path, b.Path)
	})
	projectOf := func(path string) string {
		for _, p := range owners {
			if p.Path == "." || strings.HasPrefix(path, p.Path+"/") {
				if p.Path != "." {
					return p.Path
				}
				return "."
			}
		}
		return ""
	}

	seen := map[string]bool{}
	external := 0
	for _, site := range refs.Refs {
		proj := projectOf(site.File)
		if proj == "" || proj == owner {
			continue
		}
		external++
		seen[proj] = true
	}
	projects := make([]string, 0, len(seen))
	for p := range seen {
		projects = append(projects, p)
	}
	slices.Sort(projects)
	return projects, external
}

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
//
// Files that seeded a project by directory containment while it declares none of them
// come back on [types.AffectedResult.UndeclaredBySeed]; reporting them (MGS1028) is the
// caller's, because this is a library call and its caller owns whatever stream a person
// is reading. The CLI reports it in cmd/magus/affected.go.
func (m *Magus) Affected(ctx context.Context, base string) (*types.AffectedResult, error) {
	base, err := m.resolveLastPassed(ctx, base)
	if err != nil {
		return nil, err
	}
	return project.Affected(ctx, m.ws, base)
}

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

// AffectedFromPaths computes the affected set from an explicit file list. Undeclared
// seeding files ride [types.AffectedResult.UndeclaredBySeed], the same as [Magus.Affected];
// see it for who reports them.
func (m *Magus) AffectedFromPaths(ctx context.Context, paths []string) (*types.AffectedResult, error) {
	return project.AffectedFromPaths(ctx, m.ws, paths)
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

// joinGlob roots a project-relative glob at the workspace for the cache step and the
// describe surfaces. It is a named pass-through on purpose: the call sites in this
// package read as "join", and the rooting rule itself belongs in types, where
// Project.DeclaredGlobs - the attribution mirror of these very lines - can share it.
// See types.RootGlob for why the join is cleaned rather than concatenated.
func joinGlob(projectPath, glob string) string {
	return types.RootGlob(projectPath, glob)
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
