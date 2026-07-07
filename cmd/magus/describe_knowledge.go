package main

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/host"
	"github.com/egladman/magus/internal/knowledge"
	"github.com/egladman/magus/types"
)

// describeKnowledge implements `magus describe knowledge`: it assembles the
// deterministic knowledge graph (projects, targets, spells, ops, charms,
// modules, methods, diagnostics), persists it as fingerprinted shards under
// <cache>/knowledge, and emits the merged node-link graph. The cache-first
// loader makes building implicit - there is no separate build verb.
func describeKnowledge(ctx context.Context, root string, args []string) error {
	_, err := cmdParse("describe knowledge", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus describe knowledge [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.KnowledgeGraphDefinition)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Emits the merged node-link graph (-o json for external graph tools).")
			fmt.Fprintln(os.Stderr, "The graph is cache-backed under <cache>/knowledge; only shards whose")
			fmt.Fprintln(os.Stderr, "sources changed are rebuilt.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	g, err := loadKnowledgeGraph(ctx, root, false)
	if err != nil {
		return err
	}
	out := g.Output()

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	case outputName:
		for _, n := range out.Nodes {
			fmt.Println(n.ID)
		}
		return nil
	}

	// text / wide: a routing summary, not a data dump - counts by kind and relation.
	fmt.Printf("definition: %s\n\n", out.Definition)
	fmt.Printf("knowledge graph (schema v%d): %d nodes, %d edges\n\n", out.SchemaVersion, out.NodeCount, out.EdgeCount)
	fmt.Println("nodes by kind:")
	for _, kv := range countBy(len(out.Nodes), func(i int) string { return out.Nodes[i].Kind }) {
		fmt.Printf("  %-11s %d\n", kv.key, kv.n)
	}
	fmt.Println("\nedges by relation:")
	for _, kv := range countBy(len(out.Links), func(i int) string { return out.Links[i].Relation }) {
		fmt.Printf("  %-11s %d\n", kv.key, kv.n)
	}
	fmt.Println("\nRun with -o json for the full node-link graph (external graph tools read it directly).")
	return nil
}

// loadKnowledgeGraph gathers the workspace inputs and runs the cache-first build,
// returning the merged in-memory graph. Shared by describe knowledge and the
// query/explain/path verbs so they all sit on one substrate.
func loadKnowledgeGraph(ctx context.Context, root string, refresh bool) (*knowledge.Graph, error) {
	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return nil, err
	}
	wsRoot := ws.Root()
	in := knowledge.Inputs{
		Graph:       ws.DescribeGraph(),
		Spells:      ws.DescribeSpells(),
		Modules:     allModuleEntries(),
		Diagnostics: types.AllDiagnosticCodes(),
	}
	return knowledge.Build(ctx, resolveCacheDir(wsRoot), knowledge.BuildOptions{
		Immutable: cacheImmutable(),
		Refresh:   refresh,
	}, in, slog.Default())
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

// resolveCacheDir mirrors magus.go's cache-dir resolution (config Cache.Dir, then
// MAGUS_CACHE_DIR, then <root>/.magus) so the knowledge store sits beside the
// build cache. There is no exported getter for the resolved dir.
func resolveCacheDir(root string) string {
	dir := filepath.Join(root, ".magus")
	if globalCfg.Cache.Dir != "" {
		if filepath.IsAbs(globalCfg.Cache.Dir) {
			return filepath.Clean(globalCfg.Cache.Dir)
		}
		return filepath.Join(root, globalCfg.Cache.Dir)
	}
	if ov := os.Getenv("MAGUS_CACHE_DIR"); ov != "" {
		if filepath.IsAbs(ov) {
			return filepath.Clean(ov)
		}
		return filepath.Join(root, ov)
	}
	return dir
}

// cacheImmutable reports whether MAGUS_CACHE_IMMUTABLE is set, matching the cache
// package's convention (load-only, no writes).
func cacheImmutable() bool {
	v := strings.ToLower(os.Getenv("MAGUS_CACHE_IMMUTABLE"))
	return v == "true" || v == "1"
}

type keyCount struct {
	key string
	n   int
}

// countBy tallies n items by the key each index maps to, returning counts sorted
// by key for stable output.
func countBy(n int, keyOf func(i int) string) []keyCount {
	m := map[string]int{}
	for i := 0; i < n; i++ {
		m[keyOf(i)]++
	}
	out := make([]keyCount, 0, len(m))
	for k, v := range m {
		out = append(out, keyCount{k, v})
	}
	slices.SortFunc(out, func(a, b keyCount) int { return cmp.Compare(a.key, b.key) })
	return out
}
