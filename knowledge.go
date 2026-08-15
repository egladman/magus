package magus

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/deps"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/hostmodules"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/notes"
	"github.com/egladman/magus/internal/spellruntime"
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
			log.WarnContext(ctx, "magus: skipping registered workspace in global graph", slog.String("workspace", wr), slog.String("error", err.Error()))
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

// cacheImmutable reports whether the cache is read-only, matching cache.Open's own
// cache.write.enabled / MAGUS_CACHE_WRITE_ENABLED check.
func cacheImmutable(cfg config.Config) bool {
	return !cfg.Cache.WriteEnabled()
}

// CatalogFingerprint identifies the compiled-in catalogs a binary contributes to
// generated output: diagnostic codes, built-in spells, module surface. Stamped into the
// exported graph so drift can be attributed to the build that produced it (MGS4005).
//
// Hashes the catalogs, not the version: `git describe` moves every commit and would churn
// the artifact, while these change only when the output would change anyway.
func CatalogFingerprint() string {
	h := sha256.New()
	fmt.Fprint(h, "diagnostics\x00")
	for _, c := range types.AllDiagnosticCodes() {
		fmt.Fprintf(h, "%s\x00", c)
	}
	fmt.Fprintf(h, "spells\x00%s\x00", spellruntime.BuiltinsHash())
	fmt.Fprint(h, "modules\x00")
	for _, m := range allModuleEntries() {
		for _, meth := range m.Methods {
			fmt.Fprintf(h, "%s.%s\x00", m.Name, meth.Name)
		}
	}
	// Truncated the way an output ref is: long enough that a collision is not a practical
	// concern, short enough to read in a diff and quote in an error message.
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// allModuleEntries returns every stdlib module with its methods populated. The
// summary view (empty name) carries only names, so each is re-queried for detail.
func allModuleEntries() []types.ModuleEntry {
	summary := hostmodules.Describe("")
	out := make([]types.ModuleEntry, 0, len(summary))
	for _, m := range summary {
		out = append(out, hostmodules.Describe(m.Name)...)
	}
	return out
}

// BuildKnowledgeGraph assembles, persists, and returns the workspace knowledge
// graph. It is the single graph-loading path shared by the `magus graph`
// subcommands, the query/explain/path verbs, and the MCP tools: it gathers the describe
// outputs the graph is composed from, resolves the cache dir, and runs the
// cache-first build. ws is any workspace view that can describe itself (the
// read-only Inspect result or a full *Magus).
func BuildKnowledgeGraph(ctx context.Context, ws types.Inspector, root string, cfg config.Config, refresh bool, log *slog.Logger) (*knowledge.Graph, error) {
	if log == nil {
		// The loaders below log best-effort; a nil logger (some callers, e.g. describe,
		// pass one) would panic on the first miss. Normalize once here.
		log = slog.Default()
	}
	cacheDir := resolveCacheDir(root, cfg)
	spells, err := ListSpells(ctx)
	if err != nil {
		return nil, err
	}
	graph, err := ws.TargetGraph(ctx)
	if err != nil {
		return nil, err
	}
	projects, err := ws.ListProjects(ctx)
	if err != nil {
		return nil, err
	}

	// Cached as an INPUT, not as a shard, because three consumers read it: @vcs, the
	// dir_commits roll-up in @dirs, and prose staleness. Caching it on @vcs instead handed
	// the other two an empty history on a hit, and they published it as zero churn and
	// unmeasured prose. Reading it back off @vcs is no substitute either: that shard is
	// filtered to paths with a file node, so it is a view of the scan and not a record of it.
	vcsEntries := loadKnowledgeVCSCached(ctx, cfg, root, cacheDir, refresh, log)

	in := knowledge.Inputs{
		Graph:       graph,
		Spells:      spells,
		Modules:     allModuleEntries(),
		Diagnostics: types.AllDiagnosticCodes(),
		Root:        root,
		Runtime:     knowledge.LoadRuntimeEvents(cacheDir),
		Timings:     loadKnowledgeTimings(ctx, cfg),
		OutputRefs:  loadKnowledgeOutputRefs(cacheDir),
		Symbols: loadKnowledgeSymbols(ctx, symbolIngestInputs{
			cfg: cfg, root: root, cacheDir: cacheDir,
			projects: projects, spells: spells, log: log,
		}),
		Packages:       loadKnowledgePackages(projects),
		VCS:            vcsEntries,
		VCSAuthorship:  cfg.Knowledge.VCS.Authorship == nil || *cfg.Knowledge.VCS.Authorship,
		DeclaredSpells: declaredSpellSet(projects),
		Coverage:       loadKnowledgeCoverage(root),
		NotesPath:      cfg.Knowledge.Notes.Shared,
		Notes:          loadKnowledgeNotesAt(root, cfg.Knowledge.Notes.Shared, notes.ScopeShared),
		PrivateNotes:   loadKnowledgeNotesAt(root, cfg.Knowledge.Notes.Private, notes.ScopePrivate),
	}
	return knowledge.Build(ctx, cacheDir, knowledge.BuildOptions{
		Immutable: cacheImmutable(cfg),
		Refresh:   refresh,
		MaxBytes:  int64(cfg.Knowledge.MaxSizeMB) * 1024 * 1024,
		Remote:    remoteShardsFor(ws),
	}, in, log)
}

// loadKnowledgePackages reads each project's third-party dependencies out of the
// manifests ProjectEntry already resolved, for the @packages shard.
//
// It reads ProjectEntry rather than reaching back into the spell registry because the
// existence check has already been done: ProjectEntry.Manifests holds the candidates
// that actually exist in the project's directory, in declared order. There is nothing
// to re-derive here, only a file to read.
//
// Go is the only reader today. Its manifest states exact versions, so go.mod alone is a
// resolved inventory; an ecosystem whose manifest holds ranges needs its lockfile
// (ProjectEntry.Lockfiles, already resolved beside Manifests) and a reader that
// understands that format. Best-effort throughout: an unreadable or unparseable
// manifest contributes no packages rather than failing the graph build.
func loadKnowledgePackages(projects types.ProjectsOutput) map[string][]types.KnowledgePackage {
	out := map[string][]types.KnowledgePackage{}
	for _, p := range projects.Projects {
		if p.Dir == "" {
			continue
		}
		for _, manifest := range p.Manifests {
			if manifest != "go.mod" {
				continue
			}
			if pkgs := deps.GoModule(filepath.Join(p.Dir, manifest)); len(pkgs) > 0 {
				out[p.Path] = append(out[p.Path], pkgs...)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
// loadKnowledgeNotes reads the declared notes store and maps each note's anchors to the
// node IDs the graph uses, so assembly can drop the ones that do not resolve.
//
// Best effort by design: an undeclared store, a missing directory, or an unreadable entry
// yields no notes rather than an error. A note the reader cannot parse is `magus notes
// verify`'s to report with a repair hint; failing a graph build over it would take the
// whole workspace down for one bad markdown file.
// loadKnowledgeNotesAt resolves one of the two declared notes stores and reads it,
// yielding nothing when that store is not declared. resolve is SharedDir or PrivateDir,
// which differ in exactly one way: whether the location may sit outside the repository.
func loadKnowledgeNotesAt(root, declared string, scope notes.Scope) []types.KnowledgeNote {
	dir, err := notes.Dir(root, scope, declared)
	if err != nil {
		return nil // not declared, or declared badly - notes verify says so
	}
	return loadKnowledgeNotes(root, dir, string(scope))
}

func loadKnowledgeNotes(root, dir, scope string) []types.KnowledgeNote {
	found, _, err := notes.Inspect(dir)
	if err != nil || len(found) == 0 {
		return nil
	}
	// A shared store is inside the checkout, so its notes get a workspace-relative path
	// that @vcs can attribute to an author. A private store may be anywhere, so there is
	// no relative path and no attribution to be had - the absolute path is the honest
	// Source, and a blank author is the honest answer rather than a fabricated one.
	rel, err := filepath.Rel(root, dir)
	if err != nil || strings.HasPrefix(rel, "..") {
		rel = dir
	}
	out := make([]types.KnowledgeNote, 0, len(found))
	for _, n := range found {
		anchors := make([]string, 0, len(n.Anchors))
		for _, a := range n.Anchors {
			if id := knowledge.AnchorNodeID(string(a.Kind), a.Target, scope); id != "" {
				anchors = append(anchors, id)
			}
		}
		out = append(out, types.KnowledgeNote{
			Name:    n.Name,
			Title:   n.Title,
			Path:    filepath.ToSlash(filepath.Join(rel, n.Name+".md")),
			Tags:    n.Tags,
			Anchors: anchors,
		})
	}
	return out
}

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
func symbolStore(ws types.Inspector, root string, cfg config.Config, log *slog.Logger) *knowledge.Store {
	if log == nil {
		log = slog.Default()
	}
	return knowledge.NewStore(resolveCacheDir(root, cfg), cacheImmutable(cfg), int64(cfg.Knowledge.MaxSizeMB)*1024*1024, remoteShardsFor(ws), log)
}

// MergeWorkspaceSymbols pulls every persisted per-project @symbols shard into g, for
// a symbol-seeded query (the default graph excludes them for scale). Best-effort: no
// store or no symbol shards is a no-op.
func MergeWorkspaceSymbols(ctx context.Context, ws types.Inspector, root string, cfg config.Config, g *knowledge.Graph, log *slog.Logger) error {
	return symbolStore(ws, root, cfg, log).MergeSymbolShards(ctx, g)
}

// MergeWorkspaceSymbolsForRef merges symbols into g for `magus refs`, targeting only
// the shards that mention ref (via the xref routing index) when ref is an exact symbol
// ID - the scale-safe reverse lookup - or all symbol shards when ref is a fuzzy name
// whose exact ID is not yet known.
func MergeWorkspaceSymbolsForRef(ctx context.Context, ws types.Inspector, root string, cfg config.Config, g *knowledge.Graph, ref string, log *slog.Logger) error {
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
func loadKnowledgeSymbols(ctx context.Context, in symbolIngestInputs) map[string][]types.KnowledgeSymbol {
	log := in.log
	decls := symbolIndexDeclarations(ctx, in)
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
				log.DebugContext(ctx, "knowledge: symbol index not built yet, skipping", slog.String("project", decl.project), slog.String("index", decl.path))
			} else {
				log.WarnContext(ctx, "knowledge: cannot read symbol index", slog.String("project", decl.project), slog.String("index", decl.path), slog.String("error", err.Error()))
			}
			continue
		}
		syms, err := symbols.ParseIndex(ctx, data, decl.project, decl.language)
		if err != nil {
			// An index that exists but will not decode is a real problem (corrupt output),
			// not a benign miss - surface it.
			log.WarnContext(ctx, "knowledge: cannot decode symbol index", slog.String("project", decl.project), slog.String("index", decl.path), slog.String("error", err.Error()))
			continue
		}
		out[decl.project] = syms
	}
	return out
}

// SymbolGaps reports every project that declares a SCIP index magus could not read, so a
// lookup can say whether it searched everywhere it should have. ok is false when the probe
// itself could not run: a nil slice would otherwise be indistinguishable from "no gaps"
// and would turn an internal failure into a confident claim of absence, which is the one
// outcome the verdict exists to prevent.
//
// It keys off the same declarations loadKnowledgeSymbols ingests, which is deliberately
// NOT the set magus status reports: declarations include knowledge.symbols overrides, and
// a project indexed only through one of those is invisible to the status lens.
//
// The probe is one Stat per declared index and nothing more. It deliberately does not
// decode the index to check it parses: that is a full protobuf unmarshal plus symbol
// accumulation per lookup, and a never-built index is the case that actually occurs. A
// present-but-corrupt index therefore reads as covered here; the graph build logs it.
//
// Freshness is out of scope for the same reason: deciding whether an index is merely STALE
// needs a cache handle, and the read verbs that call this inspect the workspace rather
// than opening it (opening writes).
func SymbolGaps(ctx context.Context, ws types.Inspector, root string, cfg config.Config, log *slog.Logger) (gaps []types.KnowledgeSymbolGap, ok bool) {
	if log == nil {
		log = slog.Default()
	}
	spells, err := ListSpells(ctx)
	if err != nil {
		log.WarnContext(ctx, "knowledge: symbol gap probe cannot list spells", slog.String("error", err.Error()))
		return nil, false
	}
	projects, err := ws.ListProjects(ctx)
	if err != nil {
		log.WarnContext(ctx, "knowledge: symbol gap probe cannot list projects", slog.String("error", err.Error()))
		return nil, false
	}
	return symbolGaps(ctx, symbolIngestInputs{
		cfg: cfg, root: root, cacheDir: resolveCacheDir(root, cfg),
		projects: projects, spells: spells, log: log,
	}), true
}

// SymbolOccurrences returns every exact source range where the symbol keyed by key
// appears, with each range verified against the file on disk. It reads the SAME declared
// indexes the graph is built from, so it can never disagree with `magus refs` about which
// projects were searched - but it goes back to the index rather than to the graph, because
// the graph edge stores a MaxRefLines-capped line list with no columns. Those are storage
// decisions that are right for a shard and unusable for an edit.
//
// key is a symbol node's key (a node ID with the "symbol:" prefix removed). Resolving a
// user-supplied name to one is the caller's job; the graph already does it for refs.
//
// Inspect-only, like SymbolGaps: it stats and reads index files and source files, and
// opens nothing. That is what lets a read verb call it.
//
// The returned names are the spellings the ranges may hold, taken from the index itself;
// names[0] is the identifier a rename targets. An empty set verifies nothing - see
// symbols.Verify - which is the conservative outcome for an index that names the symbol
// nowhere.
func SymbolOccurrences(ctx context.Context, ws types.Inspector, root string, cfg config.Config, log *slog.Logger, key string) (read SymbolOccurrenceRead, ok bool) {
	if log == nil {
		log = slog.Default()
	}
	spells, err := ListSpells(ctx)
	if err != nil {
		log.WarnContext(ctx, "knowledge: occurrence read cannot list spells", slog.String("error", err.Error()))
		return SymbolOccurrenceRead{}, false
	}
	projects, err := ws.ListProjects(ctx)
	if err != nil {
		log.WarnContext(ctx, "knowledge: occurrence read cannot list projects", slog.String("error", err.Error()))
		return SymbolOccurrenceRead{}, false
	}
	return symbolOccurrences(ctx, symbolIngestInputs{
		cfg: cfg, root: root, cacheDir: resolveCacheDir(root, cfg),
		projects: projects, spells: spells, log: log,
	}, key), true
}

// symbolOccurrences is the testable half of SymbolOccurrences: it takes the same resolved
// inputs loadKnowledgeSymbols and symbolGaps do, so none of the three can disagree about
// which indexes exist.
func symbolOccurrences(ctx context.Context, in symbolIngestInputs, key string) (read SymbolOccurrenceRead) {
	log := in.log
	dirByPath := map[string]string{}
	for _, p := range in.projects.Projects {
		dirByPath[p.Path] = p.Dir
	}
	// An index that exists but cannot be read or decoded is a HOLE, not a zero. Skipping it
	// quietly would drop a whole project's sites from a list whose entire contract is
	// completeness, under a verdict that says magus searched everywhere - so it is recorded
	// and the caller turns it into an unknown verdict.
	//
	// SymbolGaps cannot cover this one: it deliberately does a single Stat per declared
	// index and never decodes, so a corrupt index that stats fine reads there as covered.
	// That is a fair trade for describing fan-in and the wrong one for driving an edit.
	gap := func(project, detail string) {
		read.Unreadable = append(read.Unreadable, types.KnowledgeSymbolGap{
			Project: types.NewProjectRef(project, dirByPath[project]),
			State:   types.SymbolIndexNotBuilt,
			Detail:  detail,
		})
	}

	// Every declared index is read, not just the defining project's: a symbol defined in
	// one project is referenced from others, and a rewrite that stopped at the definition's
	// own index would leave every cross-project call site untouched.
	for _, decl := range symbolIndexDeclarations(ctx, in) {
		if ctx.Err() != nil {
			// Stop reading indexes on cancellation, but keep what was already gathered: the
			// caller distinguishes a short list from a complete one by the gaps below.
			gap(decl.project, "not read: cancelled")
			continue
		}
		data, err := os.ReadFile(decl.path)
		if err != nil {
			// A not-yet-built index is the expected case, and SymbolGaps already reports it
			// from its own Stat - so it stays quiet here rather than being counted twice.
			// Any OTHER read error is a hole SymbolGaps cannot see.
			if !errors.Is(err, fs.ErrNotExist) {
				log.WarnContext(ctx, "knowledge: cannot read symbol index", slog.String("project", decl.project), slog.String("index", decl.path), slog.String("error", err.Error()))
				gap(decl.project, "unreadable")
			}
			continue
		}
		found, foundNames, err := symbols.ParseOccurrences(ctx, data, decl.project, key)
		if err != nil {
			log.WarnContext(ctx, "knowledge: cannot decode symbol index", slog.String("project", decl.project), slog.String("index", decl.path), slog.String("error", err.Error()))
			gap(decl.project, "does not decode")
			continue
		}
		// One index names the symbol; the others may only reference it. Union rather than
		// first-wins, so a spelling that appears in a second project's index is still
		// recognized at that project's occurrences.
		for _, n := range foundNames {
			if !slices.Contains(read.Names, n) {
				read.Names = append(read.Names, n)
			}
		}
		read.Files = append(read.Files, found...)
	}

	// Each index contributes its own files, so the merged list needs re-sorting to stay
	// deterministic across the declaration order, and entries two indexes both produced for
	// one path have to be folded together. Two blocks for one file would double its count
	// and, worse, hand a caller the same edit twice - the once-per-file read guarantee and
	// the "files are independent" contract both assume one entry per path. No overlap exists
	// in this repo (every nested Go project has its own module), so this holds the contract
	// rather than fixing an observed break.
	slices.SortFunc(read.Files, func(a, b types.SymbolOccurrenceFile) int { return cmp.Compare(a.File, b.File) })
	read.Files = mergeOccurrenceFiles(read.Files)
	if err := symbols.VerifyOccurrences(ctx, read.Files, read.Names, func(p string) ([]byte, error) {
		return os.ReadFile(filepath.Join(in.root, filepath.FromSlash(p)))
	}); err != nil {
		log.WarnContext(ctx, "knowledge: occurrence verification stopped early", slog.String("error", err.Error()))
	}
	return read
}

// mergeOccurrenceFiles folds entries sharing a path into one, concatenating and re-sorting
// their occurrences and dropping sites that land on the same position. Input must be sorted
// by File.
func mergeOccurrenceFiles(in []types.SymbolOccurrenceFile) []types.SymbolOccurrenceFile {
	out := in[:0]
	for _, f := range in {
		if n := len(out); n > 0 && out[n-1].File == f.File {
			prev := &out[n-1]
			prev.Occurrences = append(prev.Occurrences, f.Occurrences...)
			slices.SortFunc(prev.Occurrences, func(a, b types.SymbolOccurrence) int {
				if c := cmp.Compare(a.Line, b.Line); c != 0 {
					return c
				}
				return cmp.Compare(a.Column, b.Column)
			})
			prev.Occurrences = slices.CompactFunc(prev.Occurrences, func(a, b types.SymbolOccurrence) bool {
				return a.Line == b.Line && a.Column == b.Column
			})
			continue
		}
		out = append(out, f)
	}
	return out
}

// SymbolOccurrenceRead is what SymbolOccurrences could read: the verified sites, the
// spellings they were checked against, and every declared index that exists but yielded
// nothing usable.
//
// Unreadable is the field that keeps the result honest. The occurrence list claims to be
// complete, so an index magus could not decode has to travel WITH the sites rather than be
// dropped on the way - a caller folds it into the coverage gaps, which turns the verdict
// from "searched everywhere" into "unknown, not absent" and names the project to rebuild.
// The sites that WERE read are still returned: a partial answer plus an accurate account
// of what is missing beats discarding both.
type SymbolOccurrenceRead struct {
	Files []types.SymbolOccurrenceFile
	// Names are the spellings an occurrence may hold; Names[0] is the identifier a rename
	// targets. See symbols.ParseOccurrences.
	Names      []string
	Unreadable []types.KnowledgeSymbolGap
}

// symbolGaps is the testable half of SymbolGaps: it takes the same resolved inputs
// loadKnowledgeSymbols does, so the two cannot disagree about which indexes exist.
func symbolGaps(ctx context.Context, in symbolIngestInputs) []types.KnowledgeSymbolGap {
	dirByPath := map[string]string{}
	for _, p := range in.projects.Projects {
		dirByPath[p.Path] = p.Dir
	}

	var out []types.KnowledgeSymbolGap
	for _, decl := range symbolIndexDeclarations(ctx, in) {
		// Detail carries what Stat can actually distinguish: absent, or present but
		// unreadable. State stays the machine-branchable field and is accurate for both,
		// since neither yields a usable index.
		var detail string
		if _, err := os.Stat(decl.path); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			detail = "unreadable"
		}
		out = append(out, types.KnowledgeSymbolGap{
			Project: types.NewProjectRef(decl.project, dirByPath[decl.project]),
			State:   types.SymbolIndexNotBuilt,
			Detail:  detail,
		})
	}
	return out
}

// SymbolGaps reports the projects whose declared symbol index this workspace could not
// read, and whether the probe ran at all. Method form of the package-level SymbolGaps,
// for callers that already hold a Magus (the MCP handlers).
func (m *Magus) SymbolGaps(ctx context.Context) ([]types.KnowledgeSymbolGap, bool) {
	return SymbolGaps(ctx, m, m.Root(), m.cfg, slog.Default())
}

// SymbolOccurrences returns every verified source range where the symbol keyed by key
// appears. Method form of the package-level SymbolOccurrences, for callers that already
// hold a Magus - the pairing SymbolGaps keeps, since the two answers are read together.
func (m *Magus) SymbolOccurrences(ctx context.Context, key string) (SymbolOccurrenceRead, bool) {
	return SymbolOccurrences(ctx, m, m.Root(), m.cfg, slog.Default(), key)
}

// resolvedSymbolIndex pairs a project with the absolute path of its SCIP index and the
// language the spell that produced it adapts.
//
// language is carried because an indexer may not report one. SCIP makes Document.Language
// optional and scip-typescript sets it on nothing, so trusting the index alone leaves
// every TypeScript symbol unlabeled and `magus query language:typescript` empty. It comes
// from the project's spells, which is authoritative and free - and it is resolved for a
// knowledge.symbols override too, since such a project is still bound to spells even
// though the override names a path rather than one.
type resolvedSymbolIndex struct {
	project  string
	path     string
	language string
}

// symbolIngestInputs is the shared context for resolving and reading symbol indexes,
// threaded as one value so loadKnowledgeSymbols and symbolIndexDeclarations cannot drift
// out of lockstep as the input set grows.
type symbolIngestInputs struct {
	cfg      config.Config
	root     string
	cacheDir string
	projects types.ProjectsOutput
	spells   []types.Spell
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
func symbolIndexDeclarations(ctx context.Context, in symbolIngestInputs) []resolvedSymbolIndex {
	capable := map[string]bool{}
	langBySpell := map[string]string{}
	for _, sp := range in.spells {
		langBySpell[sp.Name] = sp.Language
		if slices.Contains(sp.Targets, symbols.IndexOp) {
			capable[sp.Name] = true
		}
	}
	// The language a project's symbols are written in, resolved from its spells rather
	// than from the index: SCIP makes Document.Language optional and scip-typescript sets
	// it on nothing. Prefer the symbol-capable spell, but fall back to any bound spell so
	// a knowledge.symbols OVERRIDE - which names a path, not a spell, and so never reaches
	// the capable branch below - still labels its symbols.
	languageOf := func(p types.ProjectEntry) string {
		bound := p.Spells
		if len(bound) == 0 && p.Spell != "" {
			bound = []string{p.Spell}
		}
		for _, name := range bound {
			if capable[name] {
				return langBySpell[name]
			}
		}
		for _, name := range bound {
			if lang := langBySpell[name]; lang != "" {
				return lang
			}
		}
		return ""
	}
	languageByProject := map[string]string{}
	for _, p := range in.projects.Projects {
		languageByProject[p.Path] = languageOf(p)
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
			byProject[p.Path] = resolvedSymbolIndex{project: p.Path, path: symbols.IndexPath(in.cacheDir, absDir), language: languageByProject[p.Path]}
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
			in.log.WarnContext(ctx, "knowledge: symbol index path escapes the workspace, skipping", slog.String("project", decl.Project), slog.String("index", decl.Index))
			continue
		}
		byProject[decl.Project] = resolvedSymbolIndex{project: decl.Project, path: filepath.Join(in.root, decl.Index), language: languageByProject[decl.Project]}
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

// loadKnowledgeVCS gathers per-file history for the @vcs shard when opt-in
// (knowledge.vcs.enabled), routed through the VCS abstraction so it is not git-specific:
// any resolved backend that implements ChurnReporter works, and one that does not is
// skipped. Best-effort: a disabled/absent VCS or a scan error yields no metadata (the
// shard is simply absent), never an error. Callers want loadKnowledgeVCSCached; this is the
// uncached walk it wraps.
func loadKnowledgeVCS(ctx context.Context, cfg config.Config, root string, log *slog.Logger) []types.KnowledgeVCS {
	if !cfg.Knowledge.VCS.Enabled {
		return nil
	}
	res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{})
	if err != nil || res.Source == types.VCSSourceDisabled || res.VCS == nil {
		log.DebugContext(ctx, "knowledge: vcs enabled but no version control resolved, skipping")
		return nil
	}
	reporter, ok := res.VCS.(types.ChurnReporter)
	if !ok {
		log.DebugContext(ctx, "knowledge: vcs backend cannot report per-commit files, skipping", slog.String("vcs", res.Name))
		return nil
	}
	changes, err := reporter.ChangesByCommit(ctx, root, vcsMaxCommits(cfg), "")
	if err != nil {
		log.WarnContext(ctx, "knowledge: vcs history scan failed, skipping", slog.String("error", err.Error()))
		return nil
	}
	return aggregateFileHistory(changes, vcsPathPrefix(root, res.VCS.Claims()))
}

// vcsHistoryFile holds one cached scan. The fingerprint travels inside the file rather than
// in its name, so a stale scan is overwritten instead of accumulating a file per dead HEAD.
type vcsHistoryFile struct {
	Fingerprint string               `json:"fingerprint"`
	Entries     []types.KnowledgeVCS `json:"entries"`
}

// vcsHistoryFormat versions the cached SHAPE. Bump it whenever a KnowledgeVCS json tag is
// added, renamed, or retyped: the rest of the key cannot see a shape change, so an old file
// would match on an unchanged HEAD and decode the renamed fields as zero values.
//
//	v2: LastUnix (epoch int64) became LastModified (time.Time).
const vcsHistoryFormat = 2

// loadKnowledgeVCSCached returns the per-file history, walking it only when the cached scan
// does not match the current input.
//
// A hit and a miss must be indistinguishable downstream - every consumer gets the same full
// slice either way - which is the property that makes caching this safe at all.
//
// refresh forces the walk and still rewrites the cache, so distrusting it costs one walk
// rather than every walk. Best-effort throughout: an unreadable file or a failed write just
// means walking, and nothing here can fail a build.
func loadKnowledgeVCSCached(ctx context.Context, cfg config.Config, root, cacheDir string, refresh bool, log *slog.Logger) []types.KnowledgeVCS {
	fp := vcsInputFingerprint(ctx, cfg, root)
	path := filepath.Join(knowledge.StoreDir(cacheDir), "inputs", "vcs.json")
	if fp != "" && !refresh {
		if b, err := os.ReadFile(path); err == nil {
			var f vcsHistoryFile
			if err := json.Unmarshal(b, &f); err == nil && f.Fingerprint == fp {
				log.DebugContext(ctx, "knowledge: reusing cached vcs history", slog.Int("files", len(f.Entries)))
				return f.Entries
			}
		}
	}
	entries := loadKnowledgeVCS(ctx, cfg, root, log)
	// An empty scan is not worth a file, and writing one would cache "no history" against a
	// real HEAD - so a transient git failure would persist as an answer.
	if fp == "" || len(entries) == 0 || cacheImmutable(cfg) {
		return entries
	}
	b, err := json.Marshal(vcsHistoryFile{Fingerprint: fp, Entries: entries})
	if err == nil {
		err = file.WriteFileAtomic(path, b, 0o644)
	}
	if err != nil {
		log.DebugContext(ctx, "knowledge: caching vcs history failed", slog.String("error", err.Error()))
	}
	return entries
}

// vcsInputFingerprint identifies the history the scan reads: where it starts, how far back
// it walks, and the format it is cached in.
//
// Uncommitted work is deliberately excluded - the scan reads committed history only, so
// folding the dirty set in busted the cache on every add or delete of a tracked file for no
// gain. Empty (always walk) when VCS is off or no revision resolves.
func vcsInputFingerprint(ctx context.Context, cfg config.Config, root string) string {
	if !cfg.Knowledge.VCS.Enabled {
		return ""
	}
	res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{})
	if err != nil || res.Source == types.VCSSourceDisabled || res.VCS == nil {
		return ""
	}
	head, err := res.VCS.FindCommit(ctx, root, "")
	if err != nil || head.ID == "" {
		return ""
	}
	h := sha256.New()
	fmt.Fprintf(h, "f%d\x00%s\x00%d\x00", vcsHistoryFormat, head.ID, vcsMaxCommits(cfg))
	return hex.EncodeToString(h.Sum(nil))
}

// vcsMaxCommits is the bounded history window: the most recent N commits, never the whole
// history (the scale guard for a large monorepo). Configurable via knowledge.vcs.max_commits.
func vcsMaxCommits(cfg config.Config) int {
	if m := cfg.Knowledge.VCS.MaxCommits; m > 0 {
		return m
	}
	return vcsDefaultMaxCommits
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
		lastCommit   string
		lastModified time.Time
		lastAuthor   string
		authors      map[string]bool
		commits      int
	}
	byPath := map[string]*acc{}
	var order []string
	for _, c := range changes {
		short := ShortRevision(c.ID)
		modified := c.Date.UTC()
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
				// First sighting = the most recent commit (changes are newest-first).
				a = &acc{lastCommit: short, lastModified: modified, lastAuthor: c.Author, authors: map[string]bool{}}
				byPath[f] = a
				order = append(order, f)
			}
			if c.Author != "" {
				a.authors[c.Author] = true
			}
			a.commits++
		}
	}
	entries := make([]types.KnowledgeVCS, 0, len(order))
	for _, p := range order {
		a := byPath[p]
		entries = append(entries, types.KnowledgeVCS{Path: p, LastCommit: a.lastCommit, LastModified: a.lastModified, LastAuthor: a.lastAuthor, Authors: slices.Sorted(maps.Keys(a.authors)), Commits: a.commits})
	}
	return entries
}

// ShortRevision abbreviates a full VCS revision id for display, leaving a
// short id untouched. Matches this codebase's convention of a 12-hex-digit
// truncation elsewhere (PortableRef); the stored/compared value is always
// the full revision, this is presentation only.
func ShortRevision(id string) string {
	const short = 12
	if len(id) > short {
		return id[:short]
	}
	return id
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
func remoteShardsFor(ws types.Inspector) knowledge.RemoteShards {
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
