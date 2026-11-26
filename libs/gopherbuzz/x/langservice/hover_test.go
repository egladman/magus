package langservice

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

type hoverSuite struct {
	suite.Suite
	svc *Service
}

func TestHoverSuite(t *testing.T) { suite.Run(t, new(hoverSuite)) }

func (s *hoverSuite) SetupTest() { s.svc = New(fixture()) }

func (s *hoverSuite) TestModuleMethod() {
	src := "import \"fs\";\nfs\\glob(\"*\");"
	h, ok := s.svc.Hover(src, strings.Index(src, "glob")+1)
	s.Require().True(ok)
	s.Equal(Hover{
		Title: `fs\glob(pattern: str) > [str] !> any`,
		Doc:   "Glob returns the paths matching pattern.\n\nOrder is lexical.",
	}, h)
}

func (s *hoverSuite) TestModuleValue() {
	h, ok := s.svc.Hover(`fs\sep`, 4)
	s.Require().True(ok)
	s.Equal(Hover{Title: `fs\sep: str`, Doc: "The path separator."}, h)
}

func (s *hoverSuite) TestModuleName() {
	h, ok := s.svc.Hover(`import "fs";`, 9)
	s.Require().True(ok)
	s.Equal(Hover{Title: "module fs", Doc: "Filesystem access."}, h)
}

func (s *hoverSuite) TestLocalFunction() {
	src := "export fun build(args: [str]) > void {}\nbuild();"
	h, ok := s.svc.Hover(src, strings.LastIndex(src, "build")+1)
	s.Require().True(ok)
	s.Equal(Hover{Title: "fun build(args: [str])"}, h)
}

func (s *hoverSuite) TestLocalConstant() {
	h, ok := s.svc.Hover("final answer = 42;\nanswer", 21)
	s.Require().True(ok)
	s.Equal(Hover{Title: "constant answer"}, h)
}

func (s *hoverSuite) TestBuiltin() {
	h, ok := s.svc.Hover(`zdef("c", "int f();")`, 2)
	s.Require().True(ok)
	s.Equal(Hover{Title: "zdef(...)", Doc: "Buzz builtin"}, h)
}

func (s *hoverSuite) TestNothing() {
	s.noHover("   +  ")
	s.noHover("xyzzy")
	s.noHover("import \"fs\";\nfs.glob")
	s.noHover("final xs = [1];\nxs.len")
	s.noHover(`fs\nope`)
	s.noHover(`nope\glob`)
	s.noHover(`broken\x`)
}

func (s *hoverSuite) noHover(src string) {
	_, ok := s.svc.Hover(src, len(src)-1)
	s.False(ok, src)
}

func (s *hoverSuite) noSignature(src string) {
	_, ok := s.svc.Signature(src, len(src))
	s.False(ok, src)
}

func (s *hoverSuite) TestOffsetClamped() {
	_, ok := s.svc.Hover(`fs\glob`, 999)
	s.True(ok)
	_, ok = s.svc.Hover(`fs\glob`, -3)
	s.True(ok, "offset 0 sits on fs")
}

func (s *hoverSuite) TestSignatureModuleFunction() {
	src := "import \"fs\";\nfs\\glob(\"*\")"
	sig, ok := s.svc.Signature(src, strings.Index(src, "(")+1)
	s.Require().True(ok)
	s.Equal(Signature{
		Label: `fs\glob(pattern: str) > [str] !> any`,
		Doc:   "Glob returns the paths matching pattern.\n\nOrder is lexical.",
	}, sig)
}

func (s *hoverSuite) TestSignatureNestedCall() {
	src := `fs\glob(str(1), `
	sig, ok := s.svc.Signature(src, len(src))
	s.Require().True(ok)
	s.Equal(`fs\glob(pattern: str) > [str] !> any`, sig.Label)
}

func (s *hoverSuite) TestSignatureLocalFunction() {
	src := "export fun build(args: [str]) > void {}\nbuild("
	sig, ok := s.svc.Signature(src, len(src))
	s.Require().True(ok)
	s.Equal(Signature{Label: "fun build(args: [str])"}, sig)
}

func (s *hoverSuite) TestSignatureBuiltin() {
	sig, ok := s.svc.Signature("print(", 6)
	s.Require().True(ok)
	s.Equal(Signature{Label: "print(...)"}, sig)
}

func (s *hoverSuite) TestSignatureNotInCall() {
	s.noSignature("final x = 1;")
	s.noSignature(`fs\glob("*")`)
	s.noSignature("import \"fs\";\nfs.glob(")
	s.noSignature("final xs = [1];\nxs.len(")
	s.noSignature(`fs\sep(`)
	s.noSignature(`broken\x(`)
}

func (s *hoverSuite) TestSignatureClamped() {
	_, ok := s.svc.Signature(`fs\glob(`, 999)
	s.True(ok)
	_, ok = s.svc.Signature(`fs\glob(`, -4)
	s.False(ok)
}
