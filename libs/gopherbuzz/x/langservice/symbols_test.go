package langservice

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type symbolsSuite struct{ suite.Suite }

func TestSymbolsSuite(t *testing.T) { suite.Run(t, new(symbolsSuite)) }

func (s *symbolsSuite) TestScanSymbols() {
	src := "export fun build(args: [str]) > void {\n    final local = 1;\n}\n" +
		"final a = 1;\nvar b = 2;\nobject Point {}\nenum Color { red }\nfun half(\n"
	s.Equal([]symbol{
		{Name: "build", Kind: symFunction, Sig: "fun build(args: [str])"},
		{Name: "local", Kind: symConstant},
		{Name: "a", Kind: symConstant},
		{Name: "b", Kind: symConstant},
		{Name: "Point", Kind: symType},
		{Name: "Color", Kind: symType},
		{Name: "half", Kind: symFunction, Sig: "fun half("},
	}, scanSymbols(src))
}

func (s *symbolsSuite) TestScanImports() {
	src := "import \"fs\";\nimport \"magus/vcs\" as v;\nimport \"std\" as _;\nimport \"buzz:std\";\nimport \"unterminated\n"
	s.Equal([]importBinding{
		{Path: "fs", Name: "fs"},
		{Path: "magus/vcs", Name: "v"},
		{Path: "buzz:std", Name: "std"},
	}, scanImports(src))
}

func (s *symbolsSuite) TestModuleBase() {
	s.Equal("std", moduleBase("buzz:std"))
	s.Equal("vcs", moduleBase("magus/vcs"))
	s.Equal("b:c", moduleBase("a/b:c"))
}

func (s *symbolsSuite) TestStripScheme() {
	s.Equal("std", stripScheme("buzz:std"))
	s.Equal("magus/vcs", stripScheme("magus/vcs"))
	s.Equal("a/b:c", stripScheme("a/b:c"))
}

func (s *symbolsSuite) TestMemberContext() {
	base, partial, ok := memberContext(`x = fs\gl`)
	s.True(ok)
	s.Equal("fs", base)
	s.Equal("gl", partial)

	base, partial, ok = memberContext(`fs\`)
	s.True(ok)
	s.Equal("fs", base)
	s.Empty(partial)

	_, _, ok = memberContext(`a\b\c`)
	s.False(ok)
	_, _, ok = memberContext(`a.b\c`)
	s.False(ok)
	_, _, ok = memberContext(`\c`)
	s.False(ok)
}

func (s *symbolsSuite) TestImportPathContext() {
	partial, ok := importPathContext("import \"ma")
	s.True(ok)
	s.Equal("ma", partial)

	_, ok = importPathContext(`import "fs";`)
	s.False(ok)
	_, ok = importPathContext(`final x = "ma`)
	s.False(ok)
}

func (s *symbolsSuite) TestIdentSpan() {
	start, end := identSpan("a fooBar b", 4)
	s.Equal([2]int{2, 8}, [2]int{start, end})
	start, end = identSpan("a + b", 2)
	s.Equal(start, end)
}
