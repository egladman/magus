package dedup

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type minhashSuite struct{ suite.Suite }

func TestMinhashSuite(t *testing.T) { suite.Run(t, new(minhashSuite)) }

func (s *minhashSuite) TestNormalizeIgnoresLineEndingsAndTrailingBlanks() {
	s.Equal(ExactKey("a := 1\nb := 2\n"), ExactKey("a := 1  \r\nb := 2\t\r\n\r\n"))
	s.NotEqual(ExactKey("a := 1"), ExactKey("a := 2"))
}

func (s *minhashSuite) TestShinglesNeedFiveTokens() {
	s.Empty(Shingles([]string{"a", "b", "c", "d"}))
	s.Len(Shingles([]string{"a", "b", "c", "d", "e", "f"}), 2)
}

func (s *minhashSuite) TestSignatureIsFixed() {
	sh := Shingles([]string{"a", "b", "c", "d", "e", "f", "g"})
	s.Equal(Signature(sh), Signature(sh))
	s.NotEqual(Signature(sh), Signature(Shingles([]string{"q", "r", "s", "t", "u", "v", "w"})))
	s.Equal(Bands*Rows, Perms)
}
