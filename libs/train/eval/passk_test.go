package eval

import (
	"encoding/json"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/train/completion/fim"
)

type PassKSuite struct{ suite.Suite }

func TestPassK(t *testing.T) { suite.Run(t, new(PassKSuite)) }

func (s *PassKSuite) TestEstimatorMatchesTheClosedForm() {
	s.Equal(0.0, PassAtK(20, 0, 1))
	s.Equal(1.0, PassAtK(20, 20, 10))
	s.InDelta(0.25, PassAtK(20, 5, 1), 1e-12)
	// 1 - C(15,5)/C(20,5)
	s.InDelta(1-3003.0/15504.0, PassAtK(20, 5, 5), 1e-12)
	s.Equal(1.0, PassAtK(20, 16, 5))
	s.Equal(0.0, PassAtK(5, 1, 6))
	s.False(math.IsNaN(PassAtK(20, 3, 10)))
}

func (s *PassKSuite) TestAllPassNeedsEveryNamedTestOk() {
	out := "ok    test \"a\"\nFAIL  test \"b\"\n      std.assert: x\n---\n1 passed, 1 failed, 0 skipped\n"
	s.True(AllPass(out, []string{"a"}))
	s.False(AllPass(out, []string{"a", "b"}))
	s.False(AllPass("[error] boom", []string{"a"}))
	s.False(AllPass(out, nil))
}

func (s *PassKSuite) TestSeatbeltDeniesNetworkAndLimitsWrites() {
	p := Seatbelt("/scratch", "/home/u")
	s.Contains(p, "(deny network*)")
	s.Contains(p, `(subpath "/scratch")`)
	s.Contains(p, `"/home/u/.cache/magus/buzz-bytecode"`)
}

type PassKScoreSuite struct{ suite.Suite }

func TestPassKScore(t *testing.T) { suite.Run(t, new(PassKScoreSuite)) }

func writeGrades(s *PassKScoreSuite, e S3, sys System, grades []Grade) {
	var b strings.Builder
	for _, g := range grades {
		line, err := json.Marshal(g)
		s.Require().NoError(err)
		b.Write(line)
		b.WriteByte('\n')
	}
	s.Require().NoError(os.WriteFile(e.gradesPath(sys), []byte(b.String()), 0o644))
}

func taskGrades(id string, ref bool, pass, checked int) []Grade {
	out := []Grade{{Task: id, I: -1, Checked: true, Passed: ref}}
	for i := range PassN {
		out = append(out, Grade{Task: id, I: i, Checked: i < checked, Passed: i < pass})
	}
	return out
}

func (s *PassKScoreSuite) TestScorePassKUsesGradableTasksAndTheEstimator() {
	e := S3{Out: s.T().TempDir()}
	sys := System{Name: "m", Order: fim.SPM, Context: "none"}
	tasks := []Task{{ID: "a", Group: "ga"}, {ID: "b", Group: "gb"}, {ID: "c", Group: "gc"}}
	var grades []Grade
	grades = append(grades, taskGrades("a", true, 5, 10)...)
	grades = append(grades, taskGrades("b", true, 0, 20)...)
	grades = append(grades, taskGrades("c", false, 20, 20)...)
	writeGrades(s, e, sys, grades)
	m, err := e.ScorePassK(sys, tasks, "r")
	s.Require().NoError(err)
	s.Equal(2, m.Gradable)
	s.Equal(3, m.Of)
	p1 := metric(m.Rows, "pass@1")
	s.InDelta((0.25+0)/2, *p1.Value, 1e-12)
	s.Equal(2, p1.N)
	s.Equal(3, p1.Of)
	s.InDelta((0.5+1)/2, *metric(m.Rows, "sample_check_pass").Value, 1e-12)
	s.Equal("buzz-passk", p1.Set)
}

func (s *PassKScoreSuite) TestScorePassKRefusesAShortSampleCount() {
	e := S3{Out: s.T().TempDir()}
	sys := System{Name: "m", Order: fim.SPM, Context: "none"}
	g := taskGrades("a", true, 1, 1)
	writeGrades(s, e, sys, g[:10])
	_, err := e.ScorePassK(sys, []Task{{ID: "a"}}, "r")
	s.ErrorContains(err, "needs "+strconv.Itoa(PassN))
	_, err = e.ScorePassK(sys, []Task{{ID: "zz"}}, "r")
	s.ErrorContains(err, "no graded reference")
}

func (s *PassKScoreSuite) TestTaskPromptClipsToTheEditorWindows() {
	t := Task{Prefix: strings.Repeat("x\n", 5000) + "fun f() {", Middle: "\n    body\n", Suffix: "}\n" + strings.Repeat("y\n", 5000)}
	p, sfx := TaskPrompt(t)
	s.LessOrEqual(len(p), fim.PrefixWindow)
	s.LessOrEqual(len(sfx), fim.SuffixWindow)
	s.True(strings.HasSuffix(p, "fun f() {"))
	s.True(strings.HasPrefix(sfx, "}"))
}
