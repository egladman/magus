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

	"github.com/egladman/magus"
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

var graphSubs = []string{"deps", "export", "stats"}

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
	fmt.Fprintln(os.Stderr, "  export   merged knowledge graph (-o json|graphml for external graph tools)")
	fmt.Fprintln(os.Stderr, "  stats    knowledge-graph shape: god nodes, orphans, doc coverage (--kind to scope)")
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
	var refresh bool
	_, err := cmdParse("graph export", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&refresh, "refresh", false, "force a full graph rebuild before exporting")
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
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}

	opts, err := ResolveOutput(global.output, outputGraphML)
	if err != nil {
		return err
	}

	g, err := loadKnowledgeGraph(ctx, root, refresh)
	if err != nil {
		return err
	}
	out := g.Output()

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	case outputGraphML:
		return render.WriteKnowledgeGraphML(os.Stdout, out)
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
		kind    string
		refresh bool
	)
	_, err := cmdParse("graph stats", args, func(fs *flag.FlagSet) {
		fs.StringVar(&kind, "kind", "", "scope every section to one node kind (e.g. spell, target, doc, diagnostic)")
		fs.BoolVar(&refresh, "refresh", false, "force a full graph rebuild first")
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
	g, err := loadKnowledgeGraph(ctx, root, refresh)
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
// query/explain/path verbs so they all sit on one substrate.
func loadKnowledgeGraph(ctx context.Context, root string, refresh bool) (*knowledge.Graph, error) {
	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return nil, err
	}
	return magus.BuildKnowledgeGraph(ctx, ws, ws.Root(), globalCfg, refresh, slog.Default())
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
