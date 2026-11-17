package eval

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/train/dedup"
)

type evalSuite struct{ suite.Suite }

func TestEvalSuite(t *testing.T) { suite.Run(t, new(evalSuite)) }

func (s *evalSuite) TestDecontaminatorDropsRepeatedWindows() {
	evalText := "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron"
	d := NewDecontaminator([]string{evalText})
	s.Equal("exact", d.Match(evalText))
	s.Equal("ngram", d.Match("prefix words here "+evalText+" and a tail"))
	s.Equal("", d.Match("one two three four five six seven eight nine ten eleven twelve thirteen fourteen"))
	s.Equal("", d.Match(""))
}

func (s *evalSuite) TestDecontaminatorIgnoresBoilerplateGrams() {
	var texts []string
	for i := range Common + 2 {
		texts = append(texts, "if err != nil { return err } // shared boilerplate line number "+strings.Repeat("x", i))
	}
	d := NewDecontaminator(texts)
	s.Equal("", d.Match("a b c if err != nil { return err } // shared boilerplate line number zz more"))
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

func (s *evalSuite) git(dir string, args ...string) string {
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	out, err := cmd.CombinedOutput()
	s.Require().NoError(err, string(out))
	return strings.TrimSpace(string(out))
}

const scrubSrc = `package p

func Keep(a int) int {
	return a + 1
}

func Leak(a, b int) int {
	first := a * b * 1001 + 7 * a + b * 3 - 12
	second := first * first * 2002 + 9 * first
	return second
}
`

func (s *evalSuite) TestScrubLeavesACleanFileAlone() {
	d := NewDecontaminator([]string{"nothing here matches the source at all not a single window of it"})
	got := Scrub(d, dedup.Go, scrubSrc)
	s.Equal(scrubSrc, got.Text)
	s.Zero(got.Removed)
}

func (s *evalSuite) TestScrubRemovesAFileEqualToAnEvalText() {
	d := NewDecontaminator([]string{scrubSrc})
	s.Empty(Scrub(d, dedup.Go, scrubSrc).Text)
}

func (s *evalSuite) TestG1ThresholdsAreElisRetry3Bars() {
	s.Equal(2000, MinFIMBuzzSpans)
	s.Equal(30, MinFIMBuzzGroups)
	s.Equal(120, MinPassKTasks)
	s.InDelta(0.95, MinCheckPass, 1e-9)
}
