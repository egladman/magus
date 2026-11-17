package eval

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/train/completion/fim"
	"github.com/egladman/magus/libs/train/dedup"
)

type setsSuite struct{ suite.Suite }

func TestSetsSuite(t *testing.T) { suite.Run(t, new(setsSuite)) }

func (s *setsSuite) TestTextIsTheMiddle() {
	s.Equal("MID", Text(fim.Sample{Prefix: "a\nb", Middle: "MID", Suffix: "f\ng"}))
}

func (s *setsSuite) TestFileSpansAreDeterministicFimOnlyAndDistinct() {
	var b strings.Builder
	b.WriteString("package p\n\n")
	for i := range 40 {
		b.WriteString("func F" + strings.Repeat("a", i) + "(x int) int {\n\ty := x * 2\n\treturn y + 1\n}\n\n")
	}
	f := fim.File{Path: "p.go", Blob: "b", Language: dedup.Go, Text: b.String()}
	one := FileSpans(f, fim.Limit{})
	s.Equal(one, FileSpans(f, fim.Limit{}))
	s.GreaterOrEqual(len(one), 10)
	seen := map[[2]int]bool{}
	for _, sm := range one {
		s.NotEqual(fim.KindLM, sm.Kind)
		s.False(seen[[2]int{sm.Start, sm.End}])
		seen[[2]int{sm.Start, sm.End}] = true
	}
	tail := FileSpans(f, fim.Limit{From: len(f.Text) * 8 / 10})
	for _, sm := range tail {
		s.GreaterOrEqual(sm.Start, len(f.Text)*8/10)
	}
}

const taskSrc = `import "std";

fun scale(n: int) > int {
    final twice = n * 2;
    return twice;
}

fun unused(n: int) > int {
    final nothing = n + 100;
    return nothing;
}

test "scale scales" {
    std\assert(scale(2) == 4, message: "scale");
}
`

func (s *setsSuite) TestFileTasksNeedATestThatCallsTheFunction() {
	tasks := FileTasks(fim.File{Path: "a.buzz", Blob: "b", Language: dedup.Buzz, Text: taskSrc}, "dir:.")
	s.Require().Len(tasks, 1)
	t := tasks[0]
	s.Equal("scale", t.Name)
	s.Equal([]string{"scale scales"}, t.Tests)
	s.Contains(t.Middle, "final twice = n * 2;")
	s.True(strings.HasSuffix(t.Prefix, "{"))
	s.True(strings.HasPrefix(t.Suffix, "}"))
	s.Equal(taskSrc, t.Prefix+t.Middle+t.Suffix)
	s.Empty(FileTasks(fim.File{Path: "b.buzz", Language: dedup.Buzz, Text: "fun ("}, "g"))
}
