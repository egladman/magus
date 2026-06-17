package render

import (
	"fmt"
	"io"
	"slices"
	"strings"
)

// renderGraph is the format-agnostic model both the project graph (GraphOutput)
// and the target graph (TargetGraphOutput) map to via their adapters
// (projectGraphIR, targetGraphIR). writeMermaid and writeDOT are the only code
// that knows the Mermaid / Graphviz syntax, so each output format lives in one
// place regardless of which graph produced it.
type renderGraph struct {
	Title       string // Mermaid frontmatter title; "" omits it
	Direction   string // Mermaid flow direction (e.g. "LR"); "" defaults to "TD". DOT ignores it.
	NodeSpacing int    // Mermaid flowchart.nodeSpacing (gap between sibling nodes); 0 omits
	RankSpacing int    // Mermaid flowchart.rankSpacing (gap between dependency levels); 0 omits
	DOTName     string // digraph <DOTName> { ... }
	Nodes       []renderNode
	Edges       []renderEdge
	Groups      []renderGroup // subgraphs; a child lists its enclosing group in Parent
	Classes     []renderClass // classDef definitions, in emission order
}

// renderShape selects a node's Mermaid shape (DOT renders every node as the same box).
type renderShape int

const (
	shapeBox        renderShape = iota // id["label"]
	shapeRounded                       // id("label")
	shapeHexagon                       // id{{"label"}}
	shapeSubroutine                    // id[["label"]]  — external/predefined process
)

// renderNode is one node. ID is the Mermaid-safe identifier (and the key edges
// reference); DOTID is the (unescaped, quoted) identity used in DOT output.
type renderNode struct {
	ID       string
	DOTID    string
	Label    string // Mermaid display label (may contain <br/>)
	Shape    renderShape
	Classes  []string // Mermaid class names assigned to this node
	Group    string   // enclosing group ID; "" = top level
	ClickURL string   // Mermaid click target; "" = no handler
	ClickTip string
}

// renderEdge is a raw dependency edge between node IDs. Endpoint collapsing onto a
// group (Mermaid stage boxes) is applied by writeMermaid, not stored here, so DOT
// can render the same edges flat.
type renderEdge struct {
	From, To string
	Label    string // Mermaid edge label; "" = plain edge
	Dashed   bool   // Mermaid: render as a dotted arrow (-.->), e.g. a cross-project dependency
}

// renderGroup is a subgraph. Collapse routes Mermaid edges touching a member node
// onto the group itself (so an edge lands on the box, not a node buried inside it).
type renderGroup struct {
	ID, Label string
	Parent    string // enclosing group ID; "" = top level
	Collapse  bool
	Style     string // Mermaid `style <id> ...` (e.g. transparent fill+stroke for an invisible grouping); "" = default
}

// renderClass is one Mermaid classDef.
type renderClass struct {
	Name, Style string
}

// writeMermaid serializes a renderGraph as a Mermaid flowchart.
func writeMermaid(w io.Writer, g renderGraph) error {
	var b strings.Builder
	// Frontmatter carries the title and any layout config (nodeSpacing/rankSpacing
	// spread a crowded graph without ELK — both are non-secure, so GitHub honors
	// them). Emitted only when there's something to put in it.
	hasSpacing := g.NodeSpacing > 0 || g.RankSpacing > 0
	if g.Title != "" || hasSpacing {
		b.WriteString("---\n")
		if g.Title != "" {
			fmt.Fprintf(&b, "title: %s\n", g.Title)
		}
		if hasSpacing {
			b.WriteString("config:\n  flowchart:\n")
			if g.NodeSpacing > 0 {
				fmt.Fprintf(&b, "    nodeSpacing: %d\n", g.NodeSpacing)
			}
			if g.RankSpacing > 0 {
				fmt.Fprintf(&b, "    rankSpacing: %d\n", g.RankSpacing)
			}
		}
		b.WriteString("---\n")
	}
	dir := g.Direction
	if dir == "" {
		dir = "TD"
	}
	fmt.Fprintf(&b, "graph %s\n", dir)

	nodeByID := make(map[string]renderNode, len(g.Nodes))
	for _, n := range g.Nodes {
		nodeByID[n.ID] = n
	}
	groupByID := make(map[string]renderGroup, len(g.Groups))
	for _, gr := range g.Groups {
		groupByID[gr.ID] = gr
	}
	nodesByGroup := map[string][]renderNode{}
	for _, n := range g.Nodes {
		nodesByGroup[n.Group] = append(nodesByGroup[n.Group], n)
	}
	childGroups := map[string][]renderGroup{}
	var topGroups []renderGroup
	for _, gr := range g.Groups {
		if gr.Parent == "" {
			topGroups = append(topGroups, gr)
		} else {
			childGroups[gr.Parent] = append(childGroups[gr.Parent], gr)
		}
	}

	emitNode := func(n renderNode, indent string) {
		switch n.Shape {
		case shapeRounded:
			fmt.Fprintf(&b, "%s%s(%q)\n", indent, n.ID, n.Label)
		case shapeHexagon:
			fmt.Fprintf(&b, "%s%s{{%q}}\n", indent, n.ID, n.Label)
		case shapeSubroutine:
			fmt.Fprintf(&b, "%s%s[[%q]]\n", indent, n.ID, n.Label)
		default:
			fmt.Fprintf(&b, "%s%s[%q]\n", indent, n.ID, n.Label)
		}
	}
	var emitGroup func(gr renderGroup, indent string)
	emitGroup = func(gr renderGroup, indent string) {
		fmt.Fprintf(&b, "%ssubgraph %s[%q]\n", indent, gr.ID, gr.Label)
		for _, cg := range childGroups[gr.ID] {
			emitGroup(cg, indent+"  ")
		}
		for _, n := range nodesByGroup[gr.ID] {
			emitNode(n, indent+"  ")
		}
		fmt.Fprintf(&b, "%send\n", indent)
	}
	for _, gr := range topGroups {
		emitGroup(gr, "  ")
	}
	for _, n := range nodesByGroup[""] {
		emitNode(n, "  ")
	}

	// resolve routes an edge endpoint onto its group when that group collapses.
	resolve := func(id string) string {
		if n, ok := nodeByID[id]; ok && n.Group != "" {
			if gr, ok := groupByID[n.Group]; ok && gr.Collapse {
				return gr.ID
			}
		}
		return id
	}
	seen := map[string]bool{}
	wroteEdge := false
	for _, e := range g.Edges {
		from, to := resolve(e.From), resolve(e.To)
		if from == to {
			continue
		}
		key := from + "\x00" + to + "\x00" + e.Label
		if seen[key] {
			continue
		}
		seen[key] = true
		arrow := "-->"
		if e.Dashed {
			arrow = "-.->"
		}
		if e.Label != "" {
			fmt.Fprintf(&b, "  %s %s|%q| %s\n", from, arrow, e.Label, to)
		} else {
			fmt.Fprintf(&b, "  %s %s %s\n", from, arrow, to)
		}
		wroteEdge = true
	}
	// Decided after the loop (not from len(g.Edges)) so a graph whose every edge
	// collapses onto a single stage box still gets the note.
	if !wroteEdge {
		b.WriteString("  %% no dependency edges\n")
	}

	for _, c := range g.Classes {
		fmt.Fprintf(&b, "  classDef %s %s\n", c.Name, c.Style)
	}
	for _, c := range g.Classes {
		var ids []string
		for _, n := range g.Nodes {
			if slices.Contains(n.Classes, c.Name) {
				ids = append(ids, n.ID)
			}
		}
		if ids = uniqSorted(ids); len(ids) > 0 {
			fmt.Fprintf(&b, "  class %s %s\n", strings.Join(ids, ","), c.Name)
		}
	}

	for _, gr := range g.Groups {
		if gr.Style != "" {
			fmt.Fprintf(&b, "  style %s %s\n", gr.ID, gr.Style)
		}
	}

	for _, n := range g.Nodes {
		if n.ClickURL != "" {
			fmt.Fprintf(&b, "  click %s %q %q\n", n.ID, n.ClickURL, n.ClickTip)
		}
	}

	_, err := io.WriteString(w, b.String())
	return err
}

// writeDOT serializes a renderGraph as a Graphviz DOT digraph (flat: groups are
// ignored, self-edges dropped). Node identity is DOTID; edges map their endpoint
// IDs back to the corresponding node's DOTID.
func writeDOT(w io.Writer, g renderGraph) error {
	dotID := make(map[string]string, len(g.Nodes))
	for _, n := range g.Nodes {
		dotID[n.ID] = n.DOTID
	}
	var b strings.Builder
	fmt.Fprintf(&b, "digraph %s {\n", g.DOTName)
	b.WriteString("  rankdir=LR;\n")
	b.WriteString("  node [shape=box, style=rounded];\n")
	b.WriteString("\n")
	for _, n := range g.Nodes {
		fmt.Fprintf(&b, "  %q;\n", n.DOTID)
	}
	var edges []string
	for _, e := range g.Edges {
		// DOT is flat: it has no subgraphs, so an edge whose endpoint is a group id
		// (e.g. a cross-project edge between two project subgraphs) has no node to
		// land on. Drop it rather than fall back to the raw id, which would make
		// Graphviz auto-create a phantom, unlabeled node. Mermaid keeps these edges
		// — a subgraph id is a legal Mermaid edge endpoint.
		from, fromOK := dotID[e.From]
		to, toOK := dotID[e.To]
		if !fromOK || !toOK || from == to {
			continue
		}
		edges = append(edges, fmt.Sprintf("  %q -> %q;\n", from, to))
	}
	if len(edges) > 0 {
		b.WriteString("\n")
		for _, line := range edges {
			b.WriteString(line)
		}
	}
	b.WriteString("}\n")

	_, err := io.WriteString(w, b.String())
	return err
}
