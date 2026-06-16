package render

import (
	"fmt"
	"io"
	"slices"
	"strings"
)

// RenderGraph is the format-agnostic model both the project graph (GraphOutput)
// and the target graph (TargetGraphOutput) map to via their adapters
// (projectGraphIR, targetGraphIR). writeMermaid and writeDOT are the only code
// that knows the Mermaid / Graphviz syntax, so each output format lives in one
// place regardless of which graph produced it.
type RenderGraph struct {
	Title   string // Mermaid frontmatter title; "" omits the frontmatter block
	DOTName string // digraph <DOTName> { ... }
	Nodes   []RenderNode
	Edges   []RenderEdge
	Groups  []RenderGroup // subgraphs; a child lists its enclosing group in Parent
	Classes []RenderClass // classDef definitions, in emission order
}

// RenderShape selects a node's Mermaid shape (DOT renders every node as the same box).
type RenderShape int

const (
	ShapeBox     RenderShape = iota // id["label"]
	ShapeRounded                    // id("label")
	ShapeHexagon                    // id{{"label"}}
)

// RenderNode is one node. ID is the Mermaid-safe identifier (and the key edges
// reference); DOTID is the (unescaped, quoted) identity used in DOT output.
type RenderNode struct {
	ID       string
	DOTID    string
	Label    string // Mermaid display label (may contain <br/>)
	Shape    RenderShape
	Classes  []string // Mermaid class names assigned to this node
	Group    string   // enclosing group ID; "" = top level
	ClickURL string   // Mermaid click target; "" = no handler
	ClickTip string
}

// RenderEdge is a raw dependency edge between node IDs. Endpoint collapsing onto a
// group (Mermaid stage boxes) is applied by writeMermaid, not stored here, so DOT
// can render the same edges flat.
type RenderEdge struct {
	From, To string
	Label    string // Mermaid edge label; "" = plain edge
}

// RenderGroup is a subgraph. Collapse routes Mermaid edges touching a member node
// onto the group itself (so an edge lands on the box, not a node buried inside it).
type RenderGroup struct {
	ID, Label string
	Parent    string // enclosing group ID; "" = top level
	Collapse  bool
}

// RenderClass is one Mermaid classDef.
type RenderClass struct {
	Name, Style string
}

// writeMermaid serializes a RenderGraph as a Mermaid flowchart.
func writeMermaid(w io.Writer, g RenderGraph) error {
	var b strings.Builder
	if g.Title != "" {
		fmt.Fprintf(&b, "---\ntitle: %s\n---\n", g.Title)
	}
	b.WriteString("graph TD\n")
	if len(g.Edges) == 0 {
		b.WriteString("  %% no dependency edges\n")
	}

	nodeByID := make(map[string]RenderNode, len(g.Nodes))
	for _, n := range g.Nodes {
		nodeByID[n.ID] = n
	}
	groupByID := make(map[string]RenderGroup, len(g.Groups))
	for _, gr := range g.Groups {
		groupByID[gr.ID] = gr
	}
	nodesByGroup := map[string][]RenderNode{}
	for _, n := range g.Nodes {
		nodesByGroup[n.Group] = append(nodesByGroup[n.Group], n)
	}
	childGroups := map[string][]RenderGroup{}
	var topGroups []RenderGroup
	for _, gr := range g.Groups {
		if gr.Parent == "" {
			topGroups = append(topGroups, gr)
		} else {
			childGroups[gr.Parent] = append(childGroups[gr.Parent], gr)
		}
	}

	emitNode := func(n RenderNode, indent string) {
		switch n.Shape {
		case ShapeRounded:
			fmt.Fprintf(&b, "%s%s(%q)\n", indent, n.ID, n.Label)
		case ShapeHexagon:
			fmt.Fprintf(&b, "%s%s{{%q}}\n", indent, n.ID, n.Label)
		default:
			fmt.Fprintf(&b, "%s%s[%q]\n", indent, n.ID, n.Label)
		}
	}
	var emitGroup func(gr RenderGroup, indent string)
	emitGroup = func(gr RenderGroup, indent string) {
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
		if e.Label != "" {
			fmt.Fprintf(&b, "  %s -->|%q| %s\n", from, e.Label, to)
		} else {
			fmt.Fprintf(&b, "  %s --> %s\n", from, to)
		}
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

	for _, n := range g.Nodes {
		if n.ClickURL != "" {
			fmt.Fprintf(&b, "  click %s %q %q\n", n.ID, n.ClickURL, n.ClickTip)
		}
	}

	_, err := io.WriteString(w, b.String())
	return err
}

// writeDOT serializes a RenderGraph as a Graphviz DOT digraph (flat: groups are
// ignored, self-edges dropped). Node identity is DOTID; edges map their endpoint
// IDs back to the corresponding node's DOTID.
func writeDOT(w io.Writer, g RenderGraph) error {
	dotID := make(map[string]string, len(g.Nodes))
	for _, n := range g.Nodes {
		dotID[n.ID] = n.DOTID
	}
	resolveDOT := func(id string) string {
		if d, ok := dotID[id]; ok {
			return d
		}
		return id // undeclared edge endpoint: fall back to the raw id
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
		from, to := resolveDOT(e.From), resolveDOT(e.To)
		if from == to {
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
