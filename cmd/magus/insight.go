package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/render"
	"github.com/egladman/magus/types"
)

// insightAnalyzer is the workspace capability the insight lenses need; inspectWorkspace
// returns the concrete *magus.Magus that satisfies it.
type insightAnalyzer interface {
	Hotspots(ctx context.Context, opts types.InsightOptions) (types.HotspotOutput, error)
	Affinity(ctx context.Context, opts types.InsightOptions) (types.AffinityOutput, error)
	Ownership(ctx context.Context, opts types.InsightOptions) (types.OwnershipOutput, error)
	Trend(ctx context.Context, opts types.InsightOptions) (types.TrendOutput, error)
}

var insightLenses = []string{"hotspots", "affinity", "ownership", "trend", "structure", "report"}

func insightCmd(ctx context.Context, root string, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		insightUsage()
		return flag.ErrHelp
	}
	lens, rest := args[0], args[1:]
	switch lens {
	case "hotspots":
		return insightHotspots(ctx, root, rest)
	case "affinity":
		return insightAffinity(ctx, root, rest)
	case "ownership":
		return insightOwnership(ctx, root, rest)
	case "trend":
		return insightTrend(ctx, root, rest)
	case "structure":
		return insightStructure(ctx, root, rest)
	case "report":
		return insightReport(ctx, root, rest)
	default:
		fmt.Fprintf(os.Stderr, "magus insight: unknown lens %q\n", lens)
		if sug := interactive.SuggestNearest(lens, insightLenses); sug != "" {
			interactive.Emit(os.Stderr, fmt.Sprintf("did you mean %q?", sug))
		}
		fmt.Fprintln(os.Stderr, "")
		insightUsage()
		return errSilent{exitCode: 2}
	}
}

func insightUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus insight <lens> [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, types.InsightDefinition)
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "History lenses (git):")
	fmt.Fprintln(os.Stderr, "  hotspots   churn × complexity — the prime refactoring targets (--files for per-file)")
	fmt.Fprintln(os.Stderr, "  affinity   projects that change together, flagging hidden (undeclared) coupling")
	fmt.Fprintln(os.Stderr, "  ownership  author concentration, bus factor, and abandonment risk")
	fmt.Fprintln(os.Stderr, "  trend      rising vs cooling activity across the window")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Structure lens (knowledge graph):")
	fmt.Fprintln(os.Stderr, "  structure  god nodes, orphans, and doc coverage (--kind to scope)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  report     every lens as one Markdown doc (commit it as INSIGHT.md)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "History flags: --commits N, --since 90d|12w|6mo|1y. Each lens accepts -o text|json|yaml|name.")
}

// insightSetup parses the common flags, resolves the working-directory scope (like
// run/affected), and hands back the analyzer + options. extra registers lens-specific
// flags; formats lists any output formats beyond the common set the lens accepts.
func insightSetup(ctx context.Context, root, lens, def string, args []string,
	extra func(*flag.FlagSet, *types.InsightOptions), formats ...Format,
) (insightAnalyzer, types.InsightOptions, OutputOptions, error) {
	opts := types.InsightOptions{Commits: 500}
	var wholeWorkspace bool
	_, err := cmdParse("insight "+lens, args, func(fs *flag.FlagSet) {
		fs.IntVar(&opts.Commits, "commits", opts.Commits, "cap on how many recent commits to scan")
		fs.StringVar(&opts.Since, "since", "", "only commits within this window (e.g. 90d, 12w, 6mo, 1y)")
		fs.BoolVar(&wholeWorkspace, "workspace", false, "analyze the whole workspace instead of the current project/subtree")
		if extra != nil {
			extra(fs, &opts)
		}
		fs.Usage = func() {
			fmt.Fprintf(os.Stderr, "Usage: magus insight %s [flags]\n\n%s\n\nFlags (global flags also accepted, see `magus -h`):\n", lens, def)
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return nil, opts, OutputOptions{}, err
	}
	outOpts, err := ResolveOutput(global.output, formats...)
	if err != nil {
		return nil, opts, OutputOptions{}, err
	}
	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return nil, opts, OutputOptions{}, err
	}
	a, ok := ws.(insightAnalyzer)
	if !ok {
		return nil, opts, OutputOptions{}, errors.New("insight: workspace does not support insight analysis")
	}
	// Default is per-project: scope to the working directory (like run/affected;
	// cwdAnchor clamps to root). --workspace opts into the whole-workspace aggregate
	// (empty Dir makes the scan default to the workspace root).
	if wholeWorkspace {
		opts.Dir = ""
	} else {
		opts.Dir = filepath.Join(root, cwdAnchor(root))
	}
	return a, opts, outOpts, nil
}

func insightHotspots(ctx context.Context, root string, args []string) error {
	a, opts, outOpts, err := insightSetup(ctx, root, "hotspots", types.HotspotDefinition, args,
		func(fs *flag.FlagSet, o *types.InsightOptions) {
			fs.BoolVar(&o.Files, "files", false, "rank individual files by churn × complexity instead of projects")
		}, outputMermaid)
	if err != nil {
		return err
	}
	out, err := a.Hotspots(ctx, opts)
	if err != nil {
		return err
	}
	switch outOpts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(outOpts, out)
	case outputName:
		if out.Files != nil {
			for _, f := range out.Files {
				fmt.Println(f.Path)
			}
			return nil
		}
		for _, n := range hotspotRanked(out.Nodes) {
			fmt.Println(n.Path)
		}
		return nil
	case outputMermaid:
		// The file view becomes a churn-vs-complexity quadrant scatter; the project
		// view stays the heat-coloured dependency graph.
		if out.Files != nil {
			return render.WriteHotspotQuadrant(os.Stdout, out)
		}
		return render.WriteHotspotMermaid(os.Stdout, out)
	}
	return hotspotText(out)
}

func insightAffinity(ctx context.Context, root string, args []string) error {
	a, opts, outOpts, err := insightSetup(ctx, root, "affinity", types.AffinityDefinition, args, nil, outputMermaid)
	if err != nil {
		return err
	}
	out, err := a.Affinity(ctx, opts)
	if err != nil {
		return err
	}
	switch outOpts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(outOpts, out)
	case outputName:
		for _, c := range out.Pairs {
			fmt.Printf("%s %s\n", c.A, c.B)
		}
		return nil
	case outputMermaid:
		return render.WriteAffinityMermaid(os.Stdout, out)
	}
	fmt.Printf("definition: %s\n\n", out.Definition)
	fmt.Printf("affinity pairs (%s):\n", windowText(out.Commits, out.Since))
	fmt.Printf("  %5s  %-6s  %s\n", "COUNT", "HIDDEN", "PROJECTS")
	for _, c := range out.Pairs {
		fmt.Printf("  %5d  %-6s  %s <-> %s\n", c.Count, flag6(c.Hidden), c.A, c.B)
	}
	return nil
}

func insightOwnership(ctx context.Context, root string, args []string) error {
	a, opts, outOpts, err := insightSetup(ctx, root, "ownership", types.OwnershipDefinition, args, nil)
	if err != nil {
		return err
	}
	out, err := a.Ownership(ctx, opts)
	if err != nil {
		return err
	}
	switch outOpts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(outOpts, out)
	case outputName:
		for _, o := range out.Projects {
			fmt.Println(o.Path)
		}
		return nil
	}
	fmt.Printf("definition: %s\n\n", out.Definition)
	fmt.Printf("ownership (%s):\n", windowText(out.Commits, out.Since))
	fmt.Printf("  %6s  %4s  %3s  %5s  %-16s  %s\n", "SHARE", "AUTH", "BF1", "STALE", "PRIMARY", "PROJECT")
	for _, o := range out.Projects {
		fmt.Printf("  %5d%%  %4d  %3s  %5s  %-16s  %s\n",
			o.PrimaryShare, o.Authors, flag3(o.BusFactor1), flag3(o.Stale), truncate(o.Primary, 16), o.Path)
	}
	return nil
}

func insightTrend(ctx context.Context, root string, args []string) error {
	a, opts, outOpts, err := insightSetup(ctx, root, "trend", types.TrendDefinition, args, nil)
	if err != nil {
		return err
	}
	out, err := a.Trend(ctx, opts)
	if err != nil {
		return err
	}
	switch outOpts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(outOpts, out)
	case outputName:
		for _, t := range out.Projects {
			fmt.Println(t.Path)
		}
		return nil
	}
	fmt.Printf("definition: %s\n\n", out.Definition)
	fmt.Printf("trend (%s):\n", windowText(out.Commits, out.Since))
	fmt.Printf("  %6s  %6s  %7s  %s\n", "DELTA", "RECENT", "EARLIER", "PROJECT")
	for _, t := range out.Projects {
		fmt.Printf("  %+6d  %6d  %7d  %s\n", t.Delta, t.Recent, t.Earlier, t.Path)
	}
	return nil
}

// insightReport gathers every lens and emits the combined Markdown doc (the default),
// or the bundled struct under -o json/yaml.
func insightReport(ctx context.Context, root string, args []string) error {
	// The report's hotspots always include the per-file ranking.
	a, opts, outOpts, err := insightSetup(ctx, root, "report", "Every lens as one Markdown document.", args,
		func(_ *flag.FlagSet, o *types.InsightOptions) { o.Files = true }, outputMarkdown)
	if err != nil {
		return err
	}
	hot, err := a.Hotspots(ctx, opts)
	if err != nil {
		return err
	}
	aff, err := a.Affinity(ctx, opts)
	if err != nil {
		return err
	}
	own, err := a.Ownership(ctx, opts)
	if err != nil {
		return err
	}
	tr, err := a.Trend(ctx, opts)
	if err != nil {
		return err
	}
	report := types.InsightReport{Hotspots: hot, Affinity: aff, Ownership: own, Trend: tr}
	// The report spans both axes: add the structural lens (best-effort - a graph
	// build failure just omits the section rather than failing the whole report).
	if g, gerr := loadKnowledgeGraph(ctx, root, false); gerr == nil {
		report.Structure = g.Structure("")
	}

	switch outOpts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(outOpts, report)
	}
	return render.WriteInsightMarkdown(os.Stdout, report)
}

// insightStructure is the structural lens: it reads the knowledge graph (cache-
// first) rather than git history, reporting god nodes, orphans, and doc coverage.
func insightStructure(ctx context.Context, root string, args []string) error {
	var kind string
	_, err := cmdParse("insight structure", args, func(fs *flag.FlagSet) {
		fs.StringVar(&kind, "kind", "", "scope every section to one node kind (e.g. spell, target, doc, diagnostic)")
		fs.Usage = func() {
			fmt.Fprintf(os.Stderr, "Usage: magus insight structure [flags]\n\n%s\n\nFlags (global flags also accepted, see `magus -h`):\n", types.KnowledgeStructureDefinition)
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
	g, err := loadKnowledgeGraph(ctx, root, false)
	if err != nil {
		return err
	}
	out := g.Structure(kind)

	switch outOpts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(outOpts, out)
	case outputName:
		for _, god := range out.Gods {
			fmt.Println(god.ID)
		}
		return nil
	}
	return structureText(out)
}

func structureText(out types.KnowledgeStructure) error {
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

func hotspotText(out types.HotspotOutput) error {
	fmt.Printf("definition: %s\n\n", out.Definition)
	if out.Files != nil {
		fmt.Printf("file hotspots (%s):\n", windowText(out.Commits, out.Since))
		fmt.Printf("  %6s  %7s  %5s  %4s  %6s  %s\n", "SCORE", "COMMITS", "CPLX", "AUTH", "LAST", "FILE")
		for _, f := range out.Files {
			fmt.Printf("  %6d  %7d  %5d  %4d  %6s  %s\n", f.Score, f.Commits, f.Complexity, f.Authors, ago(f.LastCommit), f.Path)
		}
		return nil
	}
	fmt.Printf("project hotspots (%s):\n", windowText(out.Commits, out.Since))
	fmt.Printf("  %7s  %4s  %6s  %4s  %s\n", "COMMITS", "AUTH", "LAST", "BR", "PROJECT")
	for _, n := range hotspotRanked(out.Nodes) {
		fmt.Printf("  %7d  %4d  %6s  %4d  %s\n", n.Churn, n.Authors, agoPtr(n.LastCommit), n.BlastRadius, n.Path)
	}
	return nil
}

// hotspotRanked returns the nodes sorted hottest-first, ties broken by path.
func hotspotRanked(nodes []types.Node) []types.Node {
	out := slices.Clone(nodes)
	slices.SortFunc(out, func(a, b types.Node) int {
		if c := cmp.Compare(b.Churn, a.Churn); c != 0 {
			return c
		}
		return cmp.Compare(a.Path, b.Path)
	})
	return out
}

func windowText(commits int, since string) string {
	s := fmt.Sprintf("last %d commits", commits)
	if since != "" {
		s += " within " + since
	}
	return s
}

// ago renders a timestamp as whole days elapsed (e.g. "3d"), or "-" when zero.
func ago(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return fmt.Sprintf("%dd", int(time.Since(t).Hours()/24))
}

func agoPtr(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return ago(*t)
}

func flag3(b bool) string {
	if b {
		return "yes"
	}
	return "-"
}

func flag6(b bool) string {
	if b {
		return "hidden"
	}
	return "-"
}
