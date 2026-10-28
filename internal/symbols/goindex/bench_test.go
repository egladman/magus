package goindex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/symbols"
)

const (
	benchPackages = 60
	benchFiles    = 3
	benchFuncs    = 20
)

// writeBenchModule generates a module of benchPackages packages, each importing the one
// before it, so an edit low in the chain has every other package as a dependent.
func writeBenchModule(b *testing.B, dir string) {
	b.Helper()
	write := func(rel, content string) {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	write("go.mod", "module example.com/bench\n\ngo 1.22\n")
	for p := range benchPackages {
		for f := range benchFiles {
			var src strings.Builder
			fmt.Fprintf(&src, "package p%03d\n\n", p)
			if p > 0 && f == 0 {
				fmt.Fprintf(&src, "import \"example.com/bench/p%03d\"\n\nvar _ = p%03d.F0_0\n\n", p-1, p-1)
			}
			fmt.Fprintf(&src, "type T%d struct{ N int }\n\nfunc (t *T%d) Get() int { return t.N }\n\n", f, f)
			for fn := range benchFuncs {
				fmt.Fprintf(&src, "// F%d_%d is generated.\nfunc F%d_%d(x int) int {\n\treturn x + %d\n}\n\n", f, fn, f, fn, fn)
			}
			write(fmt.Sprintf("p%03d/f%d.go", p, f), src.String())
		}
	}
}

func benchIndexer(dir string) *Indexer { return &Indexer{Dir: dir} }

// BenchmarkFullIndex is the whole-tree index every edit paid before package grain.
func BenchmarkFullIndex(b *testing.B) {
	dir := b.TempDir()
	writeBenchModule(b, dir)
	pack := filepath.Join(b.TempDir(), "scip.pack")
	for b.Loop() {
		os.Remove(pack)
		if _, err := symbols.UpdatePack(b.Context(), pack, benchIndexer(dir)); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkOneLineEdit is a body edit in the last package of the chain: one unit
// re-indexed, every other shard copied.
func BenchmarkOneLineEdit(b *testing.B) {
	benchEdit(b, fmt.Sprintf("p%03d/f1.go", benchPackages-1), "return x + 3", "return x + 4")
}

// BenchmarkOneLineEditAtTheRoot is a body edit in the package every other one imports:
// the defined set holds, so still one unit.
func BenchmarkOneLineEditAtTheRoot(b *testing.B) {
	benchEdit(b, "p000/f1.go", "return x + 3", "return x + 4")
}

// dirtyUnit is an Indexer whose one unit reads as edited on every Units call, so a
// benchmark measures an edit to a real tree without writing to it.
type dirtyUnit struct {
	*Indexer
	unit string
	n    int
}

func (d *dirtyUnit) Units(ctx context.Context) ([]symbols.Unit, error) {
	units, err := d.Indexer.Units(ctx)
	for i := range units {
		if units[i].ID == d.unit {
			units[i].Digest += strconv.Itoa(d.n)
		}
	}
	d.n++
	return units, err
}

// workspaceRoot is this repository's root, three levels above the package directory go
// test runs in.
func workspaceRoot(b *testing.B) string {
	wd, err := os.Getwd()
	if err != nil {
		b.Fatal(err)
	}
	return filepath.Join(wd, "..", "..", "..")
}

// benchWorkspace indexes this repository's root module once, then measures one edited
// package per iteration.
func benchWorkspace(b *testing.B, unit string) {
	root := workspaceRoot(b)
	pack := filepath.Join(b.TempDir(), "scip.pack")
	ix := &dirtyUnit{Indexer: &Indexer{Dir: root}, unit: unit}
	if _, err := symbols.UpdatePack(b.Context(), pack, ix); err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		res, err := symbols.UpdatePack(b.Context(), pack, ix)
		if err != nil {
			b.Fatal(err)
		}
		if len(res.Indexed) != 1 {
			b.Fatalf("indexed %v, want %s alone: %+v", res.Indexed, unit, res)
		}
	}
}

// BenchmarkWorkspaceFullIndex is a cold index of this repository's root module, every
// package type-checked.
func BenchmarkWorkspaceFullIndex(b *testing.B) {
	root := workspaceRoot(b)
	pack := filepath.Join(b.TempDir(), "scip.pack")
	for b.Loop() {
		os.Remove(pack)
		if _, err := symbols.UpdatePack(b.Context(), pack, &Indexer{Dir: root}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkWorkspaceEditLeaf is an edit to cmd/magus, which nothing imports.
func BenchmarkWorkspaceEditLeaf(b *testing.B) {
	benchWorkspace(b, "github.com/egladman/magus/cmd/magus")
}

// BenchmarkWorkspaceEditTypes is a body edit to types, which most packages import.
func BenchmarkWorkspaceEditTypes(b *testing.B) {
	benchWorkspace(b, "github.com/egladman/magus/types")
}

func benchEdit(b *testing.B, rel, from, to string) {
	dir := b.TempDir()
	writeBenchModule(b, dir)
	pack := filepath.Join(b.TempDir(), "scip.pack")
	if _, err := symbols.UpdatePack(b.Context(), pack, benchIndexer(dir)); err != nil {
		b.Fatal(err)
	}
	path := filepath.Join(dir, rel)
	orig, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	edited := []byte(strings.Replace(string(orig), from, to, 1))
	versions := [][]byte{edited, orig}
	i := 0
	for b.Loop() {
		if err := os.WriteFile(path, versions[i%2], 0o644); err != nil {
			b.Fatal(err)
		}
		i++
		res, err := symbols.UpdatePack(b.Context(), pack, benchIndexer(dir))
		if err != nil {
			b.Fatal(err)
		}
		if len(res.Indexed) != 1 {
			b.Fatalf("indexed %v, want one unit", res.Indexed)
		}
	}
}
