package knowledge

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/types"
)

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

func benchSpells() types.SpellsOutput {
	out := types.SpellsOutput{Spells: make([]types.SpellEntry, 20)}
	for i := range out.Spells {
		out.Spells[i] = types.SpellEntry{Name: fmt.Sprintf("spell%02d", i)}
	}
	out.Spells[0].Name = "go"
	return out
}

func BenchmarkAssembleBuzz(b *testing.B) {
	root := writeBenchTree(b, 200, 0)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = assembleBuzz(root)
	}
}

func BenchmarkAssembleDocs(b *testing.B) {
	root := writeBenchTree(b, 0, 100)
	spells := benchSpells()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = assembleDocs(root, spells, nil)
	}
}
