package magus

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/gob"
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
	"sync"
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
	"github.com/egladman/magus/internal/oci"
	"github.com/egladman/magus/internal/readlog"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/spell"
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
	// A pasted path is measured from the workspace the command was run in; the registered
	// ones contribute nodes, not a second frame of reference.
	merged.SetRoot(root)
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
// acceptable: the alternative (full paths) makes node IDs unreadable.
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
	fmt.Fprintf(h, "spells\x00%s\x00", spell.BuiltinsHash())
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
//
// It answers from the store when the inputs are unchanged, and otherwise reassembles only
// the shard classes whose input stamps moved (see knowledgeStamps). It brings the default
// graph's classes up to date and leaves the lazily loaded symbol classes to the readers
// that merge them (MergeWorkspaceSymbols), so a domain read never parses a SCIP index;
// refresh rebuilds every class.
func BuildKnowledgeGraph(ctx context.Context, ws types.Inspector, root string, cfg config.Config, refresh bool, log *slog.Logger) (*knowledge.Graph, error) {
	want := knowledge.DefaultClasses
	if refresh {
		want = knowledge.AllClasses
	}
	return ensureKnowledgeGraph(ctx, ws, root, cfg, refresh, want, log)
}

// ensureKnowledgeGraph brings the classes in want up to date and returns the default graph.
func ensureKnowledgeGraph(ctx context.Context, ws types.Inspector, root string, cfg config.Config, refresh bool, want []knowledge.ShardClass, log *slog.Logger) (*knowledge.Graph, error) {
	if log == nil {
		// The loaders below log best-effort; a nil logger (some callers, e.g. describe,
		// pass one) would panic on the first miss. Normalize once here.
		log = slog.Default()
	}
	cacheDir := resolveCacheDir(root, cfg)
	var tree *knowledge.TreeWalk
	if lw, ok := ws.(*LazyWorkspace); ok && lw.root == root {
		// One read, one walk: the coverage probe that follows the read folds the same tree.
		tree = lw.tree()
	} else if root != "" {
		tree = knowledge.WalkTree(root)
	}
	store := knowledge.NewStore(cacheDir, true, 0, nil, log)

	// The workspace model (its target graph, projects and spells) is what the full stamps
	// and every rebuild read, and it is the one input that costs a workspace evaluation:
	// on a *LazyWorkspace, the first TargetGraph call parses every magusfile. So it is
	// resolved once, on demand, and a read the fast stamps settle never asks for it.
	var (
		modelOnce sync.Once
		model     knowledgeSources
		modelErr  error
	)
	resolveModel := func(ctx context.Context) (knowledgeSources, error) {
		modelOnce.Do(func() {
			var spells []types.Spell
			var graph types.TargetGraphOutput
			var projects types.ProjectsOutput
			// Spells after the workspace: ListSpells reads the global registry, which a
			// workspace load is what populates with the workspace's own spells.
			if graph, modelErr = ws.TargetGraph(ctx); modelErr != nil {
				return
			}
			if projects, modelErr = ws.ListProjects(ctx); modelErr != nil {
				return
			}
			if spells, modelErr = ListSpells(ctx); modelErr != nil {
				return
			}
			model = knowledgeSources{
				cfg: cfg, root: root, cacheDir: cacheDir,
				spells: spells, graph: graph, projects: projects, tree: tree, log: log,
			}
		})
		return model, modelErr
	}
	opts := knowledge.BuildOptions{
		Immutable:  cacheImmutable(cfg),
		Refresh:    refresh,
		MaxBytes:   int64(cfg.Knowledge.MaxSizeMB) * 1024 * 1024,
		Remote:     remoteShards(ws),
		FastStampsFunc: func(ctx context.Context, reads readlog.Reads, known bool) knowledge.Stamps {
			return knowledgeFastStamps(ctx, cfg, root, cacheDir, tree, want, reads, known)
		},
		StampsFunc: func(ctx context.Context) (knowledge.Stamps, error) {
			src, err := resolveModel(ctx)
			if err != nil {
				return nil, err
			}
			return knowledgeStamps(ctx, src, store, want), nil
		},
		IndexesFunc: func(ctx context.Context) ([]knowledge.SymbolIndexDeclaration, error) {
			src, err := resolveModel(ctx)
			if err != nil {
				return nil, err
			}
			return symbolIndexDeclarationRecords(ctx, src.symbolInputs(), indexFreshness(ctx, ws)), nil
		},
		ReadsFunc: func(ctx context.Context) (readlog.Reads, error) {
			if _, err := resolveModel(ctx); err != nil {
				return readlog.Reads{}, err
			}
			reads, _ := evaluationReads(ctx, ws)
			return reads, nil
		},
		Root: root,
	}
	return knowledge.Ensure(ctx, cacheDir, opts, want, func(stale []knowledge.ShardClass) (knowledge.Inputs, error) {
		src, err := resolveModel(ctx)
		if err != nil {
			return knowledge.Inputs{}, err
		}
		in := gatherKnowledgeInputs(ctx, src, refresh, stale)
		in.Indexes = symbolIndexDeclarationRecords(ctx, src.symbolInputs(), indexFreshness(ctx, ws))
		if reads, ok := evaluationReads(ctx, ws); ok {
			in.Reads = &reads
		}
		return in, nil
	}, log)
}

// magusBehind returns the *Magus a knowledge read's workspace is, opening a lazy one: the
// handle whose evaluation just produced the model, which is where its reads and its index
// freshness verdicts live. nil for any other Inspector (a test double), whose evaluation
// records neither.
func magusBehind(ctx context.Context, ws types.Inspector) *Magus {
	switch w := ws.(type) {
	case *Magus:
		return w
	case *LazyWorkspace:
		if m, err := w.Magus(ctx); err == nil {
			return m
		}
	}
	return nil
}

// evaluationReads returns what the evaluation behind ws read beyond the tree, and false
// when ws carries no evaluation to ask.
func evaluationReads(ctx context.Context, ws types.Inspector) (readlog.Reads, bool) {
	m := magusBehind(ctx, ws)
	if m == nil {
		return readlog.Reads{}, false
	}
	return m.EvaluationReads(), true
}

// indexFreshness is the freshness verdict of every symbol index the evaluation behind ws
// declares, by project path, or nil when ws carries no evaluation to ask. It costs the
// probe SymbolIndexStatusByStamp costs, paid where the evaluation already was.
func indexFreshness(ctx context.Context, ws types.Inspector) map[string]types.SymbolIndexStatus {
	m := magusBehind(ctx, ws)
	if m == nil {
		return nil
	}
	out := map[string]types.SymbolIndexStatus{}
	for _, s := range m.SymbolIndexStatusByStamp(ctx) {
		path := s.Project.Path
		if path == "" {
			path = "."
		}
		out[path] = s
	}
	return out
}

// symbolIndexDeclarationRecords is symbolIndexDeclarations in the form the knowledge store
// records on its manifest, so a read the fast stamps settle can probe coverage without the
// evaluated workspace the resolution needs. Never nil: a workspace that declares no index
// records an empty list, which the store tells apart from none recorded.
func symbolIndexDeclarationRecords(ctx context.Context, in symbolIngestInputs, freshness map[string]types.SymbolIndexStatus) []knowledge.SymbolIndexDeclaration {
	dirByPath := make(map[string]string, len(in.projects.Projects))
	for _, p := range in.projects.Projects {
		dirByPath[p.Path] = p.Dir
	}
	decls := symbolIndexDeclarations(ctx, in)
	out := make([]knowledge.SymbolIndexDeclaration, 0, len(decls))
	for _, d := range decls {
		rec := knowledge.SymbolIndexDeclaration{Project: d.project, Dir: dirByPath[d.project], Language: d.language, Path: d.path}
		if s, ok := freshness[d.project]; ok {
			// The verdict and the file it judged, so a later read can reuse the one while
			// the other is unchanged (see RecordedStaleIndexes). An index that cannot be
			// stat'ed records no identity, and so is re-judged by every read.
			rec.Freshness, rec.Detail = string(s.Freshness), s.Detail
			if info, err := os.Stat(d.path); err == nil {
				rec.Size, rec.ModTime = info.Size(), info.ModTime().UnixNano()
			}
		}
		out = append(out, rec)
	}
	return out
}

// recordedSymbolIndexDeclarations answers the declared indexes for a lazy workspace that
// has not been opened, from what the store recorded at its last evaluated sync, provided
// the domain fast stamp still matches. false means the caller must open the workspace to
// resolve them; it never guesses.
func recordedSymbolIndexDeclarations(ctx context.Context, lw *LazyWorkspace, cfg config.Config) ([]knowledge.SymbolIndexDeclaration, bool) {
	cacheDir := resolveCacheDir(lw.root, cfg)
	store := knowledge.NewStore(cacheDir, true, 0, nil, nil)
	reads, known := store.EvaluationReads()
	fast := knowledgeFastStamps(ctx, cfg, lw.root, cacheDir, lw.tree(), []knowledge.ShardClass{knowledge.ClassDomain}, reads, known)
	return store.SymbolIndexDeclarations(fast[knowledge.ClassDomain])
}

// RecordedStaleIndexes answers, for a lazy workspace that has not been opened, which
// declared symbol indexes the last evaluation judged stale, from the verdicts the store
// recorded: valid while the domain fast stamp matches (the sources are as they were) and
// every judged index file is the one it judged (same size and mtime). false means the
// caller must open the workspace and judge afresh, as every read did before; it never
// guesses, so an index rebuilt since the record, or one the record never judged, sends
// the read to the workspace rather than to a stale verdict.
func RecordedStaleIndexes(ctx context.Context, lw *LazyWorkspace, cfg config.Config) ([]string, bool) {
	decls, ok := recordedSymbolIndexDeclarations(ctx, lw, cfg)
	if !ok {
		return nil, false
	}
	var stale []string
	for _, d := range decls {
		if d.Freshness == "" {
			return nil, false
		}
		info, err := os.Stat(d.Path)
		switch {
		case err != nil && d.Size == 0 && d.ModTime == 0:
			// Judged absent then, absent now: the verdict (not built) stands.
		case err != nil, info.Size() != d.Size, info.ModTime().UnixNano() != d.ModTime:
			return nil, false
		}
		if d.Freshness == string(types.SymbolIndexStale) {
			stale = append(stale, d.Project)
		}
	}
	slices.Sort(stale)
	return stale, true
}

// knowledgeSources is what both the stamps and the gathered inputs are derived from,
// resolved once per build.
type knowledgeSources struct {
	cfg      config.Config
	root     string
	cacheDir string
	spells   []types.Spell
	graph    types.TargetGraphOutput
	projects types.ProjectsOutput
	tree     *knowledge.TreeWalk // nil when there is no root to walk
	log      *slog.Logger
}

func (s knowledgeSources) symbolInputs() symbolIngestInputs {
	return symbolIngestInputs{
		cfg: s.cfg, root: s.root, cacheDir: s.cacheDir,
		projects: s.projects, spells: s.spells, log: s.log,
	}
}

// gatherKnowledgeInputs reads the inputs of the stale classes and nothing else: a domain
// rebuild never reads a SCIP index or the session store, and a runtime rebuild never
// reads the tree.
func gatherKnowledgeInputs(ctx context.Context, src knowledgeSources, refresh bool, stale []knowledge.ShardClass) knowledge.Inputs {
	needs := func(cs ...knowledge.ShardClass) bool {
		return slices.ContainsFunc(cs, func(c knowledge.ShardClass) bool { return slices.Contains(stale, c) })
	}
	in := knowledge.Inputs{Graph: src.graph, Spells: src.spells, Root: src.root, Tree: src.tree}
	if needs(knowledge.ClassDomain, knowledge.ClassSymbols) {
		// Cached as an INPUT, not as a shard, because three consumers read it: @vcs, the
		// dir_commits roll-up in @dirs, and prose staleness. Caching it on @vcs instead
		// handed the other two an empty history on a hit, and they published it as zero
		// churn and unmeasured prose. Reading it back off @vcs is no substitute either: that
		// shard is filtered to paths with a file node, so it is a view of the scan and not a
		// record of it.
		in.VCS = loadKnowledgeVCSCached(ctx, src.cfg, src.root, src.cacheDir, refresh, src.log)
	}
	if needs(knowledge.ClassDomain) {
		cfg := src.cfg
		in.Modules = allModuleEntries()
		in.Diagnostics = types.AllDiagnosticCodes()
		in.Packages = loadKnowledgePackages(ctx, src.projects, src.log)
		in.VCSAuthorship = cfg.Knowledge.VCS.Authorship == nil || *cfg.Knowledge.VCS.Authorship
		in.DeclaredSpells = declaredSpellSet(src.projects)
		in.NotesPath = cfg.Knowledge.Notes.Shared
		in.Notes = loadKnowledgeNotesAt(src.root, cfg.Knowledge.Notes.Shared, notes.ScopeShared)
		in.PrivateNotes = loadKnowledgeNotesAt(src.root, cfg.Knowledge.Notes.Private, notes.ScopePrivate)
	}
	if needs(knowledge.ClassRuntime) {
		in.Runtime = knowledge.LoadRuntimeEvents(src.cacheDir)
		in.Timings = loadKnowledgeTimings(ctx, src.cfg)
		in.OutputRefs = loadKnowledgeOutputRefs(src.cacheDir)
	}
	if needs(knowledge.ClassSymbols, knowledge.ClassCoverage) {
		in.Symbols = loadKnowledgeSymbols(ctx, src.symbolInputs())
		if extra := symbolSourcesOutside(src.tree, in.Symbols); len(extra) > 0 {
			in.Extra = map[knowledge.ShardClass][]string{knowledge.ClassSymbols: extra}
		}
	}
	if needs(knowledge.ClassCoverage) {
		in.Coverage = loadKnowledgeCoverage(src.root)
	}
	if needs(knowledge.ClassSession) {
		in.AgentContacts = loadKnowledgeAgentContacts(src.root)
	}
	return in
}

// symbolSourcesOutside lists the defining files the symbols read that the tree walk did
// not see: FingerprintBodies reads every one of them, and the walk's digest is all the
// symbols stamp otherwise knows about the tree.
func symbolSourcesOutside(tree *knowledge.TreeWalk, syms map[string][]types.KnowledgeSymbol) []string {
	if tree == nil {
		return nil
	}
	seen := map[string]bool{}
	for _, list := range syms {
		for _, sym := range list {
			p, _, ok := strings.Cut(sym.Source, ":")
			if ok && !seen[p] && !tree.Contains(p) {
				seen[p] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(seen))
}

// knowledgeStamps computes the input stamp of every class in want, plus the ones the
// session stamp folds in when it is wanted. Each stamp covers everything that class's
// assembly reads, by identity rather than content: the magus binary (assembly is code),
// the config, the target graph and spells, and per class:
//
//   - domain: the tree walk's digest, the committed history's head, the ignore-rule files
//     the VCS keeps outside the tree, the notes stores and the package manifests and
//     lockfiles.
//   - runtime: the runtime records, the timing history and the output store.
//   - symbols: the tree, the head, each declared SCIP index, and any defining file the
//     last ingestion read outside the walk.
//   - coverage: each SCIP index, the coverage profile and go.mod.
//   - session: the domain, symbols and coverage stamps and the session store.
//
// A stamp that cannot be computed is left empty, which makes its class rebuild, never
// match.
func knowledgeStamps(ctx context.Context, src knowledgeSources, store *knowledge.Store, want []knowledge.ShardClass) knowledge.Stamps {
	out := knowledge.Stamps{}
	session := slices.Contains(want, knowledge.ClassSession)
	wants := func(c knowledge.ShardClass) bool {
		return slices.Contains(want, c) || (session && c != knowledge.ClassRuntime)
	}
	base, ok := baseKnowledgeStamp(src)
	if !ok || src.tree == nil {
		return out
	}
	vcsHead := vcsInputFingerprint(ctx, src.cfg, src.root)
	historyKnown := vcsHead != "" || !src.cfg.Knowledge.VCS.Enabled
	var tree string
	if wants(knowledge.ClassDomain) || wants(knowledge.ClassSymbols) {
		tree = src.tree.Digest()
	}
	var ignoreSources []string
	ignoreKnown := false
	if wants(knowledge.ClassDomain) {
		ignoreSources, ignoreKnown = vcsIgnoreSources(ctx, src.root)
	}
	if wants(knowledge.ClassDomain) && historyKnown && ignoreKnown {
		h := knowledge.NewInputHash(string(knowledge.ClassDomain))
		h.String(base)
		h.String(tree)
		h.String(vcsHead)
		for _, p := range ignoreSources {
			h.Path(p)
		}
		for _, s := range []struct {
			scope    notes.Scope
			declared string
		}{{notes.ScopeShared, src.cfg.Knowledge.Notes.Shared}, {notes.ScopePrivate, src.cfg.Knowledge.Notes.Private}} {
			if dir, err := notes.Dir(src.root, s.scope, s.declared); err == nil {
				h.Path(dir)
			}
		}
		for _, p := range src.projects.Projects {
			for _, m := range p.Manifests {
				h.Path(filepath.Join(p.Dir, m))
			}
			for _, l := range p.Lockfiles {
				h.Path(filepath.Join(src.projects.Workspace, filepath.FromSlash(l)))
			}
		}
		out[knowledge.ClassDomain] = h.Sum()
	}
	if wants(knowledge.ClassRuntime) {
		h := knowledge.NewInputHash(string(knowledge.ClassRuntime))
		h.String(base)
		h.Path(knowledge.RuntimeRecordsPath(src.cacheDir))
		if src.cfg.HistoryPath != "" {
			h.Path(src.cfg.HistoryPath)
		}
		// Every descriptor is created or renamed into place, so directory mtimes see each
		// run without reading its descriptor.
		h.Dirs(filepath.Join(src.cacheDir, "outputs"))
		out[knowledge.ClassRuntime] = h.Sum()
	}
	var indexes []resolvedSymbolIndex
	if wants(knowledge.ClassSymbols) || wants(knowledge.ClassCoverage) {
		indexes = symbolIndexDeclarations(ctx, src.symbolInputs())
	}
	foldIndexes := func(h *knowledge.InputHash) {
		for _, decl := range indexes {
			h.String(decl.project)
			h.String(decl.language)
			h.Path(decl.path)
		}
	}
	if wants(knowledge.ClassSymbols) && historyKnown {
		h := knowledge.NewInputHash(string(knowledge.ClassSymbols))
		h.String(base)
		h.String(tree)
		h.String(vcsHead)
		foldIndexes(h)
		for _, rel := range store.ExtraInputs(knowledge.ClassSymbols) {
			h.Path(filepath.Join(src.root, filepath.FromSlash(rel)))
		}
		out[knowledge.ClassSymbols] = h.Sum()
	}
	if wants(knowledge.ClassCoverage) {
		h := knowledge.NewInputHash(string(knowledge.ClassCoverage))
		h.String(base)
		foldIndexes(h)
		h.Path(filepath.Join(src.root, ".magus", "coverage.out"))
		h.Path(filepath.Join(src.root, "go.mod"))
		out[knowledge.ClassCoverage] = h.Sum()
	}
	if session {
		h := knowledge.NewInputHash(string(knowledge.ClassSession))
		// Not runtime: the overlay resolves contacts against file and dir nodes only, and
		// @runtime mints neither, while its stamp moves with every run on the machine.
		for _, c := range []knowledge.ShardClass{knowledge.ClassDomain, knowledge.ClassSymbols, knowledge.ClassCoverage} {
			if out[c] == "" {
				return out
			}
			h.String(out[c])
		}
		if dir, err := sessions.Dir(src.root); err == nil {
			h.Path(dir)
		} else {
			h.String("no session store")
		}
		out[knowledge.ClassSession] = h.Sum()
	}
	return out
}

// ignoreSourcer is the optional VCS capability of naming the files outside the working
// tree that hold ignore rules. The tree scans drop what the VCS ignores, so those rules are
// a domain input even though no walk of the tree sees them.
type ignoreSourcer interface {
	IgnoreSources(ctx context.Context, root string) ([]string, error)
}

// vcsIgnoreSources returns the ignore-rule files outside root that the workspace's VCS
// consults. ok is false when the VCS has them but could not say where, which leaves the
// domain unstamped rather than stamped blind. No VCS filters nothing, so it is ok with
// none; so is a backend that does not implement ignoreSourcer, which today is every one
// but git, and whose out-of-tree rules therefore go unstamped.
func vcsIgnoreSources(ctx context.Context, root string) (paths []string, ok bool) {
	res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{})
	if err != nil || res.Source == types.VCSSourceDisabled || res.VCS == nil {
		return nil, true
	}
	is, has := res.VCS.(ignoreSourcer)
	if !has {
		return nil, true
	}
	paths, err = is.IgnoreSources(ctx, root)
	return paths, err == nil
}

// baseKnowledgeStamp folds what every class reads: the binary, the store's schema, where
// the workspace and its cache are, the knowledge config, and the target graph, projects
// and spells the workspace describes.
func baseKnowledgeStamp(src knowledgeSources) (string, bool) {
	h := knowledge.NewInputHash("base")
	if !foldKnowledgeSite(h, src.cfg, src.root, src.cacheDir) {
		return "", false
	}
	for _, v := range []any{src.graph, src.projects, src.spells} {
		b, err := json.Marshal(v)
		if err != nil {
			return "", false
		}
		h.String(string(b))
	}
	return h.Sum(), true
}

// foldKnowledgeSite folds the part of the base stamp that costs no workspace evaluation:
// the binary, the store's schema, where the workspace and its cache are, and the knowledge
// config. It is the whole base of a fast stamp and the first half of a full one.
func foldKnowledgeSite(h *knowledge.InputHash, cfg config.Config, root, cacheDir string) bool {
	if !foldBinary(h) {
		return false
	}
	h.String(fmt.Sprint(types.KnowledgeSchemaVersion))
	h.String(root)
	h.String(cacheDir)
	h.String(cfg.HistoryPath)
	b, err := json.Marshal(cfg.Knowledge)
	if err != nil {
		return false
	}
	h.String(string(b))
	return true
}

// foldEvaluationReads folds what an evaluation read beyond the tree: each file's identity
// (inside the tree it is already in the digest; outside it this is its only coverage), each
// environment variable's current value or absence, and, after a read of the whole
// environment, every variable. known false folds a marker no recorded stamp carries.
func foldEvaluationReads(h *knowledge.InputHash, reads readlog.Reads, known bool) {
	if !known {
		h.String("reads:unknown")
		return
	}
	for _, p := range reads.Files {
		h.Path(p)
	}
	for _, name := range reads.Env {
		if v, ok := os.LookupEnv(name); ok {
			h.String("env:" + name + "=" + v)
		} else {
			h.String("env:" + name + ":unset")
		}
	}
	if reads.EnvAll {
		for _, kv := range slices.Sorted(slices.Values(os.Environ())) {
			h.String("environ:" + kv)
		}
	}
}

// knowledgeFastStamps computes the fast stamps (see knowledge.BuildOptions.FastStamps) of
// the default classes in want: every input their full stamps fold except the evaluated
// workspace model, so computing one costs the tree walk and a few stats, never a magusfile
// parse. The domain's model is a function of the tree (magusfiles, spells, magus.yaml,
// magus.lock), the binary (embedded spells), the config, the provider answers cached under
// the cache dir, and whatever its magusfile top levels read beyond the tree: the
// environment variables and files the last evaluation recorded (reads). The stamp folds
// every one, so a matching fast stamp means the evaluation would reach the same model.
// A manifest that never recorded its reads (known false) folds that fact instead, so its
// stamp matches nothing recorded with them and the next read evaluates once to record
// them. A full build still records the full stamp beside it, and --refresh ignores both.
//
// The lazy classes get none: their stamps fold the symbol index declarations, which come
// from the evaluated projects, so a symbol read pays for the model as it always did.
func knowledgeFastStamps(ctx context.Context, cfg config.Config, root, cacheDir string, tree *knowledge.TreeWalk, want []knowledge.ShardClass, reads readlog.Reads, known bool) knowledge.Stamps {
	out := knowledge.Stamps{}
	if tree == nil {
		return out
	}
	if slices.Contains(want, knowledge.ClassDomain) {
		vcsHead := vcsInputFingerprint(ctx, cfg, root)
		historyKnown := vcsHead != "" || !cfg.Knowledge.VCS.Enabled
		ignoreSources, ignoreKnown := vcsIgnoreSources(ctx, root)
		h := knowledge.NewInputHash("fast:" + string(knowledge.ClassDomain))
		if historyKnown && ignoreKnown && foldKnowledgeSite(h, cfg, root, cacheDir) {
			h.String(tree.Digest())
			h.String(vcsHead)
			for _, p := range ignoreSources {
				h.Path(p)
			}
			h.Dirs(filepath.Join(cacheDir, "providers"))
			foldEvaluationReads(h, reads, known)
			for _, s := range []struct {
				scope    notes.Scope
				declared string
			}{{notes.ScopeShared, cfg.Knowledge.Notes.Shared}, {notes.ScopePrivate, cfg.Knowledge.Notes.Private}} {
				if dir, err := notes.Dir(root, s.scope, s.declared); err == nil {
					h.Path(dir)
				}
			}
			out[knowledge.ClassDomain] = h.Sum()
		}
	}
	if slices.Contains(want, knowledge.ClassRuntime) {
		h := knowledge.NewInputHash("fast:" + string(knowledge.ClassRuntime))
		if foldKnowledgeSite(h, cfg, root, cacheDir) {
			h.Path(knowledge.RuntimeRecordsPath(cacheDir))
			if cfg.HistoryPath != "" {
				h.Path(cfg.HistoryPath)
			}
			h.Dirs(filepath.Join(cacheDir, "outputs"))
			out[knowledge.ClassRuntime] = h.Sum()
		}
	}
	return out
}

// loadKnowledgePackages reads each project's third-party dependencies out of the
// manifests ProjectEntry already resolved, for the @packages shard.
//
// It reads ProjectEntry rather than reaching back into the spell registry because the
// existence check has already been done: ProjectEntry.Manifests holds the candidates
// that actually exist in the project's directory, in declared order. There is nothing
// to re-derive here, only a file to read.
//
// deps.Readers says which manifests are read and with which lockfiles. go.mod states
// exact versions and is read alone; every other manifest holds ranges and is read with
// the lockfile ProjectEntry.Lockfiles resolved for it, which is workspace-relative
// because a lock may be hoisted above the project. Best-effort throughout: an
// unreadable or unparsable file contributes no packages rather than failing the build.
// A manifest that needs a lock and yields nothing gets one info line per project, so a
// missing or unread lockfile is visible rather than an empty inventory.
func loadKnowledgePackages(ctx context.Context, projects types.ProjectsOutput, log *slog.Logger) map[string][]types.KnowledgePackage {
	out := map[string][]types.KnowledgePackage{}
	for _, p := range projects.Projects {
		if p.Dir == "" {
			continue
		}
		var unread []string
		for _, manifest := range p.Manifests {
			r, ok := deps.Readers[manifest]
			if !ok {
				continue
			}
			lock := ""
			for _, l := range p.Lockfiles {
				if r.UnderstandsLock(l) {
					lock = filepath.Join(projects.Workspace, filepath.FromSlash(l))
					break
				}
			}
			pkgs := r.Read(filepath.Join(p.Dir, manifest), lock)
			out[p.Path] = append(out[p.Path], pkgs...)
			if len(r.Locks) == 0 || len(pkgs) > 0 {
				continue
			}
			if lock != "" {
				unread = append(unread, fmt.Sprintf("%s read, %s pinned none of its dependencies (a format this reader does not understand, or none declared); no %s nodes for %s",
					manifest, filepath.Base(lock), r.Manager, p.Path))
			} else {
				unread = append(unread, fmt.Sprintf("%s read, no lockfile it understands (%s); no %s nodes for %s",
					manifest, strings.Join(r.Locks, ", "), r.Manager, p.Path))
			}
		}
		if len(out[p.Path]) == 0 {
			delete(out, p.Path)
		}
		if len(unread) > 0 {
			log.InfoContext(ctx, "knowledge: a manifest yielded no package versions",
				slog.String("project", p.Path), slog.String("detail", strings.Join(unread, "; ")))
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
		// Keyed "<spell>/<target>" here and by the bare target in the graph, so emitting
		// the raw key minted a node id nothing matched and every timing attr was dropped.
		for _, target := range bareTargetNames(targets) {
			st, ok := h.FoldTargetHistories(project, target)
			if !ok {
				continue
			}
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
// coverage for the observed @coverage overlay. The profile is .magus/coverage.out at the
// workspace root (what `magus run test .` writes), and its lines are module-qualified,
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
		return nil // not declared, or declared badly: notes verify says so
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
	// no relative path and no attribution to be had: the absolute path is the honest
	// Source, and a blank author is the honest answer rather than a fabricated one.
	//
	// Taken from each note's own file rather than rebuilt from its name: a note that
	// declares an id is identified by that id and not by where the file sits, so a rebuilt
	// path stops naming a real file the moment someone renames the note, and @vcs then
	// attributes nothing, because no such path was ever committed.
	relPath := func(p string) string {
		r, rerr := filepath.Rel(root, p)
		if rerr != nil || strings.HasPrefix(r, "..") {
			return filepath.ToSlash(p)
		}
		return filepath.ToSlash(r)
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
			Path:    relPath(n.Path),
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
	profile, err := os.ReadFile(filepath.Join(root, ".magus", "coverage.out"))
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

// loadKnowledgeAgentContacts reads the per-repo session store for the agent events
// `magus session load` folded into it, reduced to the path contacts the @session overlay
// counts. It resolves the store through sessions.Dir, the same repo-identity keying
// `magus session ls` uses, so every worktree of a repo sees one history. It reads the
// agent-event fold alone, which the store caches per kind and refreshes only for the
// invocation files that changed, so a graph build never decodes the whole store.
//
// Best-effort throughout, and deliberately so: no store, an unreadable one, or zero
// agent events all yield no contacts, so a workspace that has never loaded a transcript
// builds exactly the graph it built before. A record whose payload this build cannot
// decode is skipped rather than failing the graph, which is the same tolerance every
// other reader of the store applies to a kind it does not know.
func loadKnowledgeAgentContacts(root string) []knowledge.AgentContact {
	if root == "" {
		return nil
	}
	dir, err := sessions.Dir(root)
	if err != nil {
		return nil
	}
	fold, err := sessions.ReadAgentEvents(dir)
	if err != nil {
		return nil
	}
	var out []knowledge.AgentContact
	for _, rec := range fold.Records {
		var ev sessions.AgentEvent
		if json.Unmarshal(rec.Payload, &ev) != nil {
			continue
		}
		// A file event carries its path in Text; a shell command's Text is never
		// stored, so it arrives path-less and the assembler counts it as dropped.
		// Denied is the host's own record of a refusal, the only denial a file event
		// carries: the re-judged Verdict exists on shell commands alone, which have
		// no path to land on.
		//
		// compat(until: no session store holds an absolute file path, which `magus
		// session show -o json` would list under files_read or files_written): loads
		// store the checkout-relative path, and only events loaded before they did
		// still need reducing here.
		out = append(out, knowledge.AgentContact{
			// A loaded event is filed under the host session, so that is its invocation id.
			Session: rec.Invocation,
			Path:    vcs.CheckoutRelative(ev.Text),
			Read:    ev.Kind == sessions.EventFileRead,
			Write:   ev.Kind == sessions.EventFileWrite,
			AtMs:    ev.AtMs,
			Denied:  ev.Denied,
		})
	}
	return out
}

// declaredSpellSet is the union of every project's declared `spells:` list, the spells
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
// and records no refs, so the output store (which already persists one OutputDescriptor
// per execution) is the source. A missing or unreadable store yields no refs, so the
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
	return knowledge.NewStore(resolveCacheDir(root, cfg), cacheImmutable(cfg), int64(cfg.Knowledge.MaxSizeMB)*1024*1024, remoteShards(ws), log)
}

// MergeWorkspaceSymbols pulls every persisted per-project @symbols shard into g, for
// a symbol-seeded query (the default graph excludes them for scale), first bringing the
// symbol and coverage classes up to date with their inputs; @session is merged as stored
// (see knowledge.SymbolClasses). No store or no symbol shards merges nothing.
func MergeWorkspaceSymbols(ctx context.Context, ws types.Inspector, root string, cfg config.Config, g *knowledge.Graph, log *slog.Logger) error {
	if _, err := ensureKnowledgeGraph(ctx, ws, root, cfg, false, knowledge.SymbolClasses, log); err != nil {
		return err
	}
	return symbolStore(ws, root, cfg, log).MergeSymbolShards(ctx, g)
}

// QueryKnowledgeGraph answers input the way BuildKnowledgeGraph, then MergeWorkspaceSymbols,
// then Graph.Query would, byte for byte, without decoding every symbol shard: matches are
// ranked from the store's names sidecar, and only the shards the answer's neighborhood
// touches are read (see knowledge.Store.QuerySymbols). It returns the answer and the graph
// the matches were ranked over, which holds every node a near-miss suggestion searches.
func QueryKnowledgeGraph(ctx context.Context, ws types.Inspector, root string, cfg config.Config, refresh bool, input string, budget int, log *slog.Logger) (types.KnowledgeQueryOutput, *knowledge.Graph, error) {
	g, err := BuildKnowledgeGraph(ctx, ws, root, cfg, refresh, log)
	if err != nil {
		return types.KnowledgeQueryOutput{}, nil, err
	}
	if _, err := ensureKnowledgeGraph(ctx, ws, root, cfg, false, knowledge.SymbolClasses, log); err != nil {
		return types.KnowledgeQueryOutput{}, nil, err
	}
	return symbolStore(ws, root, cfg, log).QuerySymbols(ctx, g, input, budget)
}

// MergeWorkspaceSymbolsForRef merges symbols into g for `magus refs`, targeting only
// the shards that mention ref (via the xref routing index) when ref is an exact symbol
// ID (the scale-safe reverse lookup), or all symbol shards when ref is a fuzzy name
// whose exact ID is not yet known.
func MergeWorkspaceSymbolsForRef(ctx context.Context, ws types.Inspector, root string, cfg config.Config, g *knowledge.Graph, ref string, log *slog.Logger) error {
	if _, err := ensureKnowledgeGraph(ctx, ws, root, cfg, false, knowledge.SymbolClasses, log); err != nil {
		return err
	}
	store := symbolStore(ws, root, cfg, log)
	// An exact symbol ID can route to just its shards; a fuzzy name (or any non-exact
	// symbol: ref) yields no routing hit and MergeSymbolShardsByID falls back to loading
	// all, so the fuzzy resolve still has every symbol to match against.
	if strings.HasPrefix(ref, types.KindSymbol+":") {
		return store.MergeSymbolShardsByID(ctx, g, []string{ref})
	}
	// A bare name routes to the shards of every symbol labeled exactly that. They hold
	// each such symbol with every edge into it, which is what refs answers from, and every
	// candidate refs weighs when the name is ambiguous. That holds only when one of them is
	// defined here, since refs then picks among them by definition; otherwise refs falls
	// back to ranking the name against every symbol, and ranking needs them all.
	if names := store.SymbolShardsForLabel(ref); len(names) > 0 {
		routed := knowledge.NewGraph()
		if err := store.MergeSymbolShardsNamed(ctx, routed, names); err != nil {
			return err
		}
		if len(routed.SymbolsNamed(ref)) > 0 {
			knowledge.UnionInto(g, routed)
			return nil
		}
	}
	return store.MergeSymbolShards(ctx, g)
}

// loadKnowledgeSymbols reads each project's SCIP index (best-effort) into per-project
// symbol records for the @symbols shards. Ingestion is AUTOMATIC: every project bound to
// a symbol-capable spell (one exposing the reserved `scip` op) is read from that
// project's cached index, so importing a language's spells is the only opt-in: no
// per-project config. The index lives under the cache dir, not the tree: `magus run
// <project>::scip` produces it there. An index that has not been built yet (its scip
// target has not run) or an unreadable/undecodable one is skipped with a debug log,
// never an error: symbol ingestion is optional enrichment, so a bad index degrades to
// "no symbols for that project" rather than failing every graph query.
func loadKnowledgeSymbols(ctx context.Context, in symbolIngestInputs) map[string][]types.KnowledgeSymbol {
	log := in.log
	decls := symbolIndexDeclarations(ctx, in)
	if len(decls) == 0 {
		return nil
	}
	out := map[string][]types.KnowledgeSymbol{}
	for _, decl := range decls {
		syms, err := parseSymbolIndexCached(ctx, in, decl)
		var decodeErr symbolDecodeError
		switch {
		case errors.As(err, &decodeErr):
			// An index that exists but will not decode is a real problem (corrupt output),
			// not a benign miss; surface it.
			log.WarnContext(ctx, "knowledge: cannot decode symbol index", slog.String("project", decl.project), slog.String("index", decl.path), slog.String("error", err.Error()))
			continue
		case errors.Is(err, fs.ErrNotExist):
			// A not-yet-built index (the scip target has not run) is expected and quiet.
			log.DebugContext(ctx, "knowledge: symbol index not built yet, skipping", slog.String("project", decl.project), slog.String("index", decl.path))
			continue
		case err != nil:
			// Any other read error (permissions) is a misconfig worth surfacing.
			log.WarnContext(ctx, "knowledge: cannot read symbol index", slog.String("project", decl.project), slog.String("index", decl.path), slog.String("error", err.Error()))
			continue
		}
		symbols.FingerprintBodies(in.root, syms)
		out[decl.project] = syms
	}
	return out
}

// symbolDecodeError marks an index that was read but would not parse, which callers
// report apart from one that could not be read.
type symbolDecodeError struct{ err error }

func (e symbolDecodeError) Error() string { return e.err.Error() }
func (e symbolDecodeError) Unwrap() error { return e.err }

// symbolIndexCache locates the two things derived from one SCIP index and kept until the
// index file moves: its parse (the graph's symbol records) and its occurrence file (every
// symbol's exact sites, for `refs --occurrences`). A large module's index is a hundred
// megabytes whose decode allocates over a gigabyte, and it changes only when its scip op
// runs, while the symbols built from it are reassembled after every source edit and a
// refs lookup wants one symbol's sites.
type symbolIndexCache struct {
	// key identifies what produced both: this binary, the project and language the parse
	// was run for, and the index file. Empty when the binary cannot be located, and then
	// nothing is cached.
	key        string
	parsedPath string
	occPath    string
}

func symbolIndexCacheFor(in symbolIngestInputs, decl resolvedSymbolIndex) symbolIndexCache {
	var c symbolIndexCache
	h := knowledge.NewInputHash("symbol index caches")
	if foldBinary(h) {
		h.String(decl.project)
		h.String(decl.language)
		h.Path(decl.path)
		c.key = h.Sum()
	}
	sum := sha256.Sum256([]byte(decl.path))
	stem := filepath.Join(knowledge.StoreDir(in.cacheDir), "inputs", "scip", hex.EncodeToString(sum[:8]))
	c.parsedPath, c.occPath = stem+".gob", stem+".occ"
	return c
}

// decodeSymbolIndex reads and decodes decl's index once and derives both cached forms from
// that one decode, writing them for the next reader: whichever of the parse or the
// occurrences a process needs first, it never decodes the index a second time for the other.
func decodeSymbolIndex(ctx context.Context, in symbolIngestInputs, decl resolvedSymbolIndex, c symbolIndexCache) ([]types.KnowledgeSymbol, map[string]symbols.KeyOccurrences, error) {
	data, err := os.ReadFile(decl.path)
	if err != nil {
		return nil, nil, err
	}
	idx, err := symbols.DecodeIndex(data)
	if err != nil {
		return nil, nil, symbolDecodeError{err}
	}
	syms := symbols.ParseDecoded(ctx, idx, decl.project, decl.language)
	occ, err := symbols.IndexOccurrences(ctx, idx, decl.project)
	if err != nil {
		return nil, nil, err
	}
	if c.key != "" && !cacheImmutable(in.cfg) {
		err := writeParsedSymbolIndex(c.parsedPath, c.key, syms)
		if err == nil {
			err = symbols.WriteOccurrenceFile(c.occPath, c.key, occ)
		}
		if err != nil {
			in.log.DebugContext(ctx, "knowledge: caching a decoded symbol index failed", slog.String("index", decl.path), slog.String("error", err.Error()))
		}
	}
	return syms, occ, nil
}

// parseSymbolIndexCached returns decl's parse, from the cache while the index is unmoved.
// The cache holds the parse alone: FingerprintBodies reads the working tree into the
// records afterwards, and the tree moves without the index.
func parseSymbolIndexCached(ctx context.Context, in symbolIngestInputs, decl resolvedSymbolIndex) ([]types.KnowledgeSymbol, error) {
	if _, err := os.Stat(decl.path); err != nil {
		return nil, err
	}
	c := symbolIndexCacheFor(in, decl)
	if c.key != "" {
		if syms, ok := readParsedSymbolIndex(c.parsedPath, c.key); ok {
			return syms, nil
		}
	}
	syms, _, err := decodeSymbolIndex(ctx, in, decl, c)
	return syms, err
}

// symbolKeyOccurrences returns key's sites in decl's index, from the occurrence file while
// the index is unmoved.
func symbolKeyOccurrences(ctx context.Context, in symbolIngestInputs, decl resolvedSymbolIndex, key string) (symbols.KeyOccurrences, error) {
	if _, err := os.Stat(decl.path); err != nil {
		return symbols.KeyOccurrences{}, err
	}
	c := symbolIndexCacheFor(in, decl)
	if c.key != "" {
		occ, err := symbols.ReadKeyOccurrences(c.occPath, c.key, key)
		if err == nil {
			return occ, nil
		}
		if !errors.Is(err, symbols.ErrOccurrenceFileStale) {
			in.log.DebugContext(ctx, "knowledge: occurrence file unreadable, decoding the index", slog.String("index", decl.path), slog.String("error", err.Error()))
		}
	}
	_, occ, err := decodeSymbolIndex(ctx, in, decl, c)
	if err != nil {
		return symbols.KeyOccurrences{}, err
	}
	return occ[key], nil
}

// The cached parse is gob: the key first, so a stale file is rejected before its records
// are decoded, then the records. gob because this is a private cache read back only by the
// binary that wrote it, which the key names.
func readParsedSymbolIndex(path, key string) ([]types.KnowledgeSymbol, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	dec := gob.NewDecoder(bufio.NewReaderSize(f, 1<<20))
	var got string
	if dec.Decode(&got) != nil || got != key {
		return nil, false
	}
	var syms []types.KnowledgeSymbol
	if dec.Decode(&syms) != nil {
		return nil, false
	}
	return syms, true
}

func writeParsedSymbolIndex(path, key string, syms []types.KnowledgeSymbol) error {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(key); err != nil {
		return err
	}
	if err := enc.Encode(syms); err != nil {
		return err
	}
	return file.WriteFileAtomic(path, buf.Bytes(), 0o644)
}

// foldBinary folds the running magus binary's identity into h, reporting false when the
// binary cannot be located, in which case nothing keyed on it may be trusted.
func foldBinary(h *knowledge.InputHash) bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	h.Binary(exe)
	return true
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
// Freshness is out of scope here, but no longer unreachable: an index that is merely STALE
// is a different fact from one that could not be read, and SymbolIndexStatus answers it
// from the cache. A gap says the evidence is missing; staleness says it is behind.
func SymbolGaps(ctx context.Context, ws types.Inspector, root string, cfg config.Config, log *slog.Logger) (gaps []types.KnowledgeSymbolGap, ok bool) {
	if log == nil {
		log = slog.Default()
	}
	if lw, isLazy := ws.(*LazyWorkspace); isLazy && !lw.Opened() && lw.root == root {
		// The read this probe follows was answered without evaluating the workspace; the
		// declarations it recorded then answer the probe the same way. A store that cannot
		// vouch for them opens the workspace below, as every probe did before.
		if decls, recorded := recordedSymbolIndexDeclarations(ctx, lw, cfg); recorded {
			return probeSymbolIndexes(decls), true
		}
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

// SymbolIndexedAt reports when the cached SCIP index of the project at projectAbsDir was
// written under cacheDir, and false when there is none. A knowledge.symbols override that
// points at an index in the tree is not consulted.
func SymbolIndexedAt(cacheDir, projectAbsDir string) (time.Time, bool) {
	info, err := os.Stat(symbols.IndexPath(cacheDir, projectAbsDir))
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}

// SymbolOccurrences returns every exact source range where the symbol keyed by key
// appears, with each range verified against the file on disk. It reads the SAME declared
// indexes the graph is built from, so it can never disagree with `magus refs` about which
// projects were searched, but it goes back to the index rather than to the graph, because
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
// names[0] is the identifier a rename targets. An empty set verifies nothing (see
// symbols.Verify), which is the conservative outcome for an index that names the symbol
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
	// completeness, under a verdict that says magus searched everywhere, so it is recorded
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
		found, err := symbolKeyOccurrences(ctx, in, decl, key)
		var decodeErr symbolDecodeError
		switch {
		case errors.Is(err, fs.ErrNotExist):
			// A not-yet-built index is the expected case, and SymbolGaps already reports it
			// from its own Stat, so it stays quiet here rather than being counted twice.
			continue
		case errors.As(err, &decodeErr):
			log.WarnContext(ctx, "knowledge: cannot decode symbol index", slog.String("project", decl.project), slog.String("index", decl.path), slog.String("error", err.Error()))
			gap(decl.project, "does not decode")
			continue
		case ctx.Err() != nil:
			gap(decl.project, "not read: cancelled")
			continue
		case err != nil:
			// Any OTHER read error is a hole SymbolGaps cannot see.
			log.WarnContext(ctx, "knowledge: cannot read symbol index", slog.String("project", decl.project), slog.String("index", decl.path), slog.String("error", err.Error()))
			gap(decl.project, "unreadable")
			continue
		}
		// One index names the symbol; the others may only reference it. Union rather than
		// first-wins, so a spelling that appears in a second project's index is still
		// recognized at that project's occurrences.
		for _, n := range found.Names {
			if !slices.Contains(read.Names, n) {
				read.Names = append(read.Names, n)
			}
		}
		read.Files = append(read.Files, found.Files...)
	}

	// Each index contributes its own files, so the merged list needs re-sorting to stay
	// deterministic across the declaration order, and entries two indexes both produced for
	// one path have to be folded together. Two blocks for one file would double its count
	// and, worse, hand a caller the same edit twice: the once-per-file read guarantee and
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
// dropped on the way: a caller folds it into the coverage gaps, which turns the verdict
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
	return probeSymbolIndexes(symbolIndexDeclarationRecords(ctx, in, nil))
}

// probeSymbolIndexes is the probe itself, over declarations however they were resolved: one Stat
// per declared index, and a gap for each that is not a readable file.
func probeSymbolIndexes(decls []knowledge.SymbolIndexDeclaration) []types.KnowledgeSymbolGap {
	var out []types.KnowledgeSymbolGap
	for _, decl := range decls {
		// Detail carries what Stat can actually distinguish: absent, or present but
		// unreadable. State stays the machine-branchable field and is accurate for both,
		// since neither yields a usable index.
		var detail string
		if _, err := os.Stat(decl.Path); err == nil {
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			detail = "unreadable"
		}
		out = append(out, types.KnowledgeSymbolGap{
			Project: types.NewProjectRef(decl.Project, decl.Dir),
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
// hold a Magus: the pairing SymbolGaps keeps, since the two answers are read together.
func (m *Magus) SymbolOccurrences(ctx context.Context, key string) (SymbolOccurrenceRead, bool) {
	return SymbolOccurrences(ctx, m, m.Root(), m.cfg, slog.Default(), key)
}

// resolvedSymbolIndex pairs a project with the absolute path of its SCIP index and the
// language the spell that produced it adapts.
//
// language is carried because an indexer may not report one. SCIP makes Document.Language
// optional and scip-typescript sets it on nothing, so trusting the index alone leaves
// every TypeScript symbol unlabeled and `magus query language:typescript` empty. It comes
// from the project's spells, which is authoritative and free, and it is resolved for a
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
// derives one for every project bound to a symbol-capable spell (one declaring a
// symbol indexer), pointing at that project's cached index (symbols.IndexPath, the
// same location the op writes to), the zero-config path. Explicit knowledge.symbols
// entries are then merged in and win on the same project, pointing instead at a
// workspace-relative path in the tree for a project whose indexer writes somewhere
// non-standard. The result is sorted by project for deterministic ingestion.
func symbolIndexDeclarations(ctx context.Context, in symbolIngestInputs) []resolvedSymbolIndex {
	capable := map[string]bool{}
	langBySpell := map[string]string{}
	for _, sp := range in.spells {
		langBySpell[sp.Name] = sp.Language
		if sp.SymbolFormat != "" {
			capable[sp.Name] = true
		}
	}
	// The language a project's symbols are written in, resolved from its spells rather
	// than from the index: SCIP makes Document.Language optional and scip-typescript sets
	// it on nothing. Prefer the symbol-capable spell, but fall back to any bound spell so
	// a knowledge.symbols OVERRIDE (which names a path, not a spell, and so never reaches
	// the capable branch below) still labels its symbols.
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
	changes, err := res.VCS.ChangesByCommit(ctx, root, vcsMaxCommits(cfg), "")
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
// A hit and a miss must be indistinguishable downstream (every consumer gets the same full
// slice either way), which is the property that makes caching this safe at all.
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
	// real HEAD, so a transient git failure would persist as an answer.
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
// Uncommitted work is deliberately excluded: the scan reads committed history only, so
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
// symlink representation and filepath.Rel stays clean: the driver's root can be
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
		for _, ch := range c.Files {
			// The @vcs shard describes files as they are NOW, so a rename's Path (the
			// name after the commit) is the only side that can match a node. PrevPath
			// is lineage, which FileHotspots reassembles; here it would name a path
			// that no longer exists.
			f := filepath.ToSlash(strings.TrimSpace(ch.Path))
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

// remoteShardAdapter rides the build cache's remote tier as a knowledge.RemoteShards: a
// shard is content-addressed by fingerprint, stored under a fixed namespace, signed on
// the way out and verified on the way in by the cache's trust set, and written only by
// a run that may write the remote tier.
type remoteShardAdapter struct{ ns *cache.RemoteNamespace }

func (a remoteShardAdapter) GetShard(ctx context.Context, key string) (io.ReadCloser, error) {
	rc, err := a.ns.Get(ctx, key)
	if errors.Is(err, cache.ErrRemoteMiss) {
		return nil, knowledge.ErrShardMiss
	}
	return rc, err
}

func (a remoteShardAdapter) PutShard(ctx context.Context, key string, r io.Reader) error {
	return a.ns.Put(ctx, key, r)
}

// publishedShards reads shards out of a published OCI artifact: the read-only half of the
// shard backing, for a collaborator or a fresh worktree that has the repository but not
// the build cache behind it.
//
// Shards are addressed by the content fingerprint the store keys them on, which is the
// title of the layer carrying them. That is what makes the fetch incremental: the reader
// asks for the shards it lacks, not for a merged graph it would have to take whole.
type publishedShards struct {
	client       *oci.Client
	ref          oci.Reference
	artifactType string
	log          *slog.Logger

	// The artifact is opened once per instance: a load asks for many shards and the token
	// exchange plus manifest GET answers the same way every time. The first caller's ctx
	// governs that open, so a cancelled first call leaves this handle permanently missing,
	// which is the same outcome as an unreachable registry and is handled identically.
	once sync.Once
	art  *oci.Artifact
}

// PublishedShards returns a read-only knowledge.RemoteShards over the artifact at ref.
// Nothing is fetched until a shard is asked for.
//
// Every read failure is a MISS: no artifact, no such tag, a private package, no network,
// no layer with that key. The store's answer to a miss is to build the shard locally,
// which is what would have happened anyway, so a registry nobody can reach must never
// turn into a failed command. Real failures are logged at debug and dropped.
func PublishedShards(c *oci.Client, ref oci.Reference, artifactType string, log *slog.Logger) knowledge.RemoteShards {
	if log == nil {
		log = slog.Default()
	}
	return &publishedShards{client: c, ref: ref, artifactType: artifactType, log: log}
}

func (p *publishedShards) GetShard(ctx context.Context, key string) (io.ReadCloser, error) {
	p.once.Do(func() {
		art, err := p.client.Artifact(ctx, p.ref, p.artifactType)
		if err != nil {
			p.log.DebugContext(ctx, "knowledge: published graph unreadable",
				slog.String("ref", p.ref.String()), slog.String("error", err.Error()))
			return
		}
		p.art = art
	})
	if p.art == nil {
		return nil, knowledge.ErrShardMiss
	}
	b, err := p.art.Layer(ctx, key)
	if err != nil {
		if !errors.Is(err, oci.ErrLayerMiss) {
			p.log.DebugContext(ctx, "knowledge: published shard fetch failed",
				slog.String("ref", p.ref.String()), slog.String("key", key), slog.String("error", err.Error()))
		}
		return nil, knowledge.ErrShardMiss
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

// PutShard refuses, and says so rather than reporting a write that did not happen.
//
// A registry has no per-blob append: publishing is upload every blob, then PUT one
// manifest naming them all. So a shard cannot join an existing artifact on its own, and
// an adapter that accumulated shards here would be holding a buffer nothing ever flushes
// (the store calls this from the middle of a build, never at the end of one). The batched
// publish is `magus graph push`, which reads the finished store off disk and uploads it as
// a single artifact. The store treats this the way it treats any remote failure: the local
// shard write already succeeded.
func (p *publishedShards) PutShard(context.Context, string, io.Reader) error {
	return errors.New("knowledge: a published graph is republished whole by `magus graph push`, never one shard at a time")
}

// UsePublishedShards installs a read-only shard source on ws, consulted whenever the
// knowledge store reaches for a shard it does not hold. Nothing is fetched here.
//
// A ws that is not a *Magus is ignored: a caller holding some other Inspector has no
// store for this to back.
func UsePublishedShards(ws types.Inspector, r knowledge.RemoteShards) {
	switch w := ws.(type) {
	case *Magus:
		w.publishedShards.Store(&r)
	case *LazyWorkspace:
		w.usePublishedShards(r)
	}
}

// shardChain reads from each backing in order and writes only to the FIRST. The build
// cache is the writable one; a published artifact is republished whole, so a later link
// has nothing to accept (see publishedShards.PutShard).
//
// Any read error moves to the next link and the last one's error is returned, so one
// unreachable backing cannot mask a hit from another.
type shardChain []knowledge.RemoteShards

func (c shardChain) GetShard(ctx context.Context, key string) (io.ReadCloser, error) {
	err := knowledge.ErrShardMiss
	for _, s := range c {
		var rc io.ReadCloser
		rc, err = s.GetShard(ctx, key)
		if err == nil {
			return rc, nil
		}
	}
	return nil, err
}

func (c shardChain) PutShard(ctx context.Context, key string, r io.Reader) error {
	return c[0].PutShard(ctx, key, r)
}

// remoteShards returns the shard backing for a workspace: the build cache's remote
// backend when ws is a cache-backed *Magus, then whatever UsePublishedShards installed.
// nil means local-only, which is what an Inspect-constructed *Magus with no published ref
// gets, because it has no cache either.
func remoteShards(ws types.Inspector) knowledge.RemoteShards {
	if lw, ok := ws.(*LazyWorkspace); ok {
		return lazyRemoteShards{lw}
	}
	m, ok := ws.(*Magus)
	if !ok {
		return nil
	}
	var chain shardChain
	if m.cache != nil {
		if ns := m.cache.RemoteNamespace(knowledgeRemoteNamespace); ns != nil {
			chain = append(chain, remoteShardAdapter{ns})
		}
	}
	if p := m.publishedShards.Load(); p != nil {
		chain = append(chain, *p)
	}
	switch len(chain) {
	case 0:
		return nil
	case 1:
		return chain[0] // one backing answers for itself; the chain would only add a hop
	default:
		return chain
	}
}

// warmKnowledgeGraph returns this handle's lazily-created warm-graph holder. The
// rebuild closure is the cache-first build the CLI runs, over every class rather than the
// default ones: callers read the store behind this graph directly (SymbolIndexDigest
// after KnowledgeGraph), so the lazy classes must be current too. The holder adds an
// in-memory cache that is trusted only while WatchKnowledgeGraph has a watcher
// invalidating it.
func (m *Magus) warmKnowledgeGraph() *warmGraph {
	m.warmGraphOnce.Do(func() {
		root := m.Root()
		cfg := m.cfg
		m.warmGraph = newWarmGraph(func(ctx context.Context, refresh bool) (*knowledge.Graph, error) {
			return ensureKnowledgeGraph(ctx, m, root, cfg, refresh, knowledge.AllClasses, slog.Default())
		}, slog.Default())
	})
	return m.warmGraph
}

// KnowledgeGraph returns the workspace knowledge graph. In the server, once
// WatchKnowledgeGraph is running, this answers from a warm in-memory graph without
// re-parsing magusfiles; otherwise (and on refresh) it rebuilds cache-first. It is
// always fresh: the warm graph is served only while a watcher can invalidate it.
func (m *Magus) KnowledgeGraph(ctx context.Context, refresh bool) (*knowledge.Graph, error) {
	return m.warmKnowledgeGraph().Get(ctx, refresh)
}

// KnowledgeGraphWithSymbols returns a graph that INCLUDES the lazily-loaded @symbols
// shards, for a symbol-seeded MCP query (magus\query on symbols, magus\refs). It
// builds cache-first into a FRESH graph (not the shared warm graph) and merges
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

// WriteGuardIndex writes the file a guard hook answers graph questions from (see
// knowledge.WriteGuardIndex), from a cache-first graph with symbols merged. Called where
// indexes are rebuilt: `magus graph build` and the server's auto-indexer.
func (m *Magus) WriteGuardIndex(ctx context.Context) error {
	at := knowledge.ReadGuardCheckout(ctx, m.Root())
	g, err := m.KnowledgeGraphWithSymbols(ctx)
	if err != nil {
		return err
	}
	fresh := !slices.ContainsFunc(m.SymbolIndexStatus(ctx), func(s types.SymbolIndexStatus) bool {
		return s.Freshness == types.SymbolIndexStale || s.Freshness == types.SymbolIndexUnvouched
	})
	return knowledge.WriteGuardIndex(resolveCacheDir(m.Root(), m.cfg), m.Root(), g, fresh, at)
}

// KnowledgeGraphWithSymbolsForRef is KnowledgeGraphWithSymbols for magus\refs: it
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
// fresh, so server MCP calls answer from memory. It returns a stop function; the
// long-lived server calls it once at startup. A one-shot CLI never calls it and
// pays the cache-first rebuild per command (equally fresh, just not warm).
func (m *Magus) WatchKnowledgeGraph(ctx context.Context) (func(), error) {
	return m.warmKnowledgeGraph().watch(ctx, m.Root())
}

// bareTargetNames lists the target names behind a project's history keys, deduplicated
// and sorted. The history keys a target as "<spell>/<target>"; everything outside that
// package names the bare target a magusfile declares.
func bareTargetNames(targets map[string]forecast.Stats) []string {
	seen := make(map[string]bool, len(targets))
	for key := range targets {
		name := key
		if i := strings.LastIndex(key, "/"); i >= 0 {
			name = key[i+1:]
		}
		seen[name] = true
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}
