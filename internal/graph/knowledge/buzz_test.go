package knowledge

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssembleBuzz(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.buzz", `import "b";
import "magus/spell/go";
export fun build(ctx: magus\Context, args: [str]) > void {
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

func TestBuzzUnresolvableImportTaggedMGS7001(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.buzz", `import "fs";
import "buzz:os";
import "magus/target";
import "spells/missing";
export fun f(ctx: magus\Context, args: [str]) > void {}
`)
	out := mergeAll([]Shard{assembleBuzz(root)}).Output()

	// Compiled-in modules (buzz stdlib, magus/*) are expected to be unresolvable
	// and are NOT flagged.
	for _, id := range []string{"import:fs", "import:buzz:os", "import:magus/target"} {
		n, ok := nodeByID(out, id)
		require.Truef(t, ok, "missing import node %q", id)
		assert.Emptyf(t, n.Attrs[AttrDiagnostic], "%q should not be flagged", id)
	}
	// A dangling workspace-relative import is flagged MGS7001.
	miss, ok := nodeByID(out, "import:spells/missing")
	require.True(t, ok)
	assert.Equal(t, string(types.UnresolvableBuzzImport), miss.Attrs[AttrDiagnostic])
}
