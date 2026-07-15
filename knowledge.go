package magus

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/host"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/knowledge"
	"github.com/egladman/magus/internal/symbols"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
	"golang.org/x/mod/modfile"
)

// BuildGlobalKnowledgeGraph unions the current workspace with each registered one
// (cfg.Knowledge.Workspaces), namespacing node IDs by workspace so repos can't
// collide. A workspace that fails to open is skipped with a warning, not fatal:
// the query degrades to what it can reach.
func BuildGlobalKnowledgeGraph(ctx context.Context, ws types.WorkspaceRepository, cfg config.Config, refresh bool, log *slog.Logger) (*knowledge.Graph, error) {
	if log == nil {
		log = slog.Default()
	}
	root := ws.Root()
	merged := knowledge.NewGraph()

	cur, err := BuildKnowledgeGraph(ctx, ws, root, cfg, refresh, log)
	if err != nil {
		return nil, err
	}
	knowledge.UnionInto(merged, knowledge.Qualified(cur, workspaceName(root)))

	seen := map[string]bool{cleanRoot(root): true}
	for _, wr := range cfg.Knowledge.Workspaces {
		abs := cleanRoot(wr)
		if abs == "" || seen[abs] {
			continue // skip blanks and the current workspace re-listed
		}
		seen[abs] = true
		g, err := buildRegisteredWorkspace(ctx, abs, refresh, log)
		if err != nil {
			log.Warn("magus: skipping registered workspace in global graph", slog.String("workspace", wr), slog.String("error", err.Error()))
			continue
		}
		knowledge.UnionInto(merged, knowledge.Qualified(g, workspaceName(abs)))
	}
	return merged, nil
}

// buildRegisteredWorkspace opens a registered workspace read-only, loads its own
// config (its cache dir, immutability, etc.), and builds its graph cache-first.
func buildRegisteredWorkspace(ctx context.Context, root string, refresh bool, log *slog.Logger) (*knowledge.Graph, error) {
	wcfg, err := config.LoadWithRoot("", root)
	if err != nil {
		return nil, err
	}
	wsRepo, err := Inspect(ctx, root)
	if err != nil {
		return nil, err
	}
	return BuildKnowledgeGraph(ctx, wsRepo, root, wcfg, refresh, log)
}

// workspaceName is the qualifier for a workspace root: its basename. Collisions
// (two repos with the same directory name) merge in the union view, which is
// acceptable - the alternative (full paths) makes node IDs unreadable.
func workspaceName(root string) string {
	return filepath.Base(filepath.Clean(root))
}

// cleanRoot resolves root to an absolute, cleaned path for de-duplication.
func cleanRoot(root string) string {
	if abs, err := filepath.Abs(root); err == nil {
		return abs
	}
	return filepath.Clean(root)
}

// resolveCacheDir resolves the workspace cache directory: config Cache.Dir, then
// MAGUS_CACHE_DIR, then <root>/.magus (relative values join to root). Open and the
// knowledge-graph loader share this single implementation.
func resolveCacheDir(root string, cfg config.Config) string {
	if cfg.Cache.Dir != "" {
		if filepath.IsAbs(cfg.Cache.Dir) {
			return filepath.Clean(cfg.Cache.Dir)
		}
		return filepath.Join(root, cfg.Cache.Dir)
	}
	if override := os.Getenv("MAGUS_CACHE_DIR"); override != "" {
		if filepath.IsAbs(override) {
			return filepath.Clean(override)
		}
		return filepath.Join(root, override)
	}
	return filepath.Join(root, ".magus")
}

// cacheImmutable reports whether the cache is read-only, honoring both the config
// flag and the MAGUS_CACHE_IMMUTABLE env var (matching cache.Open's behavior).
func cacheImmutable(cfg config.Config) bool {
	if cfg.Cache.Immutable {
		return true
	}
	v := strings.ToLower(os.Getenv("MAGUS_CACHE_IMMUTABLE"))
	return v == "true" || v == "1"
}

// allModuleEntries returns every stdlib module with its methods populated. The
// summary view (empty name) carries only names, so each is re-queried for detail.
func allModuleEntries() []types.ModuleEntry {
	summary := host.ModulesOutput("")
	out := make([]types.ModuleEntry, 0, len(summary.Modules))
	for _, m := range summary.Modules {
		out = append(out, host.ModulesOutput(m.Name).Modules...)
	}
	return out
}

// BuildKnowledgeGraph assembles, persists, and returns the workspace knowledge
// graph. It is the single graph-loading path shared by the `magus graph`
// subcommands, the query/explain/path verbs, and the MCP tools: it gathers the describe
// outputs the graph is composed from, resolves the cache dir, and runs the
// cache-first build. ws is any workspace view that can describe itself (the
// read-only Inspect result or a full *Magus).
func BuildKnowledgeGraph(ctx context.Context, ws types.Describer, root string, cfg config.Config, refresh bool, log *slog.Logger) (*knowledge.Graph, error) {
	if log == nil {
		// The loaders below log best-effort; a nil logger (some callers, e.g. describe,
		// pass one) would panic on the first miss. Normalize once here.
		log = slog.Default()
	}
	cacheDir := resolveCacheDir(root, cfg)
	spells := ws.DescribeSpells()
	graph := ws.DescribeGraph()
	projects := ws.DescribeProjects()
	in := knowledge.Inputs{
		Graph:       graph,
		Spells:      spells,
		Modules:     allModuleEntries(),
		Diagnostics: types.AllDiagnosticCodes(),
		Root:        root,
		Runtime:     knowledge.LoadRuntimeEvents(cacheDir),
		Timings:     loadKnowledgeTimings(ctx, cfg),
		OutputRefs:  loadKnowledgeOutputRefs(cacheDir),
		Symbols: loadKnowledgeSymbols(symbolIngestInputs{
			cfg: cfg, root: root, cacheDir: cacheDir,
			projects: projects, spells: spells, log: log,
		}),
		VCS:            loadKnowledgeVCS(ctx, cfg, root, cacheDir, log),
		DeclaredSpells: declaredSpellSet(projects),
		Coverage:       loadKnowledgeCoverage(root),
	}
	return knowledge.Build(ctx, cacheDir, knowledge.BuildOptions{
		Immutable: cacheImmutable(cfg),
		Refresh:   refresh,
		MaxBytes:  int64(cfg.Knowledge.MaxSizeMB) * 1024 * 1024,
		Remote:    remoteShardsFor(ws),
	}, in, log)
}

// loadKnowledgeTimings reads the local timing history (best-effort) into per-target
// timing inputs for the @runtime shard. A disabled or unreadable history yields no
// timings, so the performance attrs are simply absent, never an error. The result
// is sorted so assembly stays deterministic regardless of history map order.
func loadKnowledgeTimings(ctx context.Context, cfg config.Config) []types.KnowledgeTiming {
	if cfg.HistoryPath == "" {
		return nil
	}
	var h forecast.History
	if err := h.Load(ctx, cfg.HistoryPath); err != nil {
		return nil
	}
	var out []types.KnowledgeTiming
	for project, targets := range h.Projects {
		for target, st := range targets {
			out = append(out, types.KnowledgeTiming{
				Project:        project,
				Target:         target,
				P75Ms:          st.P75Ms,
				Samples:        st.Samples,
				HitRate:        st.HitRate,
				HitRateSamples: st.HitCount + st.MissCount,
			})
		}
	}
	slices.SortFunc(out, func(a, b types.KnowledgeTiming) int {
		if c := cmp.Compare(a.Project, b.Project); c != 0 {
			return c
		}
		return cmp.Compare(a.Target, b.Target)
	})
	return out
}

// loadKnowledgeCoverage reads the local Go coverage profile (best-effort) into per-file
// coverage for the observed @coverage overlay. The profile is coverage.out at the
// workspace root - what `magus run coverage` writes - and its lines are module-qualified,
// so the module path from go.mod is stripped to recover the workspace-relative paths the
// file/symbol nodes use. A missing profile, an unreadable go.mod, or a profile with no
// data yields no coverage, so the attrs are simply absent, never an error: a workspace
// that never ran coverage behaves exactly as before. Re-read each build, mirroring the
// timing/output-ref overlays, so the ratio stays fresh without a schema bump.
func loadKnowledgeCoverage(root string) []knowledge.FileCoverage {
	if root == "" {
		return nil
	}
	profile, err := os.ReadFile(filepath.Join(root, "coverage.out"))
	if err != nil {
		return nil // no profile produced yet
	}
	gomod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil // without the module path the qualified profile paths cannot be rebased
	}
	module := modfile.ModulePath(gomod)
	if module == "" {
		return nil
	}
	return knowledge.ParseCoverage(profile, module)
}

// declaredSpellSet is the union of every project's declared `spells:` list - the spells
// this workspace opts into, as opposed to the compiled-in builtins that are merely
// available. It tags spell nodes so the orphan lens flags only a declared-but-unused
// spell (genuinely dead) and never a builtin no project here declares. Nil when empty.
func declaredSpellSet(projects types.ProjectsOutput) map[string]bool {
	set := map[string]bool{}
	for _, p := range projects.Projects {
		for _, name := range p.Spells {
			set[name] = true
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

// loadKnowledgeOutputRefs reads the local output store (best-effort) for each target's
// most recent captured-output reference, so the @runtime shard can fold last_output_ref
// and last_run_ok onto the target node. The forecast timing history is cache-safety-locked
// and records no refs, so the output store - which already persists one OutputDescriptor
// per execution - is the source. A missing or unreadable store yields no refs, so the
// attrs are simply absent, never an error. The store already sorts by project:target, so
// assembly stays deterministic.
func loadKnowledgeOutputRefs(cacheDir string) []types.KnowledgeOutputRef {
	descs := cache.NewOutputStore(cacheDir).LatestRefsByTarget()
	if len(descs) == 0 {
		return nil
	}
	out := make([]types.KnowledgeOutputRef, 0, len(descs))
	for _, d := range descs {
		out = append(out, types.KnowledgeOutputRef{
			Project: d.Project,
			Target:  d.Target,
			Ref:     d.Ref,
			OK:      !d.Failed,
		})
	}
	return out
}

// symbolStore opens the same store BuildKnowledgeGraph writes, so the symbol shards a
// build just persisted (and the derived xref routing index) are available for a
// lazy merge.
func symbolStore(ws types.Describer, root string, cfg config.Config, log *slog.Logger) *knowledge.Store {
	if log == nil {
		log = slog.Default()
	}
	return knowledge.NewStore(resolveCacheDir(root, cfg), cacheImmutable(cfg), int64(cfg.Knowledge.MaxSizeMB)*1024*1024, remoteShardsFor(ws), log)
}

// MergeWorkspaceSymbols pulls every persisted per-project @symbols shard into g, for
// a symbol-seeded query (the default graph excludes them for scale). Best-effort: no
// store or no symbol shards is a no-op.
func MergeWorkspaceSymbols(ctx context.Context, ws types.Describer, root string, cfg config.Config, g *knowledge.Graph, log *slog.Logger) error {
	return symbolStore(ws, root, cfg, log).MergeSymbolShards(ctx, g)
}

// MergeWorkspaceSymbolsForRef merges symbols into g for `magus refs`, targeting only
// the shards that mention ref (via the xref routing index) when ref is an exact symbol
// ID - the scale-safe reverse lookup - or all symbol shards when ref is a fuzzy name
// whose exact ID is not yet known.
func MergeWorkspaceSymbolsForRef(ctx context.Context, ws types.Describer, root string, cfg config.Config, g *knowledge.Graph, ref string, log *slog.Logger) error {
	store := symbolStore(ws, root, cfg, log)
	// An exact symbol ID can route to just its shards; a fuzzy name (or any non-exact
	// symbol: ref) yields no routing hit and MergeSymbolShardsByID falls back to loading
	// all, so the fuzzy resolve still has every symbol to match against.
	if strings.HasPrefix(ref, types.KindSymbol+":") {
		return store.MergeSymbolShardsByID(ctx, g, []string{ref})
	}
	return store.MergeSymbolShards(ctx, g)
}

// loadKnowledgeSymbols reads each project's SCIP index (best-effort) into per-project
// symbol records for the @symbols shards. Ingestion is AUTOMATIC: every project bound to
// a symbol-capable spell (one exposing the reserved `scip` op) is read from that
// project's cached index, so importing a language's spells is the only opt-in - no
// per-project config. The index lives under the cache dir, not the tree: `magus run
// <project>::scip` produces it there. An index that has not been built yet (its scip
// target has not run) or an unreadable/undecodable one is skipped with a debug log,
// never an error - symbol ingestion is optional enrichment, so a bad index degrades to
// "no symbols for that project" rather than failing every graph query.
func loadKnowledgeSymbols(in symbolIngestInputs) map[string][]types.KnowledgeSymbol {
	log := in.log
	decls := symbolIndexDeclarations(in)
	if len(decls) == 0 {
		return nil
	}
	out := map[string][]types.KnowledgeSymbol{}
	for _, decl := range decls {
		data, err := os.ReadFile(decl.path)
		if err != nil {
			// A not-yet-built index (the scip target has not run) is expected and quiet;
			// any other read error (permissions) is a misconfig worth surfacing.
			if errors.Is(err, fs.ErrNotExist) {
				log.Debug("knowledge: symbol index not built yet, skipping", slog.String("project", decl.project), slog.String("index", decl.path))
			} else {
				log.Warn("knowledge: cannot read symbol index", slog.String("project", decl.project), slog.String("index", decl.path), slog.String("error", err.Error()))
			}
			continue
		}
		syms, err := symbols.ParseIndex(data, decl.project)
		if err != nil {
			// An index that exists but will not decode is a real problem (corrupt output),
			// not a benign miss - surface it.
			log.Warn("knowledge: cannot decode symbol index", slog.String("project", decl.project), slog.String("index", decl.path), slog.String("error", err.Error()))
			continue
		}
		out[decl.project] = syms
	}
	return out
}

// resolvedSymbolIndex pairs a project with the absolute path of its SCIP index.
type resolvedSymbolIndex struct {
	project string
	path    string
}

// symbolIngestInputs is the shared context for resolving and reading symbol indexes,
// threaded as one value so loadKnowledgeSymbols and symbolIndexDeclarations cannot drift
// out of lockstep as the input set grows.
type symbolIngestInputs struct {
	cfg      config.Config
	root     string
	cacheDir string
	projects types.ProjectsOutput
	spells   types.SpellsOutput
	log      *slog.Logger
}

// symbolIndexDeclarations resolves which SCIP indexes to ingest, keyed by project so a
// derived entry and an explicit override for the same project cannot both fire. It
// derives one for every project bound to a symbol-capable spell (one exposing the
// reserved `scip` op), pointing at that project's cached index (symbols.IndexPath, the
// same location the op writes to) - the zero-config path. Explicit knowledge.symbols
// entries are then merged in and win on the same project, pointing instead at a
// workspace-relative path in the tree for a project whose indexer writes somewhere
// non-standard. The result is sorted by project for deterministic ingestion.
func symbolIndexDeclarations(in symbolIngestInputs) []resolvedSymbolIndex {
	capable := map[string]bool{}
	for _, sp := range in.spells.Spells {
		if slices.Contains(sp.Targets, symbols.IndexOp) {
			capable[sp.Name] = true
		}
	}

	byProject := map[string]resolvedSymbolIndex{}
	for _, p := range in.projects.Projects {
		bound := p.Spells
		if len(bound) == 0 && p.Spell != "" {
			bound = []string{p.Spell}
		}
		for _, name := range bound {
			if !capable[name] {
				continue
			}
			// One index per project: the cache location is keyed by the project dir, so
			// the first symbol-capable spell wins and the rest would name the same file.
			absDir := filepath.Join(in.root, filepath.FromSlash(p.Path))
			byProject[p.Path] = resolvedSymbolIndex{project: p.Path, path: symbols.IndexPath(in.cacheDir, absDir)}
			break
		}
	}
	for _, decl := range in.cfg.Knowledge.Symbols {
		if decl.Project == "" || decl.Index == "" {
			continue
		}
		// An explicit override names a path in the tree; reject one that escapes the
		// workspace rather than reading an arbitrary file.
		if !filepath.IsLocal(decl.Index) {
			in.log.Warn("knowledge: symbol index path escapes the workspace, skipping", slog.String("project", decl.Project), slog.String("index", decl.Index))
			continue
		}
		byProject[decl.Project] = resolvedSymbolIndex{project: decl.Project, path: filepath.Join(in.root, decl.Index)}
	}

	out := make([]resolvedSymbolIndex, 0, len(byProject))
	for _, d := range byProject {
		out = append(out, d)
	}
	slices.SortFunc(out, func(a, b resolvedSymbolIndex) int { return cmp.Compare(a.project, b.project) })
	return out
}

// vcsDefaultMaxCommits bounds the history walk when knowledge.vcs.max_commits is unset.
// It keeps the scan sub-second on a large repo; a file older than the window undercounts
// its commits but still reports the most recent commit correctly. When the workspace is
// a subdir of the VCS root, commits touching only out-of-subdir files still consume the
// budget, so the effective in-subdir window is smaller than the bound.
const vcsDefaultMaxCommits = 1000

// vcsInputCache is the sidecar that gates the churn scan: keyed by the current revision
// and the commit bound, the metadata is re-derived only when either moves, so query-time
// builds pay nothing on an unchanged tree.
type vcsInputCache struct {
	Head    string               `json:"head"`
	Max     int                  `json:"max"`
	Entries []types.KnowledgeVCS `json:"entries"`
}

// loadKnowledgeVCS gathers per-file history for the @vcs shard when opt-in
// (knowledge.vcs.enabled), routed through the VCS abstraction so it is not git-specific:
// any resolved backend that implements ChurnReporter works, and one that does not is
// skipped. Best-effort: a disabled/absent VCS or a scan error yields no metadata (the
// shard is simply absent), never an error. The walk is cached against the current
// revision and the commit bound in <cacheDir>/knowledge/vcs-inputs.json, so an unchanged
// commit reuses the prior result and the scan never runs on the query path.
func loadKnowledgeVCS(ctx context.Context, cfg config.Config, root, cacheDir string, log *slog.Logger) []types.KnowledgeVCS {
	if !cfg.Knowledge.VCS.Enabled {
		return nil
	}
	res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{})
	if err != nil || res.Source == types.VCSSourceDisabled || res.VCS == nil {
		log.Debug("knowledge: vcs enabled but no version control resolved, skipping")
		return nil
	}
	head, err := res.VCS.FindCommit(ctx, root, "") // "" = the current revision
	if err != nil || head.ID == "" {
		log.Debug("knowledge: cannot resolve current revision, skipping vcs")
		return nil
	}

	maxCommits := cfg.Knowledge.VCS.MaxCommits
	if maxCommits <= 0 {
		maxCommits = vcsDefaultMaxCommits
	}
	cachePath := filepath.Join(cacheDir, "knowledge", "vcs-inputs.json")
	if cached, ok := readVCSCache(cachePath); ok && cached.Head == head.ID && cached.Max == maxCommits {
		return cached.Entries
	}

	reporter, ok := res.VCS.(types.ChurnReporter)
	if !ok {
		log.Debug("knowledge: vcs backend cannot report per-commit files, skipping", slog.String("vcs", res.Name))
		return nil
	}
	changes, err := reporter.ChangesByCommit(ctx, root, maxCommits, "")
	if err != nil {
		log.Warn("knowledge: vcs history scan failed, skipping", slog.String("error", err.Error()))
		return nil
	}
	entries := aggregateFileHistory(changes, vcsPathPrefix(root, res.VCS.Claims()))
	writeVCSCache(cachePath, vcsInputCache{Head: head.ID, Max: maxCommits, Entries: entries}, log)
	return entries
}

// vcsPathPrefix returns the "<subdir>/" prefix ChangesByCommit paths carry when the
// workspace root is nested below the VCS root, so aggregateFileHistory can strip it to
// workspace-relative paths that match file-node Sources. It walks up from root for a VCS
// claim marker (rather than asking the driver for its root), so both paths share the same
// symlink representation and filepath.Rel stays clean - the driver's root can be
// canonicalized (e.g. /private/var vs /var on macOS) and would yield a bogus prefix.
// Empty when root is the VCS root (the common case) or no marker is found. Mirrors
// project.vcsRootPrefix (same walk-up-for-marker algorithm); keep the two in sync.
func vcsPathPrefix(root string, claims []string) string {
	for dir := root; ; {
		for _, c := range claims {
			if _, err := os.Stat(filepath.Join(dir, c)); err == nil {
				rel, err := filepath.Rel(dir, root)
				if err != nil || rel == "." {
					return ""
				}
				return filepath.ToSlash(rel) + "/"
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// aggregateFileHistory reduces per-commit changes (newest first) to per-file metadata:
// the first sighting of a file is its most recent commit; every sighting bumps the count.
// Paths are made workspace-relative by stripping prefix; a path outside the workspace
// subtree is dropped. Renames are not followed; a renamed file starts a fresh history.
func aggregateFileHistory(changes []types.CommitChange, prefix string) []types.KnowledgeVCS {
	type acc struct {
		lastCommit string
		lastUnix   int64
		commits    int
	}
	byPath := map[string]*acc{}
	var order []string
	for _, c := range changes {
		short := shortRevision(c.ID)
		unix := c.Date.Unix()
		for _, f := range c.Files {
			f = filepath.ToSlash(strings.TrimSpace(f))
			if prefix != "" {
				rel, ok := strings.CutPrefix(f, prefix)
				if !ok {
					continue // outside the workspace subtree
				}
				f = rel
			}
			if f == "" {
				continue
			}
			a := byPath[f]
			if a == nil {
				a = &acc{lastCommit: short, lastUnix: unix} // first sighting = most recent commit
				byPath[f] = a
				order = append(order, f)
			}
			a.commits++
		}
	}
	entries := make([]types.KnowledgeVCS, 0, len(order))
	for _, p := range order {
		a := byPath[p]
		entries = append(entries, types.KnowledgeVCS{Path: p, LastCommit: a.lastCommit, LastUnix: a.lastUnix, Commits: a.commits})
	}
	return entries
}

// shortRevision abbreviates a full revision id for display, leaving a short id untouched.
func shortRevision(id string) string {
	const short = 12
	if len(id) > short {
		return id[:short]
	}
	return id
}

// readVCSCache loads the scan cache, returning ok=false on any miss (absent or
// unreadable/corrupt) so the caller simply rescans.
func readVCSCache(path string) (vcsInputCache, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return vcsInputCache{}, false
	}
	var c vcsInputCache
	if err := json.Unmarshal(data, &c); err != nil {
		return vcsInputCache{}, false
	}
	return c, true
}

// writeVCSCache persists the scan result, best-effort: a write failure only costs a
// rescan next time, so it is logged at debug and never fatal. The write is atomic
// (temp + rename), matching the shard store, so a concurrent build never reads a torn file.
func writeVCSCache(path string, c vcsInputCache, log *slog.Logger) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Debug("knowledge: cannot create vcs cache dir", slog.String("error", err.Error()))
		return
	}
	data, err := json.Marshal(c)
	if err != nil {
		return
	}
	if err := file.WriteFileAtomic(path, data, 0o644); err != nil {
		log.Debug("knowledge: cannot write vcs cache", slog.String("error", err.Error()))
	}
}

// knowledgeRemoteNamespace is the fixed "project path" the knowledge shard store
// uses on the shared remote backend, keeping its shards clear of build artifacts.
const knowledgeRemoteNamespace = "__knowledge__"

// remoteShardAdapter rides a build-cache RemoteBackend as a knowledge.RemoteShards:
// a shard is content-addressed by fingerprint, stored under a fixed namespace, and
// signed/verified by the same cache trust set as build artifacts.
type remoteShardAdapter struct{ b cache.RemoteBackend }

func (a remoteShardAdapter) GetShard(ctx context.Context, key string) (io.ReadCloser, error) {
	rc, err := a.b.GetArtifact(ctx, knowledgeRemoteNamespace, key)
	if err != nil {
		return nil, err
	}
	if rc == nil {
		return nil, knowledge.ErrShardMiss // the cache backend signals a miss with (nil, nil); make it explicit
	}
	return rc, nil
}

func (a remoteShardAdapter) PutShard(ctx context.Context, key string, r io.Reader) error {
	return a.b.PutArtifact(ctx, knowledgeRemoteNamespace, key, r)
}

// remoteShardsFor returns the shard backing for a workspace: the build cache's
// remote backend when ws is a cache-backed *Magus, else nil (local-only). An
// Inspect-constructed *Magus has no cache, so it stays local.
func remoteShardsFor(ws types.Describer) knowledge.RemoteShards {
	m, ok := ws.(*Magus)
	if !ok || m.cache == nil {
		return nil
	}
	rb := m.cache.Remote()
	if rb == nil {
		return nil
	}
	return remoteShardAdapter{rb}
}

// warmKnowledgeGraph returns this handle's lazily-created warm-graph holder. The
// rebuild closure is the same cache-first BuildKnowledgeGraph the CLI runs; the
// holder adds an in-memory cache that is trusted only while WatchKnowledgeGraph
// has a watcher invalidating it.
func (m *Magus) warmKnowledgeGraph() *warmGraph {
	m.warmGraphOnce.Do(func() {
		root := m.Root()
		cfg := m.cfg
		m.warmGraph = newWarmGraph(func(ctx context.Context) (*knowledge.Graph, error) {
			return BuildKnowledgeGraph(ctx, m, root, cfg, false, slog.Default())
		}, slog.Default())
	})
	return m.warmGraph
}

// KnowledgeGraph returns the workspace knowledge graph. In the daemon, once
// WatchKnowledgeGraph is running, this answers from a warm in-memory graph without
// re-parsing magusfiles; otherwise (and on refresh) it rebuilds cache-first. It is
// always fresh: the warm graph is served only while a watcher can invalidate it.
func (m *Magus) KnowledgeGraph(ctx context.Context, refresh bool) (*knowledge.Graph, error) {
	return m.warmKnowledgeGraph().Get(ctx, refresh)
}

// KnowledgeGraphWithSymbols returns a graph that INCLUDES the lazily-loaded @symbols
// shards, for a symbol-seeded MCP query (magus_query on symbols, magus_refs). It
// builds cache-first into a FRESH graph - not the shared warm graph - and merges
// symbols into it, so the warm graph the other MCP tools answer from is never
// polluted with a workspace's (potentially huge) symbol set.
func (m *Magus) KnowledgeGraphWithSymbols(ctx context.Context) (*knowledge.Graph, error) {
	root := m.Root()
	g, err := BuildKnowledgeGraph(ctx, m, root, m.cfg, false, slog.Default())
	if err != nil {
		return nil, err
	}
	if err := MergeWorkspaceSymbols(ctx, m, root, m.cfg, g, slog.Default()); err != nil {
		return nil, err
	}
	return g, nil
}

// KnowledgeGraphWithSymbolsForRef is KnowledgeGraphWithSymbols for magus_refs: it
// merges only the symbol shards that mention ref (targeted reverse lookup) when ref
// is an exact symbol ID, or all of them for a fuzzy name. Also fresh-not-warm, so the
// shared warm graph stays symbol-free.
func (m *Magus) KnowledgeGraphWithSymbolsForRef(ctx context.Context, ref string) (*knowledge.Graph, error) {
	root := m.Root()
	g, err := BuildKnowledgeGraph(ctx, m, root, m.cfg, false, slog.Default())
	if err != nil {
		return nil, err
	}
	if err := MergeWorkspaceSymbolsForRef(ctx, m, root, m.cfg, g, ref, slog.Default()); err != nil {
		return nil, err
	}
	return g, nil
}

// WatchKnowledgeGraph starts a file watcher that keeps the warm knowledge graph
// fresh, so daemon MCP calls answer from memory. It returns a stop function; the
// long-lived daemon calls it once at startup. A one-shot CLI never calls it and
// pays the cache-first rebuild per command (equally fresh, just not warm).
func (m *Magus) WatchKnowledgeGraph(ctx context.Context) (func(), error) {
	return m.warmKnowledgeGraph().watch(ctx, m.Root())
}
