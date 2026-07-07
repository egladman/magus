package render

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/render/md"
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
	var b md.Builder

	b.Heading(1, "Insight")
	b.Comment("Generated by `magus insight report`. A point-in-time snapshot of VCS history; regenerate to refresh.")
	b.Paragraph(types.InsightDefinition)
	b.Paragraphf("_Window: %s._", windowText(r.Hotspots.Commits, r.Hotspots.Since))

	// Hotspots.
	b.Heading(2, "Hotspots")
	b.Paragraph(types.HotspotDefinition)
	if err := b.Fenced("mermaid", func(w io.Writer) error {
		return writeMermaid(w, hotspotGraphIR(r.Hotspots))
	}); err != nil {
		return err
	}
	if len(r.Hotspots.Files) > 0 {
		if err := b.Fenced("mermaid", func(w io.Writer) error {
			return WriteHotspotQuadrant(w, r.Hotspots)
		}); err != nil {
			return err
		}
		rows := make([][]string, 0, 20)
		for _, f := range topFiles(r.Hotspots.Files, 20) {
			rows = append(rows, []string{
				strconv.Itoa(f.Score), strconv.Itoa(f.Commits), strconv.Itoa(f.Complexity),
				strconv.Itoa(f.Authors), md.Code(f.Path),
			})
		}
		b.Table([]string{"Score", "Commits", "Complexity", "Authors", "File"},
			[]md.Align{md.Right, md.Right, md.Right, md.Right}, rows)
	}

	// Affinity.
	b.Heading(2, "Affinity")
	b.Paragraph(types.AffinityDefinition)
	if len(r.Affinity.Pairs) > 0 {
		if err := b.Fenced("mermaid", func(w io.Writer) error {
			return writeMermaid(w, affinityGraphIR(r.Affinity))
		}); err != nil {
			return err
		}
		rows := make([][]string, 0, len(r.Affinity.Pairs))
		for _, c := range r.Affinity.Pairs {
			rows = append(rows, []string{
				strconv.Itoa(c.Count), checkbox(c.Hidden),
				md.Code(c.A) + " ↔ " + md.Code(c.B),
			})
		}
		b.Table([]string{"Count", "Hidden", "Projects"},
			[]md.Align{md.Right, md.Center}, rows)
	}

	// Ownership.
	b.Heading(2, "Ownership")
	b.Paragraph(types.OwnershipDefinition)
	rows := make([][]string, 0, len(r.Ownership.Projects))
	for _, o := range r.Ownership.Projects {
		rows = append(rows, []string{
			fmt.Sprintf("%d%%", o.PrimaryShare), checkbox(o.BusFactor1), checkbox(o.Stale),
			strconv.Itoa(o.Authors), mdAuthor(o.Primary), md.Code(o.Path),
		})
	}
	b.Table([]string{"Primary share", "Bus factor 1", "Stale", "Authors", "Primary", "Project"},
		[]md.Align{md.Right, md.Center, md.Center, md.Right}, rows)

	// Trend.
	b.Heading(2, "Trend")
	b.Paragraph(types.TrendDefinition)
	rows = rows[:0]
	for _, t := range r.Trend.Projects {
		rows = append(rows, []string{
			fmt.Sprintf("%+d", t.Delta), strconv.Itoa(t.Recent), strconv.Itoa(t.Earlier), md.Code(t.Path),
		})
	}
	b.Table([]string{"Delta", "Recent", "Earlier", "Project"},
		[]md.Align{md.Right, md.Right, md.Right}, rows)

	writeGraphStatsSection(&b, r.GraphStats)

	_, err := b.WriteTo(w)
	return err
}

// writeGraphStatsSection renders the knowledge-graph axis (`magus graph stats`)
// into the combined report: god nodes, orphans, and doc coverage. Empty when no
// graph was built (the report is best-effort about this section).
func writeGraphStatsSection(b *md.Builder, s types.KnowledgeStats) {
	if s.NodeCount == 0 {
		return
	}
	b.Heading(2, "Graph stats")
	b.Paragraph(types.KnowledgeStatsDefinition)

	if len(s.Gods) > 0 {
		b.Paragraph(md.Bold("God nodes") + " (most connected):")
		rows := make([][]string, 0, len(s.Gods))
		for _, g := range s.Gods {
			rows = append(rows, []string{
				strconv.Itoa(g.Degree), strconv.Itoa(g.In), strconv.Itoa(g.Out), g.Kind, md.Code(g.Label),
			})
		}
		b.Table([]string{"Degree", "In", "Out", "Kind", "Label"},
			[]md.Align{md.Right, md.Right, md.Right}, rows)
	}
	if len(s.Orphans) > 0 {
		b.Paragraph(md.Bold("Orphans") + " (neglected):")
		rows := make([][]string, 0, len(s.Orphans))
		for _, o := range s.Orphans {
			rows = append(rows, []string{o.Kind, md.Code(o.Label), o.Reason})
		}
		b.Table([]string{"Kind", "Label", "Why"}, nil, rows)
	}
	if len(s.Coverage) > 0 {
		b.Paragraph(md.Bold("Doc coverage:"))
		rows := make([][]string, 0, len(s.Coverage))
		for _, c := range s.Coverage {
			rows = append(rows, []string{
				c.Kind, fmt.Sprintf("%d/%d", c.Documented, c.Total),
				fmt.Sprintf("%d%%", c.Percent), strings.Join(c.Undocumented, ", "),
			})
		}
		b.Table([]string{"Kind", "Documented", "Coverage", "Missing"},
			[]md.Align{md.Left, md.Right, md.Right}, rows)
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
