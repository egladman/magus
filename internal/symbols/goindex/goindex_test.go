package goindex

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
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
	s.write("a/a_"+otherOS()+".go", "package a\n\nfunc OnlyElsewhere() {}\n")
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

func (s *goindexSuite) update(ix *Indexer) (symbols.UpdateResult, string) {
	pack := filepath.Join(s.T().TempDir(), "scip.pack")
	res, err := symbols.UpdatePack(s.T().Context(), pack, ix)
	s.Require().NoError(err)
	return res, pack
}

func (s *goindexSuite) index(pack string) *scip.Index {
	idx, err := symbols.ReadIndex(pack, true)
	s.Require().NoError(err)
	return idx
}

func sym(pkg, desc string) string {
	return "magus gomod example.com/m . `example.com/m/" + pkg + "`/" + desc
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
	s.Equal("type File struct {\n    Name string\n}", byKey["gomod example.com/m `example.com/m/b`/File#"])
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
	idx := s.index(s.packOf())
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
	idx := s.index(s.packOf())
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
	s.True(s.parsed(s.packOf())["gomod example.com/m `example.com/m/c`/once:n."])
}

func (s *goindexSuite) packOf() string {
	_, pack := s.update(s.indexer())
	return pack
}

// A file only another GOOS compiles is indexed, though the host never builds it.
func (s *goindexSuite) TestOtherBuildCoversItsTaggedFile() {
	keys := s.parsed(s.packOf())
	s.True(keys["gomod example.com/m `example.com/m/a`/OnlyElsewhere()."])
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
	s.Equal([]string{"example.com/m/c"}, res.Indexed)
	s.Empty(res.Dependents)
	s.Equal(2, res.Kept)
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
	s.Equal([]string{"example.com/m/a"}, res.Indexed)
	s.Equal([]string{"example.com/m/b"}, res.Dependents)
	s.False(slices.Contains(res.Dependents, "example.com/m/c"))
}

func (s *goindexSuite) TestEditToAnExcludedFileMovesItsUnit() {
	ix := s.indexer()
	before, err := ix.Units(s.T().Context())
	s.Require().NoError(err)
	s.write("a/a_"+otherOS()+".go", "package a\n\nfunc OnlyElsewhere() { _ = 2 }\n")
	after, err := ix.Units(s.T().Context())
	s.Require().NoError(err)
	s.NotEqual(before[0].Digest, after[0].Digest)
	s.Equal(before[1].Digest, after[1].Digest)
}
