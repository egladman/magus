package dedup

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

type dedupSuite struct{ suite.Suite }

func TestDedupSuite(t *testing.T) { suite.Run(t, new(dedupSuite)) }

func goFile(n int, tweak string) string {
	var b strings.Builder
	b.WriteString("package x\n\n")
	for i := range n {
		fmt.Fprintf(&b, "func f%d(a, b int) int {\n\tif a > b {\n\t\treturn a + %d\n\t}\n\treturn b - a\n}\n\n", i, i)
	}
	b.WriteString(tweak)
	return b.String()
}

func (s *dedupSuite) TestNormalizeIgnoresLineEndingsAndTrailingBlanks() {
	s.Equal(ExactKey("a := 1\nb := 2\n"), ExactKey("a := 1  \r\nb := 2\t\r\n\r\n"))
	s.NotEqual(ExactKey("a := 1"), ExactKey("a := 2"))
}

func (s *dedupSuite) TestShinglesNeedFiveTokens() {
	s.Empty(Shingles([]string{"a", "b", "c", "d"}))
	s.Len(Shingles([]string{"a", "b", "c", "d", "e", "f"}), 2)
}

func (s *dedupSuite) TestSignatureIsFixed() {
	sh := Shingles([]string{"a", "b", "c", "d", "e", "f", "g"})
	s.Equal(Signature(sh), Signature(sh))
	s.NotEqual(Signature(sh), Signature(Shingles([]string{"q", "r", "s", "t", "u", "v", "w"})))
	s.Equal(Bands*Rows, Perms)
}
