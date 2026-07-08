package render

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"slices"

	"github.com/egladman/magus/types"
)

// WriteKnowledgeDOT emits a knowledge-graph export as a Graphviz DOT digraph.
// Node identity is the raw node ID; the format is structural (edges only), so
// relations and Attrs are dropped - GraphML carries the full detail. Meant for a
// selected neighborhood, not the whole graph (an unfiltered dump is unreadable).
func WriteKnowledgeDOT(w io.Writer, out types.KnowledgeGraphOutput) error {
	return writeDOT(w, knowledgeGraphIR(out))
}

// WriteKnowledgeMermaid emits a knowledge-graph export as a Mermaid flowchart:
// nodes colored by kind, edges labeled with their relation. Meant for a selected
// neighborhood - Mermaid chokes on thousands of nodes, so the CLI gates it behind
// --select.
func WriteKnowledgeMermaid(w io.Writer, out types.KnowledgeGraphOutput) error {
	return writeMermaid(w, knowledgeGraphIR(out))
}

// knowledgeGraphIR maps a knowledge-graph export onto the shared renderGraph:
// each node keeps its raw ID as the DOT identity and a Mermaid-safe alias, is
// colored by a per-kind class, and takes a kind-specific shape; each edge carries
// its relation as the label. Deterministic: the export is already sorted, and the
// alias assignment (mermaidIDs) sorts internally.
func knowledgeGraphIR(out types.KnowledgeGraphOutput) renderGraph {
	rawIDs := make([]string, len(out.Nodes))
	for i, n := range out.Nodes {
		rawIDs[i] = n.ID
	}
	alias := mermaidIDs(rawIDs)

	g := renderGraph{Title: "magus knowledge graph", DOTName: "knowledge"}

	kindSet := map[string]bool{}
	for _, n := range out.Nodes {
		label := n.Label
		if label == "" {
			label = n.ID
		}
		kindSet[n.Kind] = true
		g.Nodes = append(g.Nodes, renderNode{
			ID:      alias[n.ID],
			DOTID:   n.ID,
			Label:   label,
			Shape:   knowledgeShape(n.Kind),
			Classes: []string{"kind_" + mermaidID(n.Kind)},
		})
	}
	for _, e := range out.Links {
		from, okF := alias[e.Source]
		to, okT := alias[e.Target]
		if !okF || !okT {
			continue // edge to a node outside the selected subgraph; skip
		}
		g.Edges = append(g.Edges, renderEdge{From: from, To: to, Label: e.Relation})
	}

	kinds := make([]string, 0, len(kindSet))
	for k := range kindSet {
		kinds = append(kinds, k)
	}
	slices.Sort(kinds)
	for _, k := range kinds {
		fill, text := knowledgeKindColor(k)
		g.Classes = append(g.Classes, renderClass{Name: "kind_" + mermaidID(k), Style: fmt.Sprintf("fill:%s,color:%s", fill, text)})
	}
	return g
}

// knowledgeShape anchors the primary containers (project, spell) with distinct
// Mermaid shapes so a neighborhood reads at a glance; everything else is a box.
// DOT ignores shape.
func knowledgeShape(kind string) renderShape {
	switch kind {
	case types.KindProject:
		return shapeHexagon
	case types.KindSpell:
		return shapeRounded
	case types.KindDoc:
		return shapeSubroutine
	default:
		return shapeBox
	}
}

// knowledgeKindPalette maps a node kind to a fill/text color. Unknown kinds fall
// back to a neutral gray.
var knowledgeKindPalette = map[string]struct{ fill, text string }{
	types.KindProject:    {"#00ADD8", "#fff"},
	types.KindTarget:     {"#3178C6", "#fff"},
	types.KindSpell:      {"#5d4d7a", "#fff"},
	types.KindOp:         {"#8a7ca8", "#fff"},
	types.KindCharm:      {"#b5651d", "#fff"},
	types.KindModule:     {"#2e8b57", "#fff"},
	types.KindMethod:     {"#3cb371", "#000"},
	types.KindDiagnostic: {"#c0392b", "#fff"},
	types.KindDoc:        {"#d4a017", "#000"},
}

func knowledgeKindColor(kind string) (fill, text string) {
	if c, ok := knowledgeKindPalette[kind]; ok {
		return c.fill, c.text
	}
	return "#888888", "#fff"
}

// WriteKnowledgeGraphML emits the merged knowledge graph as GraphML, the XML
// graph format external viewers (Gephi, yEd) open directly - the second export
// format next to node-link JSON. Every node/edge field becomes a declared
// <key>; kind-specific node Attrs are declared as attr_<name> keys collected
// across the whole graph, so the schema is self-describing. Output is
// deterministic: nodes/edges are written in input order (the export is already
// sorted) and attr keys are sorted.
func WriteKnowledgeGraphML(w io.Writer, out types.KnowledgeGraphOutput) error {
	var b bytes.Buffer // accumulate, then one write - keeps the body free of per-line error checks

	b.WriteString(xml.Header)
	b.WriteString(`<graphml xmlns="http://graphml.graphdrawing.org/xmlns">` + "\n")

	fmt.Fprintf(&b, "  <!-- magus knowledge graph, schema v%d -->\n", out.SchemaVersion)
	for _, k := range []string{"kind", "label", "doc", "source"} {
		fmt.Fprintf(&b, `  <key id="%s" for="node" attr.name="%s" attr.type="string"/>`+"\n", k, k)
	}
	for _, a := range attrKeys(out.Nodes) {
		fmt.Fprintf(&b, `  <key id="attr_%s" for="node" attr.name="%s" attr.type="string"/>`+"\n", xmlEscape(a), xmlEscape(a))
	}
	for _, k := range []string{"relation", "confidence", "provenance"} {
		fmt.Fprintf(&b, `  <key id="%s" for="edge" attr.name="%s" attr.type="string"/>`+"\n", k, k)
	}
	b.WriteString(`  <key id="score" for="edge" attr.name="score" attr.type="double"/>` + "\n")

	b.WriteString(`  <graph id="magus-knowledge" edgedefault="directed">` + "\n")
	for _, n := range out.Nodes {
		fmt.Fprintf(&b, `    <node id="%s">`+"\n", xmlEscape(n.ID))
		writeGraphMLData(&b, "kind", n.Kind)
		writeGraphMLData(&b, "label", n.Label)
		writeGraphMLData(&b, "doc", n.Doc)
		writeGraphMLData(&b, "source", n.Source)
		for _, k := range sortedKeys(n.Attrs) {
			writeGraphMLData(&b, "attr_"+k, n.Attrs[k])
		}
		b.WriteString("    </node>\n")
	}
	for _, e := range out.Links {
		fmt.Fprintf(&b, `    <edge source="%s" target="%s">`+"\n", xmlEscape(e.Source), xmlEscape(e.Target))
		writeGraphMLData(&b, "relation", e.Relation)
		writeGraphMLData(&b, "confidence", e.Confidence)
		fmt.Fprintf(&b, `      <data key="score">%g</data>`+"\n", e.Score)
		writeGraphMLData(&b, "provenance", e.Provenance)
		b.WriteString("    </edge>\n")
	}
	b.WriteString("  </graph>\n</graphml>\n")

	_, err := w.Write(b.Bytes())
	return err
}

// writeGraphMLData writes one <data> element, omitting empty values so the
// output stays compact (GraphML consumers treat a missing key as unset).
func writeGraphMLData(b *bytes.Buffer, key, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(b, `      <data key="%s">%s</data>`+"\n", key, xmlEscape(value))
}

// attrKeys collects the distinct Attrs keys across all nodes, sorted.
func attrKeys(nodes []types.KnowledgeNode) []string {
	seen := map[string]bool{}
	var keys []string
	for _, n := range nodes {
		for k := range n.Attrs {
			if !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
	}
	slices.Sort(keys)
	return keys
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// xmlEscape escapes s for use in XML character data and attribute values.
func xmlEscape(s string) string {
	var b bytes.Buffer
	// EscapeText only fails on a failing writer; bytes.Buffer never fails.
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
