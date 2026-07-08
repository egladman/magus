package magus

import (
	"cmp"
	"context"
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
	"github.com/egladman/magus/internal/knowledge"
	"github.com/egladman/magus/internal/symbols"
	"github.com/egladman/magus/types"
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
	cacheDir := resolveCacheDir(root, cfg)
	in := knowledge.Inputs{
		Graph:       ws.DescribeGraph(),
		Spells:      ws.DescribeSpells(),
		Modules:     allModuleEntries(),
		Diagnostics: types.AllDiagnosticCodes(),
		Root:        root,
		Runtime:     knowledge.LoadRuntimeEvents(cacheDir),
		Timings:     loadKnowledgeTimings(ctx, cfg),
		Symbols:     loadKnowledgeSymbols(cfg, root, log),
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

// loadKnowledgeSymbols reads each declared SCIP index (best-effort) into per-project
// symbol records for the @symbols shards. A missing index (its target has not run) or
// an unreadable/undecodable one is skipped with a debug log, never an error - symbol
// ingestion is an optional enrichment, so a bad index degrades to "no symbols for
// that project" rather than failing every graph query.
func loadKnowledgeSymbols(cfg config.Config, root string, log *slog.Logger) map[string][]types.KnowledgeSymbol {
	if len(cfg.Knowledge.Symbols) == 0 {
		return nil
	}
	out := map[string][]types.KnowledgeSymbol{}
	for _, decl := range cfg.Knowledge.Symbols {
		if decl.Project == "" || decl.Index == "" {
			continue
		}
		// The index is declared as a workspace-relative path (typically a target
		// output); reject one that escapes the root rather than reading an arbitrary file.
		if !filepath.IsLocal(decl.Index) {
			log.Warn("knowledge: symbol index path escapes the workspace, skipping", slog.String("project", decl.Project), slog.String("index", decl.Index))
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, decl.Index))
		if err != nil {
			// A not-yet-built index (the target has not run) is expected and quiet; any
			// other read error (typo, permissions) is a misconfig the user asked about.
			if errors.Is(err, fs.ErrNotExist) {
				log.Debug("knowledge: symbol index not built yet, skipping", slog.String("project", decl.Project), slog.String("index", decl.Index))
			} else {
				log.Warn("knowledge: cannot read declared symbol index", slog.String("project", decl.Project), slog.String("index", decl.Index), slog.String("error", err.Error()))
			}
			continue
		}
		syms, err := symbols.ParseIndex(data)
		if err != nil {
			// A declared index that exists but will not decode is a real problem (wrong
			// file, corrupt output), not a benign miss - surface it.
			log.Warn("knowledge: cannot decode symbol index", slog.String("project", decl.Project), slog.String("index", decl.Index), slog.String("error", err.Error()))
			continue
		}
		out[decl.Project] = syms
	}
	return out
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

// WatchKnowledgeGraph starts a file watcher that keeps the warm knowledge graph
// fresh, so daemon MCP calls answer from memory. It returns a stop function; the
// long-lived daemon calls it once at startup. A one-shot CLI never calls it and
// pays the cache-first rebuild per command (equally fresh, just not warm).
func (m *Magus) WatchKnowledgeGraph(ctx context.Context) (func(), error) {
	return m.warmKnowledgeGraph().watch(ctx, m.Root())
}
