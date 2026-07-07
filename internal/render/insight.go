package render

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/types"
)

// heatPalette is a 5-stop cold→hot ramp; index 0 is "no recent churn", index 4 the
// hottest. Text colors keep the labels legible against each fill.
var heatPalette = []struct{ fill, text string }{
	{"#eff3ff", "#000"},
	{"#bdd7e7", "#000"},
	{"#6baed6", "#000"},
	{"#3182bd", "#fff"},
	{"#08519c", "#fff"},
}

// heatBucket maps a churn count onto a palette index. 0 stays cold (0); any
// non-zero count lands in 1..4, scaled so the busiest project reaches the top of
// the ramp (ceil(count/max * 4)).
func heatBucket(count, max int) int {
	if count <= 0 || max <= 0 {
		return 0
	}
	b := (count*4 + max - 1) / max
	if b > 4 {
		b = 4
	}
	return b
}

// WriteHotspotMermaid emits the dependency graph as a Mermaid flowchart heat-coloured
// by edit frequency: each project node is filled by its churn bucket and labelled
// with its commit count, authors, and blast radius. Edges are the dependency edges,
// so the hot spots show up in the context of what depends on them.
func WriteHotspotMermaid(w io.Writer, out types.HotspotOutput) error {
	return writeMermaid(w, hotspotGraphIR(out))
}

// hotspotGraphIR maps a HotspotOutput onto the shared renderGraph. IDs are assigned
// in path order (collisions get a numeric suffix) so output is deterministic — it
// mirrors projectGraphIR but buckets nodes by churn instead of spell and stays flat
// (no subgraphs), which reads better as a heatmap.
func hotspotGraphIR(out types.HotspotOutput) renderGraph {
	paths := make([]string, len(out.Nodes))
	maxChurn := 0
	for i, n := range out.Nodes {
		paths[i] = n.Path
		if n.Churn > maxChurn {
			maxChurn = n.Churn
		}
	}
	ids := mermaidIDs(paths)

	g := renderGraph{Title: "magus hotspot heatmap", DOTName: "magus_hotspots"}

	nodes := slices.Clone(out.Nodes)
	slices.SortFunc(nodes, func(a, b types.Node) int { return strings.Compare(a.Path, b.Path) })
	for _, n := range nodes {
		label := n.Path + fmt.Sprintf("<br/>commits=%d", n.Churn)
		if n.Authors > 0 {
			label += fmt.Sprintf("<br/>authors=%d", n.Authors)
		}
		if n.BlastRadius > 0 {
			label += fmt.Sprintf("<br/>BR=%d", n.BlastRadius)
		}
		class := fmt.Sprintf("heat%d", heatBucket(n.Churn, maxChurn))
		rn := renderNode{ID: ids[n.Path], DOTID: n.Path, Label: label, Shape: shapeBox, Classes: []string{class}}
		if n.Dir != "" {
			rn.ClickURL = "file://" + n.Dir
			rn.ClickTip = n.Path
		}
		g.Nodes = append(g.Nodes, rn)
	}
	for _, n := range out.Nodes {
		for _, child := range n.Children {
			if cid, ok := ids[child]; ok {
				g.Edges = append(g.Edges, renderEdge{From: ids[n.Path], To: cid})
			}
		}
	}
	for i, c := range heatPalette {
		g.Classes = append(g.Classes, renderClass{
			Name:  fmt.Sprintf("heat%d", i),
			Style: fmt.Sprintf("fill:%s,color:%s", c.fill, c.text),
		})
	}
	return g
}

// WriteAffinityMermaid emits the affinity (co-change) graph: each project that shares
// a commit with another is a node, and every pair that changed together is an edge
// labelled with how often. Hidden pairs (affinity without a declared dependency) are
// drawn as dashed edges — the architectural smell the lens exists to surface.
func WriteAffinityMermaid(w io.Writer, out types.AffinityOutput) error {
	return writeMermaid(w, affinityGraphIR(out))
}

func affinityGraphIR(out types.AffinityOutput) renderGraph {
	pathSet := map[string]struct{}{}
	for _, c := range out.Pairs {
		pathSet[c.A] = struct{}{}
		pathSet[c.B] = struct{}{}
	}
	paths := make([]string, 0, len(pathSet))
	for p := range pathSet {
		paths = append(paths, p)
	}
	ids := mermaidIDs(paths)

	g := renderGraph{Title: "magus affinity graph", DOTName: "magus_affinity"}
	slices.Sort(paths)
	for _, p := range paths {
		g.Nodes = append(g.Nodes, renderNode{ID: ids[p], DOTID: p, Label: p, Shape: shapeBox})
	}
	// out.Pairs is already sorted; the edge is symmetric, so the arrow is only a
	// drawing artifact — the count label carries the meaning. A hidden pair is dashed.
	hidden := false
	for _, c := range out.Pairs {
		e := renderEdge{From: ids[c.A], To: ids[c.B], Label: strconv.Itoa(c.Count), Dashed: c.Hidden}
		if c.Hidden {
			hidden = true
		}
		g.Edges = append(g.Edges, e)
	}
	if hidden {
		g.Title += " (dashed = hidden affinity)"
	}
	return g
}

// WriteHotspotQuadrant emits a Mermaid quadrantChart of files on churn (y) vs
// complexity (x): the top-right quadrant — frequently changed and hard to understand
// — is the refactor-now list. Axes are normalised to the busiest/most-complex file in
// the set; only the highest-scoring files are plotted to keep the chart legible.
func WriteHotspotQuadrant(w io.Writer, out types.HotspotOutput) error {
	files := topFiles(out.Files, 20)
	maxCx, maxCommits := 1, 1
	for _, f := range files {
		if f.Complexity > maxCx {
			maxCx = f.Complexity
		}
		if f.Commits > maxCommits {
			maxCommits = f.Commits
		}
	}
	var b strings.Builder
	b.WriteString("quadrantChart\n")
	b.WriteString("  title Hotspots: churn vs complexity\n")
	b.WriteString("  x-axis Low Complexity --> High Complexity\n")
	b.WriteString("  y-axis Low Churn --> High Churn\n")
	b.WriteString("  quadrant-1 Refactor now\n")
	b.WriteString("  quadrant-2 Churny but simple\n")
	b.WriteString("  quadrant-3 Healthy\n")
	b.WriteString("  quadrant-4 Stable but complex\n")
	for _, f := range files {
		x := float64(f.Complexity) / float64(maxCx)
		y := float64(f.Commits) / float64(maxCommits)
		fmt.Fprintf(&b, "  %q: [%.3f, %.3f]\n", f.Path, x, y)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// WriteInsightMarkdown writes the combined `magus insight report`: a browsable
// Markdown doc with each lens's table and the two graphs as embedded Mermaid, suitable
// for committing (e.g. INSIGHT.md) and rendering on a Git host.
func WriteInsightMarkdown(w io.Writer, r types.InsightReport) error {
	var b strings.Builder

	b.WriteString("# Insight\n\n")
	b.WriteString("<!-- Generated by `magus insight report`. A point-in-time snapshot of VCS history; regenerate to refresh. -->\n\n")
	fmt.Fprintf(&b, "%s\n\n", types.InsightDefinition)
	fmt.Fprintf(&b, "_Window: %s._\n\n", windowText(r.Hotspots.Commits, r.Hotspots.Since))

	// Hotspots.
	b.WriteString("## Hotspots\n\n")
	fmt.Fprintf(&b, "%s\n\n", types.HotspotDefinition)
	b.WriteString("```mermaid\n")
	if err := writeMermaid(&b, hotspotGraphIR(r.Hotspots)); err != nil {
		return err
	}
	b.WriteString("```\n\n")
	if len(r.Hotspots.Files) > 0 {
		b.WriteString("```mermaid\n")
		if err := WriteHotspotQuadrant(&b, r.Hotspots); err != nil {
			return err
		}
		b.WriteString("```\n\n")
		b.WriteString("| Score | Commits | Complexity | Authors | File |\n|--:|--:|--:|--:|---|\n")
		for _, f := range topFiles(r.Hotspots.Files, 20) {
			fmt.Fprintf(&b, "| %d | %d | %d | %d | `%s` |\n", f.Score, f.Commits, f.Complexity, f.Authors, f.Path)
		}
		b.WriteString("\n")
	}

	// Affinity.
	b.WriteString("## Affinity\n\n")
	fmt.Fprintf(&b, "%s\n\n", types.AffinityDefinition)
	if len(r.Affinity.Pairs) > 0 {
		b.WriteString("```mermaid\n")
		if err := writeMermaid(&b, affinityGraphIR(r.Affinity)); err != nil {
			return err
		}
		b.WriteString("```\n\n")
		b.WriteString("| Count | Hidden | Projects |\n|--:|:-:|---|\n")
		for _, c := range r.Affinity.Pairs {
			fmt.Fprintf(&b, "| %d | %s | `%s` ↔ `%s` |\n", c.Count, checkbox(c.Hidden), c.A, c.B)
		}
		b.WriteString("\n")
	}

	// Ownership.
	b.WriteString("## Ownership\n\n")
	fmt.Fprintf(&b, "%s\n\n", types.OwnershipDefinition)
	if len(r.Ownership.Projects) > 0 {
		b.WriteString("| Primary share | Bus factor 1 | Stale | Authors | Primary | Project |\n|--:|:-:|:-:|--:|---|---|\n")
		for _, o := range r.Ownership.Projects {
			fmt.Fprintf(&b, "| %d%% | %s | %s | %d | %s | `%s` |\n",
				o.PrimaryShare, checkbox(o.BusFactor1), checkbox(o.Stale), o.Authors, mdAuthor(o.Primary), o.Path)
		}
		b.WriteString("\n")
	}

	// Trend.
	b.WriteString("## Trend\n\n")
	fmt.Fprintf(&b, "%s\n\n", types.TrendDefinition)
	if len(r.Trend.Projects) > 0 {
		b.WriteString("| Delta | Recent | Earlier | Project |\n|--:|--:|--:|---|\n")
		for _, t := range r.Trend.Projects {
			fmt.Fprintf(&b, "| %+d | %d | %d | `%s` |\n", t.Delta, t.Recent, t.Earlier, t.Path)
		}
		b.WriteString("\n")
	}

	writeStructureSection(&b, r.Structure)

	_, err := io.WriteString(w, b.String())
	return err
}

// writeStructureSection renders the knowledge-graph structural lens into the
// combined report: god nodes, orphans, and doc coverage. Empty when no graph was
// built (the report is best-effort about the structural section).
func writeStructureSection(b *strings.Builder, s types.KnowledgeStructure) {
	if s.NodeCount == 0 {
		return
	}
	b.WriteString("## Structure\n\n")
	fmt.Fprintf(b, "%s\n\n", types.KnowledgeStructureDefinition)

	if len(s.Gods) > 0 {
		b.WriteString("**God nodes** (most connected):\n\n")
		b.WriteString("| Degree | In | Out | Kind | Label |\n|--:|--:|--:|---|---|\n")
		for _, g := range s.Gods {
			fmt.Fprintf(b, "| %d | %d | %d | %s | `%s` |\n", g.Degree, g.In, g.Out, g.Kind, g.Label)
		}
		b.WriteString("\n")
	}
	if len(s.Orphans) > 0 {
		b.WriteString("**Orphans** (neglected):\n\n")
		b.WriteString("| Kind | Label | Why |\n|---|---|---|\n")
		for _, o := range s.Orphans {
			fmt.Fprintf(b, "| %s | `%s` | %s |\n", o.Kind, o.Label, o.Reason)
		}
		b.WriteString("\n")
	}
	if len(s.Coverage) > 0 {
		b.WriteString("**Doc coverage:**\n\n")
		b.WriteString("| Kind | Documented | Coverage | Missing |\n|---|--:|--:|---|\n")
		for _, c := range s.Coverage {
			missing := strings.Join(c.Undocumented, ", ")
			fmt.Fprintf(b, "| %s | %d/%d | %d%% | %s |\n", c.Kind, c.Documented, c.Total, c.Percent, missing)
		}
		b.WriteString("\n")
	}
}

func windowText(commits int, since string) string {
	s := fmt.Sprintf("last %d commits", commits)
	if since != "" {
		s += " within " + since
	}
	return s
}

func topFiles(files []types.FileHotspot, n int) []types.FileHotspot {
	if len(files) > n {
		return files[:n]
	}
	return files
}

func checkbox(b bool) string {
	if b {
		return "⚠️"
	}
	return ""
}

func mdAuthor(a string) string {
	if a == "" {
		return "—"
	}
	return a
}
