package fim

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/train/dedup"
)

type fimSuite struct{ suite.Suite }

func TestFIMSuite(t *testing.T) { suite.Run(t, new(fimSuite)) }

const goSrc = `package p

import "fmt"

func Add(a, b int) int {
	sum := a + b
	fmt.Println("sum", sum)
	return sum
}

func Sub(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}
`

const buzzSrc = `import "std";

fun add(a: int, b: int) > int {
    final sum = a + b;
    std\print("sum {sum}");
    return sum;
}

fun sub(a: int, b: int) > int {
    if (a > b) {
        return a - b;
    }
    return b - a;
}

test "add" {
    std\assert(add(1, b: 2) == 3, message: "add");
}
`

const tsSrc = "export function add(a: number, b: number): number {\n  const sum = a + b;\n  console.log('sum', sum);\n  return sum;\n}\n\nexport function sub(a: number, b: number): number {\n  if (a > b) {\n    return a - b;\n  }\n  return b - a;\n}\n"

// manyGo is a valid Go file of n copies of goSrc's functions under distinct names.
func manyGo(n int) string {
	var b strings.Builder
	b.WriteString("package p\n\nimport \"fmt\"\n\n")
	for i := range n {
		fmt.Fprintf(&b, "func Add%d(a, b int) int {\n\tsum := a + b\n\tfmt.Println(\"sum\", sum)\n\treturn sum\n}\n\nfunc Sub%d(a, b int) int {\n\tif a > b {\n\t\treturn a - b\n\t}\n\treturn b - a\n}\n\n", i, i)
	}
	return b.String()
}

func texts(src string, rs []Range) []string {
	var out []string
	for _, r := range rs {
		out = append(out, src[r.Start:r.End])
	}
	return out
}

func (s *fimSuite) TestConstructsGo() {
	got := texts(goSrc, Constructs(dedup.Go, goSrc))
	s.Contains(got, "return sum")
	s.Contains(got, `fmt.Println("sum", sum)`)
	s.Contains(got, `"sum", sum`)
	s.Contains(got, "return a - b")
}

func (s *fimSuite) TestConstructsBuzz() {
	got := texts(buzzSrc, Constructs(dedup.Buzz, buzzSrc))
	s.Contains(got, "return sum;")
	s.Contains(got, `final sum = a + b;`)
	s.Contains(got, "return a - b;")
	s.Contains(got, `std\assert(add(1, b: 2) == 3, message: "add");`)
	s.Contains(got, `1, b: 2`)
}

func (s *fimSuite) TestConstructsTypeScript() {
	got := texts(tsSrc, Constructs(dedup.TypeScript, tsSrc))
	s.Contains(got, "return sum;")
	s.Contains(got, "const sum = a + b;")
	s.Contains(got, "'sum', sum")
}

func (s *fimSuite) TestConstructsOfBrokenSourceAreNone() {
	s.Empty(Constructs(dedup.Go, "func ("))
	s.Empty(Constructs(dedup.Buzz, "fun ("))
	s.Empty(Constructs(dedup.TypeScript, "function ( {"))
}

func (s *fimSuite) TestSeedIsFixedPerFileAndBlob() {
	a1, b1 := Seed("a.go", "blob1")
	a2, b2 := Seed("a.go", "blob1")
	a3, _ := Seed("a.go", "blob2")
	s.Equal([2]uint64{a1, b1}, [2]uint64{a2, b2})
	s.NotEqual(a1, a3)
}

func (s *fimSuite) TestBuildIsDeterministic() {
	f := File{Path: "a.go", Blob: "b1", Language: dedup.Go, Text: manyGo(20)}
	one := Build(f, 200, Limit{})
	two := Build(f, 200, Limit{})
	s.Equal(one, two)
	other := Build(File{Path: "b.go", Blob: "b1", Language: dedup.Go, Text: f.Text}, 200, Limit{})
	s.NotEqual(one, other)
}

func (s *fimSuite) TestSampleInvariants() {
	text := manyGo(30)
	f := File{Path: "a.go", Blob: "b1", Language: dedup.Go, Text: text}
	kinds := map[Kind]int{}
	orders := map[Order]int{}
	for _, sm := range Build(f, 2000, Limit{}) {
		kinds[sm.Kind]++
		if sm.Kind == KindLM {
			s.Empty(sm.Middle)
			s.Empty(sm.Suffix)
			s.NotEmpty(sm.Prefix)
			s.Empty(sm.Order)
			continue
		}
		orders[sm.Order]++
		s.Equal(text[sm.Start:sm.End], sm.Middle)
		s.Equal(sm.Prefix, text[sm.Start-len(sm.Prefix):sm.Start])
		s.True(strings.HasPrefix(text[sm.End:], sm.Suffix))
		s.LessOrEqual(len(sm.Prefix), PrefixWindow)
		s.LessOrEqual(len(sm.Suffix), SuffixWindow)
		s.Empty(sm.Context)
		if sm.Kind == KindEmpty {
			s.Empty(sm.Middle)
		} else {
			s.NotEmpty(sm.Middle)
		}
	}
	total := 2000.0
	s.InDelta(0.1, float64(kinds[KindLM])/total, 0.03)
	s.InDelta(0.9*0.4, float64(kinds[KindAST])/total, 0.04)
	s.InDelta(0.9*0.3, float64(kinds[KindLine])/total, 0.04)
	s.InDelta(0.9*0.2, float64(kinds[KindRandom])/total, 0.04)
	s.InDelta(0.9*0.1, float64(kinds[KindEmpty])/total, 0.04)
	s.InDelta(0.5, float64(orders[PSM])/(total-float64(kinds[KindLM])), 0.05)
}

func (s *fimSuite) TestPrefixWindowStartsOnAlignedLine() {
	var b strings.Builder
	for i := range 3000 {
		fmt.Fprintf(&b, "line %d of the file\n", i)
	}
	text := b.String()
	r := Range{Start: 30000, End: 30010}
	prefix, middle, suffix := Windows(text, r)
	start := r.Start - len(prefix)
	s.LessOrEqual(len(prefix), PrefixWindow)
	s.True(start == 0 || text[start-1] == '\n')
	s.GreaterOrEqual(start, ((r.Start-PrefixWindow)/AnchorAlign+1)*AnchorAlign)
	s.Equal(text[r.Start:r.End], middle)
	s.Len(suffix, SuffixWindow)

	short, _, _ := Windows(text, Range{Start: 100, End: 100})
	s.Len(short, 100)
}

func (s *fimSuite) TestLimitReservesATail() {
	text := manyGo(30)
	f := File{Path: "a.go", Blob: "b1", Language: dedup.Go, Text: text}
	cut := len(text) * 8 / 10
	for _, sm := range Build(f, 500, Limit{To: cut}) {
		if sm.Kind != KindLM {
			s.LessOrEqual(sm.End, cut)
		}
	}
}

func (s *fimSuite) TestFileWithoutASpanFallsBackAndSaysSo() {
	f := File{Path: "a.go", Blob: "b1", Language: dedup.Go, Text: "func ("}
	var fell int
	for _, sm := range Build(f, 300, Limit{}) {
		if sm.Fallback != "" {
			fell++
			s.Equal(KindEmpty, sm.Kind)
		}
		s.NotEqual(KindAST, sm.Kind)
	}
	s.Positive(fell)
}
