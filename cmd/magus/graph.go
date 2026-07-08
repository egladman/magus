package main

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"

	magus "github.com/egladman/magus"
	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/knowledge"
	"github.com/egladman/magus/internal/render"
	"github.com/egladman/magus/types"
)

// `magus graph` is the graph meta-home: the workspace's graphs as objects.
// query/explain/path READ the knowledge graph (daily retrieval verbs); graph
// owns the graph ITSELF - emit the project dependency DAG (deps), export the
// merged knowledge graph for external tools (export), and report its shape
// (stats). One home instead of surfaces scattered across describe and insight.

var graphSubs = []string{"deps", "export", "stats", "open", "verify"}

func graphCmd(ctx context.Context, root string, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		graphUsage()
		return flag.ErrHelp
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "deps":
		return graphDeps(ctx, root, rest)
	case "export":
		return graphExport(ctx, root, rest)
	case "stats":
		return graphStats(ctx, root, rest)
	case "open":
		return graphOpen(ctx, root, rest)
	case "verify":
		return graphVerify(ctx, root, rest)
	default:
		fmt.Fprintf(os.Stderr, "magus graph: unknown subcommand %q\n", sub)
		if sug := interactive.SuggestNearest(sub, graphSubs); sug != "" {
			interactive.Emit(os.Stderr, fmt.Sprintf("did you mean %q?", sug))
		}
		fmt.Fprintln(os.Stderr, "")
		graphUsage()
		return errSilent{exitCode: 2}
	}
}

func graphUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus graph <subcommand> [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "The workspace's graphs as objects: emit, export, and measure them.")
	fmt.Fprintln(os.Stderr, "(query/explain/path read the knowledge graph; graph is the graph itself.)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Subcommands:")
	fmt.Fprintln(os.Stderr, "  deps     project dependency DAG (-o text|json|yaml|dot|mermaid|tree)")
	fmt.Fprintln(os.Stderr, "  export   merged knowledge graph (-o json|graphml; --select for a dot|mermaid neighborhood)")
	fmt.Fprintln(os.Stderr, "  stats    knowledge-graph shape: god nodes, orphans, doc coverage (--kind to scope)")
	fmt.Fprintln(os.Stderr, "  open     open this workspace's graph in the hosted explorer (delivered privately; data never leaves your machine)")
	fmt.Fprintln(os.Stderr, "  verify   check derived artifacts for drift (installed agent skill vs this binary); CI guard")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "See also: magus query/explain/path (read the graph), magus insight (git-history analytics).")
}

// graphDeps emits the project dependency DAG - the standalone home of the view
// `magus run <target> --graph` and `magus affected <target> --graph` scope to a
// run (those flags remain as scoped passthroughs).
func graphDeps(ctx context.Context, root string, args []string) error {
	var (
		upstream bool
		depth    int
		spell    string
		target   string
	)
	pos, err := cmdParse("graph deps", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&upstream, "upstream", false, "show dependents instead of dependencies")
		fs.IntVar(&depth, "depth", 0, "cap displayed depth (0 = unlimited)")
		fs.StringVar(&spell, "spell", "", "only projects driven by this spell")
		fs.StringVar(&target, "target", "", "target whose duration history annotates nodes (default: build)")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus graph deps [flags] [project...]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Emit the project dependency DAG. A trailing list of project paths roots")
			fmt.Fprintln(os.Stderr, "the graph at those projects; default is the whole workspace. The same")
			fmt.Fprintln(os.Stderr, "view scoped to a run is available as `magus run <target> --graph` and")
			fmt.Fprintln(os.Stderr, "`magus affected <target> --graph`.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return err
	}
	return renderWorkspaceGraph(ctx, ws, graphRenderOptions{
		Upstream: upstream,
		Depth:    depth,
		Spell:    spell,
		Roots:    pos,
		Target:   target,
	})
}

// graphExport emits the merged knowledge graph: it assembles the deterministic
// graph (projects, targets, spells, ops, charms, modules, methods, diagnostics,
// docs, buzz sources), persists it as fingerprinted shards under
// <cache>/knowledge, and writes the node-link export. The cache-first loader
// makes building implicit - there is no separate build verb.
func graphExport(ctx context.Context, root string, args []string) error {
	var (
		refresh     bool
		globalScope bool
		sel         string
		budget      int
	)
	_, err := cmdParse("graph export", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&refresh, "refresh", false, "force a full graph rebuild before exporting")
		fs.BoolVar(&globalScope, "global", false, "union the workspaces registered in config (knowledge.workspaces) into one graph, IDs namespaced by workspace")
		fs.StringVar(&sel, "select", "", "export only the neighborhood of a query (same grammar as `magus query`) instead of the whole graph")
		fs.IntVar(&budget, "budget", knowledge.DefaultBudget, "node budget for --select (how many nodes the neighborhood may collect)")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus graph export [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.KnowledgeGraphDefinition)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Emits the merged graph: -o json for the node-link form, -o graphml for")
			fmt.Fprintln(os.Stderr, "GraphML (Gephi, yEd, and other graph viewers read both directly). The")
			fmt.Fprintln(os.Stderr, "graph is cache-backed under <cache>/knowledge; only shards whose sources")
			fmt.Fprintln(os.Stderr, "changed are rebuilt.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "--select \"<terms>\" narrows the export to a query's neighborhood, sharing")
			fmt.Fprintln(os.Stderr, "the engine behind `magus query`. -o dot and -o mermaid render only with")
			fmt.Fprintln(os.Stderr, "--select: the full graph has too many nodes for those layouts to be legible.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}

	opts, err := ResolveOutput(global.output, outputGraphML, outputDot, outputMermaid)
	if err != nil {
		return err
	}
	// dot/mermaid are graph-layout formats; on the whole graph (1000s of nodes)
	// they are unreadable, so they require a --select neighborhood to scope down.
	if (opts.Format == outputDot || opts.Format == outputMermaid) && sel == "" {
		return fmt.Errorf("-o %s requires --select \"<terms>\" to scope the export; the full graph is too large to lay out (use -o json or -o graphml for the whole graph)", opts.Format)
	}

	// The whole-graph export stays domain-only; a --select neighborhood pulls in the
	// symbol shards only when the selection actually targets symbols.
	g, err := loadKnowledgeGraph(ctx, root, refresh, globalScope, sel != "" && knowledge.SeedsSymbols(sel))
	if err != nil {
		return err
	}
	out := g.Output()
	if sel != "" {
		out = g.Select(sel, budget)
		if out.NodeCount == 0 {
			fmt.Fprintf(os.Stderr, "magus graph export: no nodes matched --select %q\n", sel)
		}
	}
	// The blob base lets a viewer link a node's relative `source` to the right repo.
	// A --global union spans many repos, so a single base would be wrong: leave it off.
	if !globalScope {
		out.SourceBaseURL = deriveSourceBase(ctx, root)
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	case outputGraphML:
		return render.WriteKnowledgeGraphML(os.Stdout, out)
	case outputDot:
		return render.WriteKnowledgeDOT(os.Stdout, out)
	case outputMermaid:
		return render.WriteKnowledgeMermaid(os.Stdout, out)
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
	fmt.Println("\nRun with -o json (node-link) or -o graphml for the full graph.")
	return nil
}

// graphStats reports the knowledge graph's shape: god nodes, orphans, and doc
// coverage. It reads the graph cache-first rather than git history - the
// structural companion to insight's history lenses (insight report embeds it).
func graphStats(ctx context.Context, root string, args []string) error {
	var (
		kind        string
		refresh     bool
		globalScope bool
		withSymbols bool
	)
	_, err := cmdParse("graph stats", args, func(fs *flag.FlagSet) {
		fs.StringVar(&kind, "kind", "", "scope every section to one node kind (e.g. spell, target, doc, diagnostic)")
		fs.BoolVar(&refresh, "refresh", false, "force a full graph rebuild first")
		fs.BoolVar(&globalScope, "global", false, "union the workspaces registered in config (knowledge.workspaces) before computing stats")
		fs.BoolVar(&withSymbols, "symbols", false, "include the lazily-loaded symbol shards in the stats (excluded by default; they can dwarf the domain graph)")
		fs.Usage = func() {
			fmt.Fprintf(os.Stderr, "Usage: magus graph stats [flags]\n\n%s\n\nFlags (global flags also accepted, see `magus -h`):\n", types.KnowledgeStatsDefinition)
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	outOpts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	// Stats stay domain-only unless --symbols (or a --kind symbol scope) opts in.
	g, err := loadKnowledgeGraph(ctx, root, refresh, globalScope, withSymbols || kind == types.KindSymbol)
	if err != nil {
		return err
	}
	out := g.Stats(kind)

	switch outOpts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(outOpts, out)
	case outputName:
		for _, god := range out.Gods {
			fmt.Println(god.ID)
		}
		return nil
	}
	return statsText(out)
}

func statsText(out types.KnowledgeStats) error {
	fmt.Printf("definition: %s\n\n", out.Definition)
	fmt.Printf("graph: %d nodes, %d edges\n\n", out.NodeCount, out.EdgeCount)
	fmt.Println("god nodes (most connected):")
	fmt.Printf("  %6s  %4s  %4s  %-11s  %s\n", "DEGREE", "IN", "OUT", "KIND", "LABEL")
	for _, g := range out.Gods {
		fmt.Printf("  %6d  %4d  %4d  %-11s  %s\n", g.Degree, g.In, g.Out, g.Kind, g.Label)
	}
	if len(out.Orphans) > 0 {
		fmt.Printf("\norphans (%d):\n", len(out.Orphans))
		for _, o := range out.Orphans {
			fmt.Printf("  %-11s  %-26s  %s\n", o.Kind, truncate(o.Label, 26), o.Reason)
		}
	}
	if len(out.Coverage) > 0 {
		fmt.Println("\ndoc coverage:")
		for _, c := range out.Coverage {
			fmt.Printf("  %-11s  %d/%d (%d%%)", c.Kind, c.Documented, c.Total, c.Percent)
			if len(c.Undocumented) > 0 {
				fmt.Printf("  missing: %s", strings.Join(c.Undocumented, ", "))
			}
			fmt.Println()
		}
	}
	return nil
}

// loadKnowledgeGraph gathers the workspace inputs and runs the cache-first build,
// returning the merged in-memory graph. Shared by the graph subcommands and the
// query/explain/path verbs so they all sit on one substrate. When global is set,
// it unions the workspaces registered in config (knowledge.workspaces) with the
// current one, namespacing node IDs by workspace. When includeSymbols is set (a
// symbol-seeded query), the lazily-loaded @symbols shards are merged in on top of
// the default domain graph.
func loadKnowledgeGraph(ctx context.Context, root string, refresh, global, includeSymbols bool) (*knowledge.Graph, error) {
	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return nil, err
	}
	if global {
		// Cross-workspace symbol federation is a later phase; --global stays domain-only.
		return magus.BuildGlobalKnowledgeGraph(ctx, ws, globalCfg, refresh, slog.Default())
	}
	g, err := magus.BuildKnowledgeGraph(ctx, ws, ws.Root(), globalCfg, refresh, slog.Default())
	if err != nil {
		return nil, err
	}
	if includeSymbols {
		if err := magus.MergeWorkspaceSymbols(ctx, ws, ws.Root(), globalCfg, g, slog.Default()); err != nil {
			return nil, err
		}
	}
	return g, nil
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

// ---- project-DAG rendering shared by graph deps, run --graph, and affected --graph ----

type graphRenderOptions struct {
	Upstream bool
	Depth    int
	Spell    string
	Roots    []string
	// Target is the target whose duration history to show (e.g. "build").
	// Falls back to "build" when empty.
	Target string
}

// renderWorkspaceGraph emits the project dependency graph; respects -o (text|json|yaml|dot|mermaid|tree).
func renderWorkspaceGraph(ctx context.Context, ws types.WorkspaceRepository, opts graphRenderOptions) error {
	outOpts, err := ResolveOutput(global.output, outputDot, outputMermaid, outputTree)
	if err != nil {
		return err
	}

	g, err := ws.Graph()
	if err != nil {
		return err
	}

	target := opts.Target
	if target == "" {
		target = "build"
	}

	// Load timing history best-effort; silently skip when unavailable.
	composeOpts := []magus.ComposeOption{magus.WithGraphInput(g)}
	if opts.Upstream {
		composeOpts = append(composeOpts, magus.WithUpstream())
	}
	if opts.Spell != "" {
		composeOpts = append(composeOpts, magus.WithComposeSpell(opts.Spell))
	}
	if len(opts.Roots) > 0 {
		composeOpts = append(composeOpts, magus.WithComposeRoots(opts.Roots...))
	}
	if path := globalCfg.HistoryPath; path != "" {
		var hist forecast.History
		if err := hist.Load(ctx, path); err == nil {
			composeOpts = append(composeOpts, magus.WithGraphHistory(&hist, target))
		}
	}

	switch outOpts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(outOpts, magus.ComposeGraph(ws, composeOpts...))
	case outputName:
		out := magus.ComposeGraph(ws, composeOpts...)
		for _, n := range out.Nodes {
			fmt.Println(n.Path)
		}
		return nil
	case outputDot:
		return render.WriteGraphDOT(os.Stdout, magus.ComposeGraph(ws, composeOpts...))
	case outputMermaid:
		return render.WriteGraphMermaid(os.Stdout, magus.ComposeGraph(ws, composeOpts...))
	}

	// text and tree formats both render the ASCII dependency tree.
	var rOpts []render.RenderOption
	if opts.Upstream {
		rOpts = append(rOpts, render.WithDirection(types.Upstream))
	}
	if opts.Spell != "" {
		rOpts = append(rOpts, render.WithSpell(opts.Spell))
	}
	if opts.Depth != 0 {
		rOpts = append(rOpts, render.WithMaxDepth(opts.Depth))
	}
	if len(opts.Roots) > 0 {
		rOpts = append(rOpts, render.WithRoots(opts.Roots...))
	}
	return render.WriteTree(os.Stdout, g, rOpts...)
}
