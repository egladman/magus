package render

import (
	"fmt"
	"io"
	"strings"
)

// dotGraph is the flat model writeDOT serializes. The project, target and knowledge
// graphs each map onto it, so the DOT syntax lives in one place.
type dotGraph struct {
	Name  string // digraph <Name> { ... }
	Nodes []string
	Edges [][2]string // [from, to]; an endpoint that names no node is dropped
}

// writeDOT serializes a dotGraph as a Graphviz DOT digraph, dropping self-edges.
func writeDOT(w io.Writer, g dotGraph) error {
	known := make(map[string]bool, len(g.Nodes))
	for _, n := range g.Nodes {
		known[n] = true
	}
	var b strings.Builder
	fmt.Fprintf(&b, "digraph %s {\n", g.Name)
	b.WriteString("  rankdir=LR;\n")
	b.WriteString("  node [shape=box, style=rounded];\n")
	b.WriteString("\n")
	for _, n := range g.Nodes {
		fmt.Fprintf(&b, "  %q;\n", n)
	}
	var edges []string
	for _, e := range g.Edges {
		// An unknown endpoint would make Graphviz auto-create a phantom, unlabeled node.
		if !known[e[0]] || !known[e[1]] || e[0] == e[1] {
			continue
		}
		edges = append(edges, fmt.Sprintf("  %q -> %q;\n", e[0], e[1]))
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
