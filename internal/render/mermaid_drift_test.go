package render

// TestMermaidClassDefDrift asserts the Go Mermaid emitters write the classDef names their
// consumers style against.
//
// It used to be a two-way lock against a JS twin in console/src/console/graph/mermaid.ts.
// That twin is gone: the browser's "Copy as Mermaid" affordance was removed, so nothing in
// the console emits Mermaid and there is no second implementation to drift from. The CLI's
// `-o mermaid` is now the only emitter, and this is its own side of the old contract.
//
// Where each class name is emitted:
//
//	"anchor", "target"   - targetGraphIR -> WriteTargetGraphMermaid
//	"kind_*"             - knowledgeGraphIR -> WriteKnowledgeMermaid

import (
	"bytes"
	"strings"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/require"
)

// targetsClassDefNames are the exact classDef name strings the Go targets-flavor
// emitter writes. They are listed here explicitly so that renaming one in
// targetgraph.go without updating this list causes a test failure. Only the two
// role classes are emitted now; MAGUS.md no longer embeds per-project graphs.
var targetsClassDefNames = []string{
	"anchor", // targetRoleClasses[0].Name (targetgraph.go)
	"target", // targetRoleClasses[1].Name (targetgraph.go)
}

// knowledgeClassDefNames are kind_<kind> names from knowledgeKindPalette in
// knowledgegraph.go.
var knowledgeClassDefNames = []string{
	"kind_project",
	"kind_spell",
	"kind_target",
	"kind_op",
	"kind_charm",
	"kind_module",
	"kind_method",
	"kind_diagnostic",
	"kind_doc",
}

func TestMermaidClassDefDrift(t *testing.T) {
	// -- side A: verify the Go emitters produce the expected classDef names ------

	t.Run("go_targets_emitter_anchor_target", func(t *testing.T) {
		// WriteTargetGraphMermaid emits "anchor" and "target" roles via targetGraphIR.
		// A project with one depended-on target (target role) and one leaf (anchor role).
		out := types.TargetGraphOutput{
			Projects: []types.TargetGraphProject{{
				Path: ".",
				Nodes: []types.TargetGraphNode{
					{Name: "ci", Dependencies: []string{"build"}}, // anchor (nothing depends on ci)
					{Name: "build"}, // target (ci depends on build)
				},
			}},
		}
		var buf bytes.Buffer
		require.NoError(t, WriteTargetGraphMermaid(&buf, out))
		got := buf.String()
		// Use a space suffix to avoid "classDef anchor" matching "classDef anchor2".
		for _, name := range targetsClassDefNames {
			require.True(t, strings.Contains(got, "classDef "+name+" "),
				"WriteTargetGraphMermaid output missing classDef %q - "+
					"update targetsClassDefNames in this test to match targetgraph.go", name)
		}
	})

	t.Run("go_knowledge_emitter", func(t *testing.T) {
		// Build a KnowledgeGraphOutput with one node of every kind in the palette
		// so that all kind_* classDefs appear in the output.
		var nodes []types.KnowledgeNode
		var links []types.KnowledgeEdge
		prev := ""
		for i, k := range []string{
			types.KindProject, types.KindSpell, types.KindTarget, types.KindOp,
			types.KindCharm, types.KindModule, types.KindMethod, types.KindDiagnostic,
			types.KindDoc,
		} {
			id := k + ":test"
			nodes = append(nodes, types.KnowledgeNode{ID: id, Kind: k, Label: k})
			if i > 0 {
				links = append(links, types.KnowledgeEdge{Source: prev, Target: id, Relation: "references"})
			}
			prev = id
		}
		out := types.KnowledgeGraphOutput{Nodes: nodes, Links: links}
		var buf bytes.Buffer
		require.NoError(t, WriteKnowledgeMermaid(&buf, out))
		got := buf.String()
		for _, name := range knowledgeClassDefNames {
			require.True(t, strings.Contains(got, "classDef "+name+" "),
				"WriteKnowledgeMermaid output missing classDef %q - "+
					"update knowledgeClassDefNames in this test to match knowledgegraph.go", name)
		}
	})
}
