package render

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"

	"github.com/egladman/magus/types"
)

func knowledgeSubgraph() types.KnowledgeGraphOutput {
	return types.KnowledgeGraphOutput{
		SchemaVersion: 1,
		Directed:      true,
		NodeCount:     3,
		EdgeCount:     2,
		Nodes: []types.KnowledgeNode{
			{ID: "spell:go", Kind: types.KindSpell, Label: "go"},
			{ID: "op:go:go-build", Kind: types.KindOp, Label: "go-build"},
			{ID: "target:pkg/a:build", Kind: types.KindTarget, Label: "build"},
		},
		Links: []types.KnowledgeEdge{
			{Source: "spell:go", Target: "op:go:go-build", Relation: "contains", Confidence: "extracted", Score: 1},
			{Source: "target:pkg/a:build", Target: "spell:go", Relation: "uses", Confidence: "extracted", Score: 1},
			// An edge to a node outside the subgraph is dropped by both formats.
			{Source: "target:pkg/a:build", Target: "spell:missing", Relation: "uses", Confidence: "extracted", Score: 1},
		},
	}
}

func TestWriteKnowledgeMermaid(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteKnowledgeMermaid(&buf, knowledgeSubgraph()); err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	for _, want := range []string{
		"graph TD",
		`|"contains"|`,        // relation rides the edge label
		`|"uses"|`,            // both intra-subgraph relations present
		"classDef kind_spell", // nodes colored by kind
		"classDef kind_op",
		"classDef kind_target",
		`("go")`, // spell shape is rounded
	} {
		if !strings.Contains(got, want) {
			t.Errorf("mermaid output missing %q\n%s", want, got)
		}
	}
	// The edge to a node outside the subgraph must not appear.
	if strings.Contains(got, "spell:missing") || strings.Contains(got, "spell_missing") {
		t.Errorf("mermaid leaked an out-of-subgraph edge target\n%s", got)
	}
}

func TestWriteKnowledgeDOT(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteKnowledgeDOT(&buf, knowledgeSubgraph()); err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	for _, want := range []string{
		"digraph knowledge {",
		`"spell:go";`,                         // raw IDs are the DOT identity
		`"target:pkg/a:build";`,               // colon/slash IDs survive quoting
		`"spell:go" -> "op:go:go-build";`,     // contains edge
		`"target:pkg/a:build" -> "spell:go";`, // uses edge
	} {
		if !strings.Contains(got, want) {
			t.Errorf("dot output missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "spell:missing") {
		t.Errorf("dot leaked an out-of-subgraph edge target\n%s", got)
	}
}

func graphMLFixture() types.KnowledgeGraphOutput {
	return types.KnowledgeGraphOutput{
		SchemaVersion: 1,
		Directed:      true,
		NodeCount:     2,
		EdgeCount:     1,
		Nodes: []types.KnowledgeNode{
			{ID: "spell:go", Kind: "spell", Label: "go", Doc: "Go toolchain <adapter>", Attrs: map[string]string{"claims": "net, exec"}},
			{ID: "target:pkg/a:build", Kind: "target", Label: "build", Source: "pkg/a/magusfile.buzz:3"},
		},
		Links: []types.KnowledgeEdge{
			{Source: "target:pkg/a:build", Target: "spell:go", Relation: "uses", Confidence: "extracted", Score: 1, Provenance: "pkg/a/magusfile.buzz:3"},
		},
	}
}

func TestWriteKnowledgeGraphML(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteKnowledgeGraphML(&buf, graphMLFixture()); err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	// Well-formed XML end to end.
	dec := xml.NewDecoder(strings.NewReader(got))
	for {
		_, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("output is not well-formed XML: %v\n%s", err, got)
		}
	}

	for _, want := range []string{
		`<key id="kind" for="node" attr.name="kind" attr.type="string"/>`,
		`<key id="attr_claims" for="node" attr.name="claims" attr.type="string"/>`,
		`<key id="score" for="edge" attr.name="score" attr.type="double"/>`,
		`<graph id="magus-knowledge" edgedefault="directed">`,
		`<node id="spell:go">`,
		`<data key="doc">Go toolchain &lt;adapter&gt;</data>`,
		`<data key="attr_claims">net, exec</data>`,
		`<edge source="target:pkg/a:build" target="spell:go">`,
		`<data key="relation">uses</data>`,
		`<data key="score">1</data>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n%s", want, got)
		}
	}
}

func TestWriteKnowledgeGraphMLOmitsEmptyData(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteKnowledgeGraphML(&buf, graphMLFixture()); err != nil {
		t.Fatal(err)
	}
	// Node target:pkg/a:build has no Doc; no empty <data key="doc"></data> may appear.
	if strings.Contains(buf.String(), `<data key="doc"></data>`) {
		t.Fatalf("empty data element emitted:\n%s", buf.String())
	}
}

func TestWriteKnowledgeGraphMLDeterministic(t *testing.T) {
	var a, b bytes.Buffer
	if err := WriteKnowledgeGraphML(&a, graphMLFixture()); err != nil {
		t.Fatal(err)
	}
	if err := WriteKnowledgeGraphML(&b, graphMLFixture()); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Fatal("two writes of the same graph differ")
	}
}
