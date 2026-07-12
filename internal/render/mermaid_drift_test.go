package render

// TestMermaidClassDefDrift is the anti-drift contract for Phase 7 (Copy as
// Mermaid). It enforces a two-way lock between the Go Mermaid emitters and the
// JS implementation in website/src/console/graph/graph-explorer.js:
//
//  1. It runs the Go emitters and asserts the expected classDef names appear in
//     their output.
//  2. It reads website/src/console/graph/graph-explorer.js from disk and asserts the SAME
//     names appear verbatim in the JS source.
//
// If you rename a classDef in either place without updating the other, exactly
// one of the two subtests fails CI. See the KEYWORDS mirror note in
// website/src/playground/editor.js for the house pattern this follows.
//
// Where each class name is emitted in Go:
//
//	"anchor", "target"   - targetGraphIR -> WriteTargetGraphMermaid
//	"external"           - addCrossTargetDependencies -> writeProjectGraph (markdown)
//	"spell"              - writeToolchainGraph (markdown) + spellClass var
//	"kind_*"             - knowledgeGraphIR -> WriteKnowledgeMermaid

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/require"
)

// targetsClassDefNames are the exact classDef name strings the Go targets-flavor
// emitters write. They are listed here explicitly so that renaming one in
// targetgraph.go without updating this list causes a test failure.
var targetsClassDefNames = []string{
	"anchor",   // targetRoleClasses[0].Name (targetgraph.go)
	"target",   // targetRoleClasses[1].Name (targetgraph.go)
	"external", // externalClass.Name        (targetgraph.go)
	"spell",    // spellClass.Name           (targetgraph.go)
}

// knowledgeClassDefNames are kind_<kind> names from knowledgeKindPalette in
// knowledgegraph.go. Each must appear in the JS file's kindClassPalette object.
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
		for _, name := range []string{"anchor", "target"} {
			require.True(t, strings.Contains(got, "classDef "+name+" "),
				"WriteTargetGraphMermaid output missing classDef %q - "+
					"update targetsClassDefNames in this test to match targetgraph.go", name)
		}
	})

	t.Run("go_targets_markdown_external_spell", func(t *testing.T) {
		// WriteTargetGraphMarkdown emits "external" (addCrossTargetDependencies) and
		// "spell" (writeToolchainGraph) inside its per-project mermaid blocks.
		out := types.TargetGraphOutput{
			Projects: []types.TargetGraphProject{{
				Path: ".",
				Nodes: []types.TargetGraphNode{{
					Name:   "build",
					Spells: []types.TargetSpellUse{{Spell: "go", Ops: []string{"build"}}},
					CrossDependencies: []types.CrossTargetRef{
						{Project: "pkg/b", Target: "gen"},
					},
				}},
			}},
		}
		var buf bytes.Buffer
		require.NoError(t, WriteTargetGraphMarkdown(&buf, out, nil, nil, ""))
		got := buf.String()
		for _, name := range []string{"external", "spell"} {
			require.True(t, strings.Contains(got, "classDef "+name+" "),
				"WriteTargetGraphMarkdown output missing classDef %q - "+
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

	// -- side B: verify website/src/console/graph/graph-explorer.js contains the same names ----

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	jsPath := filepath.Join(repoRoot, "website", "src", "console", "graph", "graph-explorer.js")

	data, err := os.ReadFile(jsPath)
	require.NoError(t, err, "could not read %s", jsPath)
	content := string(data)

	t.Run("js_targets_classDefs", func(t *testing.T) {
		// Use a space suffix so "classDef anchor" does not match "classDef anchor2".
		for _, name := range targetsClassDefNames {
			require.True(t, strings.Contains(content, "classDef "+name+" "),
				"JS file missing targets classDef %q - update website/src/console/graph/graph-explorer.js "+
					"(toMermaid targets branch) to match the rename in targetgraph.go",
				name)
		}
	})

	t.Run("js_knowledge_classDefs", func(t *testing.T) {
		require.True(t, strings.Contains(content, "kind_"),
			"JS file missing knowledge classDef prefix \"kind_\"")
		// The kind names appear as object keys: `kind_project:`, `kind_spell:`, etc.
		// Use the colon suffix to ensure exact word boundaries.
		for _, name := range knowledgeClassDefNames {
			require.True(t, strings.Contains(content, name+":"),
				"JS file missing knowledge classDef %q - update website/src/console/graph/graph-explorer.js "+
					"(kindClassPalette in toMermaid knowledge branch) to match knowledgegraph.go",
				name)
		}
	})

	// Anti-drift comment sanity: the cross-reference comments must exist so the
	// next reader knows where to look.
	require.True(t, strings.Contains(content, "internal/render/targetgraph.go"),
		"JS file is missing the anti-drift comment pointing at internal/render/targetgraph.go")
	require.True(t, strings.Contains(content, "internal/render/knowledgegraph.go"),
		"JS file is missing the anti-drift comment pointing at internal/render/knowledgegraph.go")
}
