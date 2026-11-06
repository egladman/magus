package goindex

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/scip-code/scip/bindings/go/scip"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/symbols"
)

type goindexSuite struct {
	suite.Suite
	dir string
}

func TestGoindexSuite(t *testing.T) { suite.Run(t, new(goindexSuite)) }

// otherOS is a GOOS the host is not, whose file the host build excludes.
func otherOS() string {
	if runtime.GOOS == "windows" {
		return "linux"
	}
	return "windows"
}

func (s *goindexSuite) SetupTest() {
	s.dir = s.T().TempDir()
	s.write("go.mod", "module example.com/m\n\ngo 1.22\n")
	s.write("a/a.go", `// Package a reads.
package a

// Reader reads.
type Reader interface {
	Read(p []byte) (int, error)
}

// Limit caps a read.
const Limit = 4

// Old is removed by a test.
func Old() int { return Limit }
`)
	s.write(fmt.Sprintf("a/a_%s.go", otherOS()), "package a\n\nfunc OnlyElsewhere() {}\n")
	s.write("b/b.go", `package b

import "example.com/m/a"

type File struct {
	Name string
}

func (f *File) Read(p []byte) (int, error) { return a.Old(), nil }

var _ a.Reader = (*File)(nil)
`)
	s.write("b/b_test.go", "package b\n\nimport \"testing\"\n\nfunc TestFile(t *testing.T) { _ = File{Name: \"x\"} }\n")
	s.write("c/c.go", "package c\n\nfunc C() {}\n")
}

func (s *goindexSuite) write(rel, content string) {
	path := filepath.Join(s.dir, filepath.FromSlash(rel))
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o755))
	s.Require().NoError(os.WriteFile(path, []byte(content), 0o644))
}

func (s *goindexSuite) indexer() *Indexer {
	return &Indexer{Dir: s.dir, Builds: []map[string]string{{"GOOS": otherOS()}}}
}

func (s *goindexSuite) wasmIndexer() *Indexer {
	return &Indexer{Dir: s.dir, Builds: []map[string]string{{"GOOS": "js", "GOARCH": "wasm"}}}
}

func (s *goindexSuite) update(ix *Indexer) (symbols.UpdateResult, string) {
	pack := filepath.Join(s.T().TempDir(), "scip.pack")
	res, err := symbols.UpdatePack(s.T().Context(), pack, ix)
	s.Require().NoError(err)
	return res, pack
}

func (s *goindexSuite) index(pack string) *symbols.Index {
	idx, err := symbols.ReadIndex(pack, true)
	s.Require().NoError(err)
	return idx
}

func occurrences(items []string, want string) int {
	n := 0
	for _, item := range items {
		if item == want {
			n++
		}
	}
	return n
}

func sym(pkg, desc string) string {
	return fmt.Sprintf("magus gomod example.com/m . `example.com/m/%s`/%s", pkg, desc)
}

func (s *goindexSuite) parsed(pack string) map[string]bool {
	keys := map[string]bool{}
	for _, ks := range symbols.ParseDecoded(s.T().Context(), s.index(pack), ".", "go") {
		keys[ks.Key] = true
	}
	return keys
}

func (s *goindexSuite) TestUnitsAreOnePerPackage() {
	units, err := s.indexer().Units(s.T().Context())
	s.Require().NoError(err)
	var ids []string
	for _, u := range units {
		ids = append(ids, u.ID)
	}
	s.Equal([]string{"example.com/m/a", "example.com/m/b", "example.com/m/c"}, ids)
	s.Equal([]string{"example.com/m/a", "testing"}, units[1].Deps, "b's test variant imports testing")
}

// Keys match the ones scip-go named the same declarations by, so graph ids hold.
func (s *goindexSuite) TestKeysMatchScipGoDescriptors() {
	_, pack := s.update(s.indexer())
	keys := s.parsed(pack)
	for _, key := range []string{
		"gomod example.com/m `example.com/m/a`/",
		"gomod example.com/m `example.com/m/a`/Reader#",
		"gomod example.com/m `example.com/m/a`/Reader#Read.",
		"gomod example.com/m `example.com/m/a`/Limit.",
		"gomod example.com/m `example.com/m/b`/File#Read().",
		"gomod example.com/m `example.com/m/b`/File#Name.",
		"gomod example.com/m `example.com/m/b`/TestFile().",
	} {
		s.True(keys[key], key)
	}
}

func (s *goindexSuite) TestSignaturesRenderAsScipGoDid() {
	_, pack := s.update(s.indexer())
	byKey := map[string]string{}
	for _, ks := range symbols.ParseDecoded(s.T().Context(), s.index(pack), ".", "go") {
		byKey[ks.Key] = ks.Signature
	}
	s.Equal("func (*File).Read(p []byte) (int, error)", byKey["gomod example.com/m `example.com/m/b`/File#Read()."])
	s.Equal("const Limit untyped int = 4", byKey["gomod example.com/m `example.com/m/a`/Limit."])
	s.Equal("struct field Name string", byKey["gomod example.com/m `example.com/m/b`/File#Name."])
	s.Equal("type File struct{ Name string }", byKey["gomod example.com/m `example.com/m/b`/File#"])
}

// Another package's type is spelled by its package name, not its import path: the
// graph's readers match a parameter's type against interface names that way.
func (s *goindexSuite) TestSignaturesNameAnotherPackageByItsName() {
	s.write("b/wrap.go", "package b\n\nimport \"example.com/m/a\"\n\nfunc Wrap(r a.Reader) a.Reader { return r }\n")
	_, pack := s.update(s.indexer())
	byKey := map[string]string{}
	for _, ks := range symbols.ParseDecoded(s.T().Context(), s.index(pack), ".", "go") {
		byKey[ks.Key] = ks.Signature
	}
	s.Equal("func Wrap(r a.Reader) a.Reader", byKey["gomod example.com/m `example.com/m/b`/Wrap()."])
}

// An interface names what it declares, an embedded interface by name rather than its
// methods, a struct's fields align as gofmt aligns them, and a field of an anonymous
// struct type spells that struct's tags in backquotes.
func (s *goindexSuite) TestTypeSignaturesKeepTheDeclaredShape() {
	s.write("b/shape.go", "package b\n\nimport \"example.com/m/a\"\n\ntype ReadCloser interface {\n\ta.Reader\n\tClose() error\n}\n\ntype Pair struct {\n\tK string `json:\"k\"`\n\tValue int\n\tIn struct {\n\t\tN int `json:\"n\"`\n\t}\n}\n")
	_, pack := s.update(s.indexer())
	byKey := map[string]string{}
	for _, ks := range symbols.ParseDecoded(s.T().Context(), s.index(pack), ".", "go") {
		byKey[ks.Key] = ks.Signature
	}
	s.Equal("type ReadCloser interface {\n    Close() error\n    a.Reader\n}", byKey["gomod example.com/m `example.com/m/b`/ReadCloser#"])
	s.Equal("type Pair struct {\n    K     string \"json:\\\"k\\\"\"\n    Value int\n    In    struct {\n        N int \"json:\\\"n\\\"\"\n    }\n}", byKey["gomod example.com/m `example.com/m/b`/Pair#"])
	s.Equal("struct field In struct{N int `json:\"n\"`}", byKey["gomod example.com/m `example.com/m/b`/Pair#In."])
}

func (s *goindexSuite) TestImplementationsCrossPackages() {
	_, pack := s.update(s.indexer())
	for _, ks := range symbols.ParseDecoded(s.T().Context(), s.index(pack), ".", "go") {
		if ks.Key == "gomod example.com/m `example.com/m/b`/File#" {
			s.Equal([]string{"gomod example.com/m `example.com/m/a`/Reader#"}, ks.Implements)
			return
		}
	}
	s.Fail("File is not in the index")
}

func (s *goindexSuite) TestReferencesAndCallsResolveAcrossPackages() {
	_, pack := s.update(s.indexer())
	for _, ks := range symbols.ParseDecoded(s.T().Context(), s.index(pack), ".", "go") {
		if ks.Key != "gomod example.com/m `example.com/m/a`/Old()." {
			continue
		}
		s.Equal("a/a.go:13", ks.Source)
		s.Equal(13, ks.DefEndLine)
		s.Require().Len(ks.Refs, 1)
		s.Equal("b/b.go", ks.Refs[0].Path)
		return
	}
	s.Fail("Old is not in the index")
}

func (s *goindexSuite) TestPackageDocAndImportsRecorded() {
	idx := s.index(s.builtPack())
	var importRef bool
	for _, d := range idx.Documents {
		if d.RelativePath != "b/b.go" {
			continue
		}
		for _, o := range d.Occurrences {
			if o.Symbol == sym("a", "") && o.SymbolRoles == 0 {
				r, _ := o.SourceRange()
				importRef = importRef || r.Start.Line == 2
			}
		}
	}
	s.True(importRef, "the import path references package a")
}

// Each file's package clause defines its package, test files included: the graph places a
// file in the package it defines, and only one defining file would leave the rest unplaced.
func (s *goindexSuite) TestEveryPackageClauseDefinesItsPackage() {
	idx := s.index(s.builtPack())
	defined := map[string]bool{}
	for _, d := range idx.Documents {
		for _, o := range d.Occurrences {
			if o.Symbol == sym("b", "") && o.SymbolRoles&int32(scip.SymbolRole_Definition) != 0 {
				defined[d.RelativePath] = true
			}
		}
	}
	s.Equal(map[string]bool{"b/b.go": true, "b/b_test.go": true}, defined)
}

func (s *goindexSuite) TestAnonymousStructVarFieldsKeyAsScipGoDid() {
	s.write("c/once.go", "package c\n\nvar once struct {\n\tn int\n}\n\nfunc N() int { return once.n }\n")
	s.True(s.parsed(s.builtPack())["gomod example.com/m `example.com/m/c`/once:n."])
}

func (s *goindexSuite) builtPack() string {
	_, pack := s.update(s.indexer())
	return pack
}

// A file only another GOOS compiles is indexed, though the host never builds it.
func (s *goindexSuite) TestOtherBuildCoversItsTaggedFile() {
	keys := s.parsed(s.builtPack())
	s.True(keys["gomod example.com/m `example.com/m/a`/OnlyElsewhere()."])
}

// `./...` skips a package the host excludes whole, so only a build can find it.
func (s *goindexSuite) TestOtherBuildFindsAPackageTheHostExcludesWhole() {
	s.write("w/w.go", "//go:build js && wasm\n\npackage w\n\nimport \"example.com/m/c\"\n\nfunc OnlyInWasm() { c.C() }\n")
	_, pack := s.update(s.wasmIndexer())

	keys := s.parsed(pack)
	s.True(keys["gomod example.com/m `example.com/m/w`/OnlyInWasm()."])
	var paths []string
	for _, d := range s.index(pack).Documents {
		paths = append(paths, d.RelativePath)
	}
	s.Contains(paths, "w/w.go")
	s.Equal(1, occurrences(paths, "c/c.go"), "c keeps the host's one document")
}

func (s *goindexSuite) TestAPackageTheHostExcludesWholeStaysOutWithoutABuild() {
	s.write("w/w.go", "//go:build js && wasm\n\npackage w\n\nfunc OnlyInWasm() {}\n")
	_, pack := s.update(&Indexer{Dir: s.dir})
	s.False(s.parsed(pack)["gomod example.com/m `example.com/m/w`/OnlyInWasm()."])
}

func (s *goindexSuite) TestEditToAPackageTheHostExcludesWholeMovesItsUnit() {
	s.write("w/w.go", "//go:build js && wasm\n\npackage w\n\nfunc OnlyInWasm() {}\n")
	ix := s.wasmIndexer()
	digest := func() string {
		units, err := ix.Units(s.T().Context())
		s.Require().NoError(err)
		for _, u := range units {
			if u.ID == "example.com/m/w" {
				return u.Digest
			}
		}
		s.FailNow("no unit for example.com/m/w")
		return ""
	}
	before := digest()
	s.write("w/w.go", "//go:build js && wasm\n\npackage w\n\nfunc OnlyInWasm() { _ = 1 }\n")
	s.NotEqual(before, digest())
}

// A pack written without a build holds shards that lack that build's files.
func (s *goindexSuite) TestAddingABuildMovesTheVersion() {
	s.NotEqual((&Indexer{Dir: s.dir}).Version(), s.wasmIndexer().Version())
}

func (s *goindexSuite) TestHostOnlyIndexMissesTheTaggedFile() {
	_, pack := s.update(&Indexer{Dir: s.dir})
	s.False(s.parsed(pack)["gomod example.com/m `example.com/m/a`/OnlyElsewhere()."])
}

// A body edit re-indexes its own package and nothing else.
func (s *goindexSuite) TestBodyEditReindexesOnePackage() {
	pack := filepath.Join(s.T().TempDir(), "scip.pack")
	_, err := symbols.UpdatePack(s.T().Context(), pack, s.indexer())
	s.Require().NoError(err)

	s.write("c/c.go", "package c\n\nfunc C() { _ = 1 }\n")
	res, err := symbols.UpdatePack(s.T().Context(), pack, s.indexer())
	s.Require().NoError(err)
	s.Equal(symbols.UpdateResult{Indexed: []string{"example.com/m/c"}, Kept: 2}, res)
}

// Removing a symbol another package used re-indexes that package too.
func (s *goindexSuite) TestRemovedSymbolReindexesItsUsers() {
	pack := filepath.Join(s.T().TempDir(), "scip.pack")
	_, err := symbols.UpdatePack(s.T().Context(), pack, s.indexer())
	s.Require().NoError(err)

	content, err := os.ReadFile(filepath.Join(s.dir, "a/a.go"))
	s.Require().NoError(err)
	s.write("a/a.go", strings.Replace(string(content), "func Old() int", "func New() int", 1))
	res, err := symbols.UpdatePack(s.T().Context(), pack, s.indexer())
	s.Require().NoError(err)
	s.Equal(symbols.UpdateResult{Indexed: []string{"example.com/m/a"}, Dependents: []string{"example.com/m/b"}, Kept: 1}, res,
		"c, which never used Old, is kept rather than re-indexed")
}

func (s *goindexSuite) TestEditToAnExcludedFileMovesItsUnit() {
	ix := s.indexer()
	before, err := ix.Units(s.T().Context())
	s.Require().NoError(err)
	s.write(fmt.Sprintf("a/a_%s.go", otherOS()), "package a\n\nfunc OnlyElsewhere() { _ = 2 }\n")
	after, err := ix.Units(s.T().Context())
	s.Require().NoError(err)
	s.NotEqual(before[0].Digest, after[0].Digest)
	s.Equal(before[1].Digest, after[1].Digest)
}

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

// removePack deletes pack so the next update rebuilds it whole; one already gone is fine.
func removePack(b *testing.B, pack string) {
	b.Helper()
	if err := os.Remove(pack); err != nil && !errors.Is(err, fs.ErrNotExist) {
		b.Fatal(err)
	}
}

// BenchmarkFullIndex is the whole-tree index every edit paid before package grain.
func BenchmarkFullIndex(b *testing.B) {
	dir := b.TempDir()
	writeBenchModule(b, dir)
	pack := filepath.Join(b.TempDir(), "scip.pack")
	for b.Loop() {
		removePack(b, pack)
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
		removePack(b, pack)
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
