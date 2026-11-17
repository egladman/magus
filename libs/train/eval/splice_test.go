package eval

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type SpliceSuite struct{ suite.Suite }

func TestSplice(t *testing.T) { suite.Run(t, new(SpliceSuite)) }

func (s *SpliceSuite) TestSpliceReplacesTheRange() {
	out, err := Splice("abcdef", 2, 4, "XY!")
	s.Require().NoError(err)
	s.Equal("abXY!ef", out)
	_, err = Splice("abc", 2, 9, "")
	s.ErrorContains(err, "outside")
}

func (s *SpliceSuite) TestSpliceNameStaysBesideTheOriginal() {
	s.Equal("hack/dev/x.sab12.buzz", SpliceName("hack/dev/x.buzz", "ab12"))
}

const goFile = `package p

import "strings"

func Up(s string) string {
	return strings.ToUpper(s)
}

func Use() int {
	return helper() + len(Up("x"))
}
`

func (s *SpliceSuite) TestGoCheckerPassesTheOriginalAndItsOwnUnresolvedNames() {
	g := NewGoChecker()
	orig, parsed := g.Errors("p.go", goFile)
	s.True(parsed)
	s.Equal([]string{"undefined: helper"}, orig)
	s.True(g.CheckSplice("p.go", goFile, orig))
}

func (s *SpliceSuite) TestGoCheckerRefusesANewTypeErrorAndASyntaxError() {
	g := NewGoChecker()
	orig, _ := g.Errors("p.go", goFile)
	bad, err := Splice(goFile, len("package p\n\nimport \"strings\"\n\nfunc Up(s string) string {\n\treturn "), len("package p\n\nimport \"strings\"\n\nfunc Up(s string) string {\n\treturn strings.ToUpper(s)"), "strings.ToUpper(s, 1)")
	s.Require().NoError(err)
	s.False(g.CheckSplice("p.go", bad, orig))
	broken, _ := Splice(goFile, 40, 60, "func (")
	s.False(g.CheckSplice("p.go", broken, orig))
}
