package dedup

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

type pairsSuite struct{ suite.Suite }

func TestPairsSuite(t *testing.T) { suite.Run(t, new(pairsSuite)) }

func goFile(n int, tweak string) string {
	var b strings.Builder
	b.WriteString("package x\n\n")
	for i := range n {
		fmt.Fprintf(&b, "func f%d(a, b int) int {\n\tif a > b {\n\t\treturn a + %d\n\t}\n\treturn b - a\n}\n\n", i, i)
	}
	b.WriteString(tweak)
	return b.String()
}

func (s *pairsSuite) TestFindExactAndNearAcrossLanguages() {
	base := goFile(40, "")
	near := goFile(40, "func extra() int { return 1 }\n")
	far := goFile(40, "")
	far = strings.ReplaceAll(far, "return", "yield")
	docs := []Doc{
		{"b/copy.go", Go, base + "\n"},
		{"a/base.go", Go, base},
		{"c/near.go", Go, near},
		{"d/far.go", Go, far},
		{"e/other.ts", TypeScript, base},
	}
	pairs, rep := Find(docs)
	got := map[string]string{}
	for _, p := range pairs {
		got[p.A+"|"+p.B] = p.Kind
	}
	s.Equal("exact", got["a/base.go|b/copy.go"])
	s.Equal("near", got["a/base.go|c/near.go"])
	s.Len(pairs, 2, "the far file and the other language pair with nothing")
	s.Equal(1, rep.Exact[Go])
	s.Equal(1, rep.Near[Go])
	s.Equal(4, rep.Docs[Go])
}

func (s *pairsSuite) TestFindIsOrderIndependent() {
	docs := []Doc{
		{"z.go", Go, goFile(30, "")},
		{"y.go", Go, goFile(30, "var q = 1\n")},
		{"x.go", Go, goFile(30, "")},
	}
	first, _ := Find(docs)
	rev := []Doc{docs[2], docs[1], docs[0]}
	second, _ := Find(rev)
	s.Equal(first, second)
}

func (s *pairsSuite) TestShortAndUnlexedFilesPairOnlyByExactKey() {
	docs := []Doc{
		{"a.buzz", Buzz, "x"},
		{"b.buzz", Buzz, "y"},
		{"c.buzz", Buzz, "\"unterminated"},
		{"d.buzz", Buzz, "x\n"},
	}
	pairs, rep := Find(docs)
	s.Equal([]Pair{{A: "a.buzz", B: "d.buzz", Kind: "exact", Jaccard: 1}}, pairs)
	s.Equal(1, rep.Unlexed[Buzz])
	s.Equal(2, rep.TooShort[Buzz])
}
