package magus

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/host"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/knowledge"
	"github.com/egladman/magus/types"
)

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
// graph. It is the single graph-loading path shared by `magus describe knowledge`,
// the query/explain/path verbs, and the MCP tools: it gathers the describe
// outputs the graph is composed from, resolves the cache dir, and runs the
// cache-first build. ws is any workspace view that can describe itself (the
// read-only Inspect result or a full *Magus).
func BuildKnowledgeGraph(ctx context.Context, ws types.Describer, root string, cfg config.Config, refresh bool, log *slog.Logger) (*knowledge.Graph, error) {
	in := knowledge.Inputs{
		Graph:       ws.DescribeGraph(),
		Spells:      ws.DescribeSpells(),
		Modules:     allModuleEntries(),
		Diagnostics: types.AllDiagnosticCodes(),
	}
	return knowledge.Build(ctx, resolveCacheDir(root, cfg), knowledge.BuildOptions{
		Immutable: cacheImmutable(cfg),
		Refresh:   refresh,
	}, in, log)
}
