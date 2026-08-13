package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"

	magus "github.com/egladman/magus"
	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/httpx"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/interactive/clihint"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/render"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/types"
)

// `magus graph` is the graph meta-home: the workspace's graphs as objects.
// query/explain/path READ the knowledge graph (daily retrieval verbs); graph
// owns the graph ITSELF - emit the project dependency DAG (deps), export the
// merged knowledge graph for external tools (export), and report its shape
// (stats). One home instead of surfaces scattered across describe and insight.

var graphSubs = []string{"build", "deps", "export", "stats", "diff", "verify"}

func graphCmd(ctx context.Context, root string, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		graphUsage()
		return flag.ErrHelp
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "build":
		return graphBuild(ctx, root, rest)
	case "deps":
		return graphDeps(ctx, root, rest)
	case "export":
		return graphExport(ctx, root, rest)
	case "stats":
		return graphStats(ctx, root, rest)
	case "diff":
		return graphDiff(ctx, root, rest)
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
	fmt.Fprintln(os.Stderr, "  build    rebuild the knowledge graph now, reindexing code symbols (runs each project's scip op)")
	fmt.Fprintln(os.Stderr, "  deps     project dependency DAG (-o text|json|yaml|dot|mermaid|tree)")
	fmt.Fprintln(os.Stderr, "  export   merged knowledge graph (-o json|graphml; --select for a dot|mermaid neighborhood)")
	fmt.Fprintln(os.Stderr, "  stats    knowledge-graph shape: god nodes, orphans, doc coverage (--kind to scope)")
	fmt.Fprintln(os.Stderr, "  diff     nodes/edges added/removed/changed vs a baseline export or --rev; PR blast-radius")
	fmt.Fprintln(os.Stderr, "  verify   check derived artifacts for drift (installed agent skill vs this binary); CI guard")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "See also: magus query/explain/path (read the graph).")
}

// graphBuild is the explicit "build the graph now" subcommand: it reindexes code symbols
// (runs each symbol-capable project's scip op to refresh its cached SCIP index) and
// then forces a full knowledge-graph rebuild, re-ingesting those indexes. It exists
// because building is otherwise implicit (cache-first, on read), which leaves no obvious
// way to say "refresh everything now" - especially the symbol indexes, which the daemon
// otherwise keeps fresh in the background. A missing indexer is reported with an install
// hint but does not fail the build; the domain graph rebuilds regardless.
func graphBuild(ctx context.Context, root string, args []string) error {
	var skipSymbols bool
	_, err := cmdParse("graph build", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&skipSymbols, "no-symbols", false, "rebuild the domain graph only; do not reindex code symbols")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus graph build [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Rebuild the knowledge graph now. By default it first reindexes code symbols")
			fmt.Fprintln(os.Stderr, "by running each symbol-capable project's `scip` op, then rebuilds and")
			fmt.Fprintln(os.Stderr, "re-ingests. The daemon does this automatically in the background; this is the")
			fmt.Fprintln(os.Stderr, "manual trigger (after a branch switch, or when the daemon is not running).")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}

	if !skipSymbols {
		m, err := loadMagus(ctx, root)
		if err != nil {
			return err
		}
		defer func() { _ = m.Close() }()
		n, rerr := m.ReindexSymbols(ctx)
		fmt.Fprintf(os.Stderr, "reindexed %d project(s)\n", n)
		if rerr != nil {
			// Non-fatal: a missing/failing indexer must not block the domain-graph
			// rebuild. Surface the actionable hints and carry on.
			interactive.Emit(os.Stderr, "some projects were not reindexed:")
			fmt.Fprintf(os.Stderr, "  %s\n", rerr.Error())
		}
	}

	g, err := loadKnowledgeGraph(ctx, root, true /* refresh */, false, false)
	if err != nil {
		return err
	}
	out := g.Output()
	fmt.Fprintf(os.Stderr, "knowledge graph rebuilt: %d nodes, %d edges\n", out.NodeCount, out.EdgeCount)
	return nil
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
// makes building implicit - there is no separate build subcommand.
func graphExport(ctx context.Context, root string, args []string) error {
	var (
		refresh      bool
		globalScope  bool
		reproducible bool
		staticAlias  bool
		sel          string
		budget       int
		open         bool
		exploreBase  string
		printOnly    bool
		serve        bool
		useTargets   bool
		follow       bool
	)
	pos, err := cmdParse("graph export", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&refresh, "refresh", false, "force a full graph rebuild before exporting")
		fs.BoolVar(&globalScope, "global", false, "union the workspaces registered in config (knowledge.workspaces) into one graph, IDs namespaced by workspace")
		fs.BoolVar(&reproducible, "reproducible", false, "omit everything that is not a function of the source tree (locally observed runtime attrs, git history) so the export regenerates byte-identically")
		// compat(until: no released magusfile or script passes --static): renamed to
		// --reproducible, which names the guarantee rather than gesturing at the site build.
		// Drop it once `git grep -- --static` finds no caller outside this repo's history.
		fs.BoolVar(&staticAlias, "static", false, "deprecated alias for --reproducible")
		fs.StringVar(&sel, "select", "", "export only the neighborhood of a query (same grammar as `magus query`) instead of the whole graph")
		fs.IntVar(&budget, "budget", knowledge.DefaultBudget, "node budget for --select (how many nodes the neighborhood may collect)")
		// Opening a viewer is spelled --open everywhere in this CLI (see `magus query
		// output --open`). It lives on export because export is already the verb that
		// emits the graph: -o json hands it to another tool, --open hands it to the
		// Graph Explorer. The flags below only mean anything alongside it.
		fs.BoolVar(&open, "open", false, "deliver the graph to the interactive Graph Explorer instead of stdout (privately: it never leaves your machine)")
		fs.StringVar(&exploreBase, "url", defaultExploreURL, "with --open: base URL of the Graph Explorer page (override for a self-hosted mirror)")
		fs.BoolVar(&printOnly, "print", false, "with --open: print the explorer URL to stdout instead of launching a browser")
		fs.BoolVar(&serve, "serve", false, "with --open: hand the graph to the page from an ephemeral loopback server instead of a URL fragment (no size limit; serves once and stops)")
		fs.BoolVar(&useTargets, "targets", false, "with --open: open the target dependency graph instead of the knowledge graph; pass a project path to scope it")
		fs.BoolVar(&follow, "follow", false, "with --open: keep the explorer updating from the running daemon instead of showing a snapshot (needs `magus server start`)")
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
			fmt.Fprintln(os.Stderr, "--open sends the same graph to the interactive Graph Explorer instead of")
			fmt.Fprintln(os.Stderr, "stdout. It never leaves your machine: by default it rides in the link's URL")
			fmt.Fprintln(os.Stderr, "fragment (#data=...), which browsers never transmit; --serve hands it over an")
			fmt.Fprintln(os.Stderr, "ephemeral 127.0.0.1 loopback server instead (no size limit). --targets opens")
			fmt.Fprintln(os.Stderr, "the target dependency graph, and takes an optional project path to scope it.")
			fmt.Fprintf(os.Stderr, "--follow keeps the view updating from the running daemon (%s);\n", clihint.ServerStart)
			fmt.Fprintln(os.Stderr, "with no mode flag and a reachable daemon it is chosen automatically.")
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

	// --open is a different DESTINATION, not a different format, so it short-circuits
	// before any -o resolution: the explorer takes the graph in the shape the explorer
	// wants, and -o json/graphml stay the paths that hand it to another tool.
	if open {
		return openExplorer(ctx, root, explorerOptions{
			refresh:     refresh,
			globalScope: globalScope,
			base:        exploreBase,
			printOnly:   printOnly,
			serve:       serve,
			useTargets:  useTargets,
			follow:      follow,
		}, pos)
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
	if staticAlias {
		reproducible = true
	}
	if reproducible {
		stripUnreproducible(&out)
	}
	// Omitted under --reproducible for the same reason as everything else it drops: the
	// fingerprint identifies the BINARY, not the graph, so two builds of one source rewrote
	// this field and failed the drift gate with the fingerprint as the entire diff.
	//
	// Every other export still carries it, which is where it answers its question:
	// when two graphs disagree, which build produced each.
	if !reproducible {
		out.CatalogFingerprint = magus.CatalogFingerprint()
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

// stripUnreproducible removes everything from an exported graph that is not a function of
// the source tree, leaving an artifact that regenerates byte-identically anywhere.
//
// Two kinds qualify, for the same reason and not the obvious one. Locally OBSERVED data
// (run timings, output refs, coverage) varies by machine. Git HISTORY - author nodes,
// per-file churn, the dir_commits roll-up, prose staleness - varies by COMMIT, and that is
// the sharper problem for a checked-in export: committing anything changes the churn, so
// the artifact invalidates itself and the drift gate fires on the commit that just fixed
// it. The @dirs shard calls its inputs deterministic, which is true per HEAD and is not the
// property a committed file needs.
//
// Graph.Output shares node attribute maps with the live graph, so this copies rather than
// deleting in place. Interactive queries and the live graph keep all of it.
func stripUnreproducible(g *types.KnowledgeGraphOutput) {
	drop := func(key string) bool { return knowledge.IsRuntimeAttr(key) || knowledge.IsHistoryAttr(key) }

	nodes := g.Nodes[:0]
	for _, n := range g.Nodes {
		// An author node exists only because git history does, so it goes whole rather than
		// surviving as a bare id with every attr stripped off it.
		if n.Kind == types.KindAuthor {
			continue
		}
		kept := make(map[string]string, len(n.Attrs))
		for key, value := range n.Attrs {
			if !drop(key) {
				kept[key] = value
			}
		}
		if len(kept) == 0 {
			kept = nil
		}
		n.Attrs = kept
		nodes = append(nodes, n)
	}
	g.Nodes = nodes
	g.NodeCount = len(nodes)

	links := g.Links[:0]
	for _, link := range g.Links {
		if link.Provenance == knowledge.ProvenanceRuntime || link.Relation == types.RelationAuthored {
			continue
		}
		links = append(links, link)
	}
	g.Links = links
	g.EdgeCount = len(links)
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
	fmt.Printf("graph: %d nodes, %d edges\n", out.NodeCount, out.EdgeCount)
	// The connectivity lens: fragmentation is a data-quality signal. One component with few isolated nodes
	// is a well-linked graph; many components / a high isolated count means the builder left relationships
	// undefined (the discoverability gap to chase).
	fmt.Printf("connectivity: %d component(s), largest holds %d, %d isolated node(s)\n\n",
		out.ComponentCount, out.LargestComponentSize, out.IsolatedCount)
	fmt.Println("god nodes (most connected):")
	fmt.Printf("  %6s  %4s  %4s  %-11s  %s\n", "DEGREE", "IN", "OUT", "KIND", "LABEL")
	for _, g := range out.Gods {
		fmt.Printf("  %6d  %4d  %4d  %-11s  %s\n", g.Degree, g.In, g.Out, g.Kind, g.Label)
	}
	if len(out.Orphans) > 0 {
		// Orphans is a capped sample; name the true isolated total so a large graph does not read as if
		// only len(Orphans) nodes are unlinked.
		if out.IsolatedCount > len(out.Orphans) {
			fmt.Printf("\norphans (showing %d of %d isolated):\n", len(out.Orphans), out.IsolatedCount)
		} else {
			fmt.Printf("\norphans (%d):\n", len(out.Orphans))
		}
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
		// Warn rather than silently drop a symbol-seeded selection, so an empty result
		// is not mistaken for "no such symbol".
		if includeSymbols {
			interactive.Emit(os.Stderr, "note: symbol queries are domain-only under --global (cross-workspace symbols are a later phase)")
		}
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

// loadKnowledgeGraphForRefs builds the domain graph and merges the symbol shards a
// `magus refs` lookup needs - targeted to ref's shards via the xref routing index when
// ref is an exact symbol ID, else all symbol shards. Symbols are per-workspace, so
// refs does not take --global.
func loadKnowledgeGraphForRefs(ctx context.Context, root string, refresh bool, ref string) (*knowledge.Graph, error) {
	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return nil, err
	}
	g, err := magus.BuildKnowledgeGraph(ctx, ws, ws.Root(), globalCfg, refresh, slog.Default())
	if err != nil {
		return nil, err
	}
	if err := magus.MergeWorkspaceSymbolsForRef(ctx, ws, ws.Root(), globalCfg, g, ref, slog.Default()); err != nil {
		return nil, err
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

// ---- graph export --open: delivering the graph to the Graph Explorer ----

// defaultExploreURL is the hosted, data-agnostic Graph Explorer. `open` points a
// browser at this page with the workspace's graph delivered PRIVATELY: either in a
// URL fragment (default) or fetched from an ephemeral loopback server (--serve).
// Either way the graph stays on the machine - the site only serves static assets.
const defaultExploreURL = "https://eli.gladman.cc/magus/console/graph/"

// fragmentWarnBytes is a conservative ceiling on the encoded fragment. The whole
// URL rides on the command line to the browser and into the address bar; Chrome
// handles multi-megabyte URLs, but Safari (~80 KB) and older Firefox (~64 KB)
// cap shorter. Past this we point at --serve, which has no size limit.
const fragmentWarnBytes = 48 * 1024

// Two privacy-first delivery modes:
//   - default: gzip+base64url the graph into a `#data=` URL fragment. A fragment
//     is never sent in an HTTP request, so the graph never leaves the machine.
//     Simple and serverless, but bounded by browser URL limits.
//   - --serve: run an ephemeral loopback HTTP server (127.0.0.1) that serves the
//     graph to the page via `#src=`. No size limit; the data stays on the local
//     network (loopback), never reaching the hosted site. CORS is locked to the
//     site origin so no other page can read it.
//
// explorerOptions is what `magus graph export --open` passes down: the delivery mode
// and scope already parsed, so this file no longer owns a flag set of its own.
type explorerOptions struct {
	refresh     bool
	globalScope bool
	base        string
	printOnly   bool
	serve       bool
	useTargets  bool
	follow      bool
}

// openExplorer delivers the graph to the Graph Explorer.
//
// It used to be `magus graph export --open`, its own subcommand with its own flags. Opening a
// viewer was spelled two ways across the CLI - a subcommand here, a --open FLAG on
// `magus query output` - for one act. It is a flag everywhere now, and export is its
// host because export is already the verb that emits the graph: -o json hands it to
// another tool, --open hands it to the viewer.
func openExplorer(ctx context.Context, root string, o explorerOptions, pos []string) error {
	refresh, globalScope, base := o.refresh, o.globalScope, o.base
	printOnly, serve, useTargets, follow := o.printOnly, o.serve, o.useTargets, o.follow

	if useTargets {
		if serve {
			fmt.Fprintln(os.Stderr, "magus graph export --open: --targets and --serve cannot be used together.")
			fmt.Fprintln(os.Stderr, "Target graphs are small; they always use the URL fragment.")
			return errSilent{exitCode: 2}
		}
		if globalScope {
			fmt.Fprintln(os.Stderr, "magus graph export --open: --targets and --global cannot be used together.")
			fmt.Fprintln(os.Stderr, "--targets scopes to this workspace's target graph; use a positional argument to scope to one project.")
			return errSilent{exitCode: 2}
		}
		if refresh {
			fmt.Fprintln(os.Stderr, "magus graph export --open: --targets and --refresh cannot be used together.")
			fmt.Fprintln(os.Stderr, "--targets reads the target graph directly from the magusfile; there is no knowledge store to refresh.")
			return errSilent{exitCode: 2}
		}
		return graphOpenTargets(ctx, root, base, printOnly, pos)
	}

	// Zero-arg default for the interactive open: when no explicit delivery mode is
	// chosen and no --targets, probe the ACTUAL console first (not just the proc
	// socket - a proc daemon can be up with no bridge running). If it is reachable,
	// use --follow for an always-fresh view; otherwise fall through to fragment mode.
	// Skip the auto-probe under --print: that flag exists for scriptable, copyable
	// output, so its URL must be deterministic (the static data fragment) rather than
	// flipping to a live+token URL whenever a daemon happens to be listening. Explicit
	// --follow --print still prints the follow URL.
	if !follow && !serve && !printOnly {
		if liveBridgeReachable(ctx) {
			follow = true
		}
	}
	if follow {
		return graphOpenFollow(ctx, printOnly, useTargets)
	}

	// The explorer shows the domain graph; symbol shards would bloat it, so exclude them.
	g, err := loadKnowledgeGraph(ctx, root, refresh, globalScope, false)
	if err != nil {
		return err
	}
	out := g.Output()
	if !globalScope {
		out.SourceBaseURL = deriveSourceBase(ctx, root) // link node sources to the right repo
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return fmt.Errorf("encode graph: %w", err)
	}

	if serve {
		return graphOpenServe(ctx, base, raw, out.NodeCount, out.EdgeCount)
	}

	encoded, err := render.EncodeFragmentRaw(raw)
	if err != nil {
		return err
	}
	openURL := strings.TrimRight(base, "/") + "/#data=" + encoded

	if len(encoded) > fragmentWarnBytes {
		fmt.Fprintf(os.Stderr, "magus graph export --open: this graph encodes to %d KB, near or past what Safari and older\n", len(encoded)/1024)
		fmt.Fprintln(os.Stderr, "Firefox accept in a URL (Chrome is fine). If the page does not load, re-run with")
		fmt.Fprintln(os.Stderr, "--serve to deliver it over a loopback server instead (no size limit). Continuing.")
	}

	if printOnly {
		fmt.Println(openURL)
		return nil
	}

	fmt.Fprintf(os.Stderr, "opening the graph explorer for this workspace (%d nodes, %d edges).\n", out.NodeCount, out.EdgeCount)
	fmt.Fprintln(os.Stderr, "your graph rides in the link fragment and is never uploaded - it does not leave your machine.")
	if err := openBrowser(openURL); err != nil {
		fmt.Fprintf(os.Stderr, "magus graph export --open: could not open a browser (%v).\n", err)
		fmt.Fprintln(os.Stderr, "Re-run with --print to get the URL, or open it yourself.")
		return errSilent{exitCode: 1}
	}
	return nil
}

// graphOpenTargets opens the workspace's target dependency graph in the hosted
// Graph Explorer using the #data= fragment path. Target graphs are always
// delivered via the fragment (they are small, so --serve is never needed).
// If args contains a project path, only that project's targets are included.
func graphOpenTargets(ctx context.Context, root, base string, printOnly bool, args []string) error {
	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return err
	}
	out, err := ws.TargetGraph(ctx)
	if err != nil {
		return err
	}

	if len(args) > 0 {
		scope := args[0]
		var filtered []types.TargetGraphProject
		for _, p := range out.Projects {
			if p.Path == scope {
				filtered = append(filtered, p)
				break
			}
		}
		if len(filtered) == 0 {
			paths := make([]string, 0, len(out.Projects))
			for _, p := range out.Projects {
				paths = append(paths, p.Path)
			}
			slices.Sort(paths)
			fmt.Fprintf(os.Stderr, "magus graph export --open --targets: unknown project %q\n", scope)
			fmt.Fprintln(os.Stderr, "valid projects:")
			for _, p := range paths {
				fmt.Fprintf(os.Stderr, "  %s\n", p)
			}
			return errSilent{exitCode: 2}
		}
		out.Projects = filtered
	}

	raw, err := json.Marshal(out)
	if err != nil {
		return fmt.Errorf("encode target graph: %w", err)
	}
	encoded, err := render.EncodeFragmentRaw(raw)
	if err != nil {
		return err
	}
	openURL := strings.TrimRight(base, "/") + "/#data=" + encoded

	if printOnly {
		fmt.Println(openURL)
		return nil
	}

	fmt.Fprintln(os.Stderr, "opening the graph explorer for this workspace's target graph.")
	fmt.Fprintln(os.Stderr, "your graph rides in the link fragment and is never uploaded - it does not leave your machine.")
	if err := openBrowser(openURL); err != nil {
		fmt.Fprintf(os.Stderr, "magus graph export --open: could not open a browser (%v).\n", err)
		fmt.Fprintln(os.Stderr, "Re-run with --print to get the URL, or open it yourself.")
		return errSilent{exitCode: 1}
	}
	return nil
}

// graphOpenServe hands the graph to the hosted page over an ephemeral 127.0.0.1 server,
// then STOPS - a one-shot handoff, not a standing service. The loopback bind, CORS lock,
// serve-once, and grace-then-shutdown all live in internal/httpx (shared with the live log
// stream); this wraps them with the graph-specific URL (#src=) and the user-facing
// messages. The graph is delivered browser <-> loopback and never leaves the machine.
func graphOpenServe(ctx context.Context, base string, raw []byte, nodes, edges int) error {
	origin, err := httpx.ParseOrigin(base)
	if err != nil {
		return err
	}
	bs, err := httpx.StartBlob(origin, "/graph.json", "application/json", raw)
	if err != nil {
		return err
	}
	// SourceURL carries the per-run bearer token in a `?token=` query param; tucking the
	// whole URL into the `#src=` fragment keeps the token out of any HTTP request the browser
	// makes to the hosted page, and the explorer replays it when it fetches the blob.
	openURL := strings.TrimRight(base, "/") + "/#src=" + url.QueryEscape(bs.SourceURL())

	fmt.Fprintf(os.Stderr, "handing this workspace's graph (%d nodes, %d edges) to your browser over loopback (%s).\n", nodes, edges, bs.Addr())
	fmt.Fprintf(os.Stderr, "it is served once, CORS-locked to %s, and never leaves your machine; the server stops as soon as the page has it.\n", origin)
	if err := openBrowser(openURL); err != nil {
		fmt.Fprintf(os.Stderr, "magus graph export --open: could not open a browser (%v). Open this yourself (the server is waiting):\n  %s\n", err, openURL)
	}

	switch outcome := bs.WaitServed(ctx); outcome {
	case httpx.ServeCompleted:
		fmt.Fprintln(os.Stderr, "graph loaded; loopback server stopped.")
	case httpx.ServeTimedOut:
		fmt.Fprintln(os.Stderr, "the page never requested the graph; loopback server stopped. Re-run if your browser did not open.")
	case httpx.ServeCanceled:
		fmt.Fprintln(os.Stderr, "\ncanceled; loopback server stopped.")
	default:
		// A new ServeOutcome added upstream must not be swallowed as success: name it.
		return fmt.Errorf("graph export --open --serve: unexpected serve outcome %v", outcome)
	}
	return nil
}

// openBrowser launches a browser for a URL and does not wait - the browser owns the
// tab from there. It honors the freedesktop/de-facto BROWSER convention first, so a
// user can force a specific browser on any platform (e.g.
// `BROWSER=firefox magus query out1a2b3c --open`); only when BROWSER is unset or every
// entry fails does it fall back to the OS default handler (macOS `open`, Windows
// FileProtocolHandler, else `xdg-open`, which itself already respects BROWSER and the
// desktop's default-web-browser setting on Linux).
func openBrowser(raw string) error {
	target, err := safeBrowserURL(raw)
	if err != nil {
		return err
	}
	if err := openViaBrowserEnv(target); err == nil {
		return nil
	}
	var cmd *exec.Cmd
	// The opener binary is fixed; the only variable is the URL, and safeBrowserURL
	// above has already required an http(s) scheme and a host and re-serialized it.
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target) //nolint:gosec // G702: fixed opener, URL validated by safeBrowserURL
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target) //nolint:gosec // G702: fixed opener, URL validated by safeBrowserURL
	default:
		cmd = exec.Command("xdg-open", target) //nolint:gosec // G702: fixed opener, URL validated by safeBrowserURL
	}
	return cmd.Start()
}

// safeBrowserURL parses raw and returns it only if it is an http(s) URL, re-serialized
// from the parsed form.
//
// The base of every URL magus opens is overridable - `--url` for the explorer,
// MAGUS_LOG_VIEWER_URL for the log viewer - so what reaches the opener is not a
// constant, and it is handed to a program as an argument. Two things follow. A string
// beginning with "-" would be read by the opener as a FLAG rather than a URL, which the
// scheme check rules out. And a scheme like file: or a shell-ish string is not something
// magus should hand a launcher on the strength of an environment variable.
//
// Re-serializing rather than returning raw is the part that makes this a sanitizer and
// not just a check: what gets executed is what was parsed and validated, so no unparsed
// tail can ride along.
func safeBrowserURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("not a URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("refusing to open %q: only http and https URLs are opened", raw)
	}
	if u.Host == "" {
		return "", fmt.Errorf("refusing to open %q: no host", raw)
	}
	return u.String(), nil
}

// openViaBrowserEnv tries the $BROWSER convention: a colon-separated list of commands,
// each either containing "%s" (replaced by the URL) or taking the URL as a trailing
// argument. The first entry that launches wins. Returns an error if BROWSER is unset
// or no entry starts, so the caller falls back to the platform opener.
func openViaBrowserEnv(url string) error {
	env := strings.TrimSpace(os.Getenv("BROWSER"))
	if env == "" {
		return errors.New("BROWSER not set")
	}
	for _, entry := range strings.Split(env, ":") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		var fields []string
		if strings.Contains(entry, "%s") {
			fields = strings.Fields(strings.ReplaceAll(entry, "%s", url))
		} else {
			fields = append(strings.Fields(entry), url)
		}
		if len(fields) == 0 {
			continue
		}
		cmd := exec.Command(fields[0], fields[1:]...) //nolint:gosec // G702: user's own configured browser-open command, not remote input
		if err := cmd.Start(); err == nil {
			return nil
		}
	}
	return errors.New("no BROWSER entry launched")
}

// probeLiveBridgeTimeout bounds the real HTTP probe of the console below.
const probeLiveBridgeTimeout = 2 * time.Second

// probeLiveBridge issues a real HTTP GET to the console's guarded
// /api/v1/graph route to confirm it is actually up, mirroring the doctor
// bridge check (internal/doctor/checks_mcp.go probeBridgeReachability). A
// daemon-status probe alone is not enough: daemonStatus("") accepts ANY
// reachable proc socket (Mode=="proc"), which is a different transport than
// the console this URL targets - a proc-mode daemon with no bridge running
// would otherwise let a token be printed for an address nothing is listening
// on. A 401/403 response proves the guarded route exists (auth runs before
// the handler); connection refused/timeout means the bridge is down.
func probeLiveBridge(ctx context.Context, addr string) error {
	target := "http://" + addr + "/api/v1/graph"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("bridge not reachable at %s: %w", target, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil
	default:
		return fmt.Errorf("bridge at %s responded with unexpected status %d", target, resp.StatusCode)
	}
}

// liveBridgeReachable reports whether the console is actually up, for the
// zero-arg auto-switch in graphOpen. It never emits a token; it only decides
// whether to attempt live mode at all.
func liveBridgeReachable(ctx context.Context) bool {
	pctx, cancel := context.WithTimeout(ctx, probeLiveBridgeTimeout)
	defer cancel()
	return probeLiveBridge(pctx, mcpAddrString()) == nil
}

// graphOpenLive opens the Graph Explorer served BY the running daemon from its own
// loopback origin (http://<host>/console/graph/). Under the daemon-origin grammar the origin
// names which daemon; the page loads both itself and its graph data from that one loopback
// origin, so the graph never leaves the machine. The clean /console/graph/ path is canonical -
// the daemon serves the shell for it and the console's boot router opens the graph surface.
// There is no #live= host directive and no hosted explorer base - the --url flag governs only
// the static (--data/--targets/--serve) modes, not --follow.
//
// The token is loaded from the on-disk token file written by auth.Save/SaveNew.
// It is embedded in the URL fragment (which browsers do not transmit in HTTP
// requests) and is stripped from the fragment by the page on first load.
func graphOpenFollow(ctx context.Context, printOnly, useTargets bool) error {
	hostPort := mcpAddrString()

	// Probe the ACTUAL console (not just the proc socket) so we never emit a
	// URL and token for a transport nothing is listening on. Explicit --follow
	// with no reachable bridge is an error; magus never auto-starts a daemon.
	pctx, cancel := context.WithTimeout(ctx, probeLiveBridgeTimeout)
	defer cancel()
	if err := probeLiveBridge(pctx, hostPort); err != nil {
		fmt.Fprintln(os.Stderr, "magus graph export --open --follow: the console is not reachable.")
		fmt.Fprintf(os.Stderr, "start it: %s\n", clihint.ServerStart)
		return errSilent{exitCode: 1}
	}

	token, err := auth.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "magus graph export --open --follow: could not load the MCP token: %v\n", err)
		fmt.Fprintf(os.Stderr, "If no token exists yet, run: %s\n", clihint.MCPTokenGenerate)
		return errSilent{exitCode: 1}
	}

	linkOpts := console.LinkOpts{Host: hostPort, Surface: "graph", Token: token}
	if useTargets {
		linkOpts.Fragment = append(linkOpts.Fragment, console.FragmentParam{Key: "flavor", Value: "targets"})
	}
	openURL := console.Link(linkOpts)

	if printOnly {
		fmt.Println(openURL)
		return nil
	}

	fmt.Fprintf(os.Stderr, "opening the graph explorer in live mode (daemon at %s).\n", hostPort)
	fmt.Fprintln(os.Stderr, "the explorer connects directly to your local daemon; your graph never leaves your machine.")
	if err := openBrowser(openURL); err != nil {
		fmt.Fprintf(os.Stderr, "magus graph export --open: could not open a browser (%v).\n", err)
		fmt.Fprintln(os.Stderr, "Re-run with --print to get the URL, or open it yourself.")
		return errSilent{exitCode: 1}
	}
	return nil
}
