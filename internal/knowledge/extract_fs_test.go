package knowledge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
}

// findEdge returns the edge matching (source,target,relation), or ok=false.
func findEdge(out types.KnowledgeGraphOutput, source, target, relation string) (types.KnowledgeEdge, bool) {
	for _, e := range out.Links {
		if e.Source == source && e.Target == target && e.Relation == relation {
			return e, true
		}
	}
	return types.KnowledgeEdge{}, false
}

func TestAssembleBuzz(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.buzz", `import "b";
import "magus/spell/go";
export fun build(args: [str]) > void {
    // NOTE: build is tricky
    helper();
}
fun helper() > void {}
`)
	writeFile(t, root, "b.buzz", "export fun thing() > void {}\n")
	// A testdata fixture must be skipped, not extracted.
	writeFile(t, root, "testdata/skip.buzz", "export fun ignored() > void {}\n")

	out := mergeAll([]Shard{assembleBuzz(root)}).Output()

	for _, id := range []string{
		"file:a.buzz", "file:b.buzz",
		"function:a.buzz:build", "function:a.buzz:helper", "function:b.buzz:thing",
	} {
		_, ok := nodeByID(out, id)
		assert.Truef(t, ok, "missing node %q", id)
	}
	_, ok := nodeByID(out, "function:testdata/skip.buzz:ignored")
	assert.False(t, ok, "testdata should be skipped")

	build, _ := nodeByID(out, "function:a.buzz:build")
	assert.Equal(t, "true", build.Attrs["exported"])

	assert.True(t, hasEdge(out, "file:a.buzz", "function:a.buzz:build", types.RelationContains))
	assert.True(t, hasEdge(out, "function:a.buzz:build", "function:a.buzz:helper", types.RelationCalls))

	// import "b" resolves to the scanned b.buzz (extracted); magus/spell/go does not
	// (an inferred edge to the literal import node).
	e, ok := findEdge(out, "file:a.buzz", "file:b.buzz", types.RelationImports)
	require.True(t, ok, "resolved import edge")
	assert.Equal(t, types.ConfidenceExtracted, e.Confidence)
	e, ok = findEdge(out, "file:a.buzz", "import:magus/spell/go", types.RelationImports)
	require.True(t, ok, "unresolved import edge")
	assert.Equal(t, types.ConfidenceInferred, e.Confidence)

	// The in-body NOTE binds rationale_for to the function it documents.
	rats := 0
	for _, n := range out.Nodes {
		if n.Kind == types.KindRationale {
			rats++
			assert.Equal(t, "NOTE", n.Label)
			assert.Contains(t, n.Doc, "tricky")
		}
	}
	assert.Equal(t, 1, rats)
	found := false
	for _, edge := range out.Links {
		if edge.Relation == types.RelationRationaleFor && edge.Target == "function:a.buzz:build" {
			found = true
		}
	}
	assert.True(t, found, "rationale_for edge to build")
}

func TestAssembleDocs(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/codes/sandbox/MGS2010.md", "# MGS2010\nRelated to MGS2001. See [go spell](../../spells/go.md).\n")
	writeFile(t, root, "docs/spells/go.md", "# go\nThe go spell.\n")
	writeFile(t, root, "README.md", "Uses the `go` spell; see MGS2010 when it fails.\n")

	out := mergeAll([]Shard{assembleDocs(root, types.SpellsOutput{Spells: []types.SpellEntry{{Name: "go"}}})}).Output()

	for _, id := range []string{"doc:docs/codes/sandbox/MGS2010.md", "doc:docs/spells/go.md", "doc:README.md"} {
		_, ok := nodeByID(out, id)
		assert.Truef(t, ok, "missing doc node %q", id)
	}

	// Path-convention edges are extracted.
	e, ok := findEdge(out, "doc:docs/codes/sandbox/MGS2010.md", "diagnostic:MGS2010", types.RelationDocuments)
	require.True(t, ok, "path documents edge to MGS2010")
	assert.Equal(t, types.ConfidenceExtracted, e.Confidence)
	e, ok = findEdge(out, "doc:docs/spells/go.md", "spell:go", types.RelationDocuments)
	require.True(t, ok, "path documents edge to spell go")
	assert.Equal(t, types.ConfidenceExtracted, e.Confidence)

	// In-body MGS mention -> inferred; a markdown link -> references.
	e, ok = findEdge(out, "doc:docs/codes/sandbox/MGS2010.md", "diagnostic:MGS2001", types.RelationDocuments)
	require.True(t, ok, "in-body MGS2001 mention")
	assert.Equal(t, types.ConfidenceInferred, e.Confidence)
	assert.True(t, hasEdge(out, "doc:docs/codes/sandbox/MGS2010.md", "doc:docs/spells/go.md", types.RelationReferences), "resolved markdown link")

	// README mentions the go spell in backticks and MGS2010 in prose (both inferred).
	assert.True(t, hasEdge(out, "doc:README.md", "spell:go", types.RelationDocuments))
	assert.True(t, hasEdge(out, "doc:README.md", "diagnostic:MGS2010", types.RelationDocuments))
}
