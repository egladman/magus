package render

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"slices"

	"github.com/egladman/magus/types"
)

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
