package knowledge

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

	b, _ := nodeByID(out, "file:b.buzz")
	assert.Equal(t, map[string]string{"language": "buzz", AttrLines: "1", AttrBytes: "29"}, b.Attrs,
		"a buzz file node is sized from the read that parsed it")

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
import "magus/spell";
import "spells/missing";
export fun f(ctx: magus\Context, args: [str]) > void {}
`)
	out := mergeAll([]Shard{assembleBuzz(root)}).Output()

	// Compiled-in modules (buzz stdlib, magus/*) are expected to be unresolvable
	// and are NOT flagged.
	for _, id := range []string{"import:fs", "import:buzz:os", "import:magus/spell"} {
		n, ok := nodeByID(out, id)
		require.Truef(t, ok, "missing import node %q", id)
		assert.Emptyf(t, n.Attrs[attrDiagnostic], "%q should not be flagged", id)
	}
	// A dangling workspace-relative import is flagged MGS7001.
	miss, ok := nodeByID(out, "import:spells/missing")
	require.True(t, ok)
	assert.Equal(t, string(types.UnresolvableBuzzImport), miss.Attrs[attrDiagnostic])
}

// TestSourceWalksSkipVCSIgnored: the file and function rows of a committed MAGUS.md rank
// what these walks find, so an ignored build output must not reach them. Measured
// 2026-09-23: one .buzz file under the ignored dist/ put itself second in the file row
// and its hub function first in the function row.
func TestSourceWalksSkipVCSIgnored(t *testing.T) {
	root := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = root
	require.NoError(t, cmd.Run())
	writeFile(t, root, ".gitignore", "/dist/\n")
	writeFile(t, root, "kept.buzz", "fun kept() > void {}\n")
	writeFile(t, root, "kept.go", "package kept\n")
	writeFile(t, root, "dist/built.buzz", "fun built() > void {}\n")
	writeFile(t, root, "dist/built.go", "package built\n")

	assert.Equal(t, []string{"kept.buzz"}, findBuzzFiles(root))
	assert.Equal(t, []string{"kept.buzz", "kept.go"}, findCommentSources(root))
}

// writeBenchTree materializes a synthetic workspace of nBuzz .buzz files (each a
// few functions, two imports, a rationale comment, intra-file calls) and nDocs
// markdown docs (each with an MGS code, a backtick spell mention, and a link) so
// the filesystem extractors run against a realistic tree.
func writeBenchTree(b *testing.B, nBuzz, nDocs int) string {
	b.Helper()
	root := b.TempDir()
	for i := 0; i < nBuzz; i++ {
		dir := filepath.Join(root, fmt.Sprintf("pkg%04d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			b.Fatal(err)
		}
		next := (i + 1) % max(nBuzz, 1)
		src := fmt.Sprintf(`import "pkg%04d/mod";
import "magus/spell/go";
// package-level doc
export fun build(ctx: magus\Context, args: [str]) > void {
    // NOTE: build note %d explains the tricky bit
    helper();
}
fun helper() > void { thing(); }
fun thing() > void {}
`, next, i)
		if err := os.WriteFile(filepath.Join(dir, "mod.buzz"), []byte(src), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	if nDocs > 0 {
		dir := filepath.Join(root, "docs", "codes", "sandbox")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			b.Fatal(err)
		}
		for i := 0; i < nDocs; i++ {
			md := fmt.Sprintf("# Doc %d\n\nSee MGS%04d and the `go` spell; also `spell%02d`. [next](page%04d.md).\n",
				i, 2000+(i%10), i%20, (i+1)%max(nDocs, 1))
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("page%04d.md", i)), []byte(md), 0o644); err != nil {
				b.Fatal(err)
			}
		}
	}
	return root
}

func BenchmarkAssembleBuzz(b *testing.B) {
	root := writeBenchTree(b, 200, 0)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = assembleBuzz(root)
	}
}
