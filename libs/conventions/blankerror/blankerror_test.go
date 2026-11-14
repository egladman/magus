package blankerror

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
	"golang.org/x/tools/go/analysis/analysistest"
)

type AnalyzerSuite struct {
	suite.Suite
}

func TestBlankError(t *testing.T) {
	suite.Run(t, new(AnalyzerSuite))
}

func (s *AnalyzerSuite) TestNewRequiresABase() {
	_, err := New(Options{})
	s.Require().Error(err)
}

const baseSource = `package a

import "os"

func untouched() {
	_ = os.Remove("x")
}

func edited() {
	_ = os.Remove("x")
	println("before")
}

func adds() {
	println("before")
}

func reasoned() {
	println("before")
}

func fixes() {
	println("before")
}
`

// TestOnlyTouchedFunctionsAreCharged commits baseSource, then analyzes
// testdata/src/a/a.go as the working copy of that repository.
func (s *AnalyzerSuite) TestOnlyTouchedFunctionsAreCharged() {
	if _, err := exec.LookPath("git"); err != nil {
		s.T().Skip("needs git")
	}
	root := s.T().TempDir()
	pkg := filepath.Join(root, "src", "a")
	s.Require().NoError(os.MkdirAll(pkg, 0o755))
	head, err := os.ReadFile(filepath.Join("testdata", "src", "a", "a.go"))
	s.Require().NoError(err)
	s.Require().NoError(os.WriteFile(filepath.Join(pkg, "a.go"), []byte(baseSource), 0o644))
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "."},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "base"},
	} {
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		s.Require().NoError(err, string(out))
	}
	s.Require().NoError(os.WriteFile(filepath.Join(pkg, "a.go"), head, 0o644))

	analyzer, err := New(Options{Base: "HEAD"})
	s.Require().NoError(err)
	var got []int
	for _, r := range analysistest.Run(discard{}, root, analyzer, "a") {
		for _, d := range r.Diagnostics {
			got = append(got, r.Pass.Fset.Position(d.Pos).Line)
		}
	}
	var want []int
	for i, line := range strings.Split(string(head), "\n") {
		if strings.Contains(line, `"flagged"`) {
			want = append(want, i+1)
		}
	}
	s.Equal(want, got)
}

// discard takes analysistest's verdict out of the run: a want comment would
// share the line with the site and count as its reason.
type discard struct{}

func (discard) Errorf(string, ...any) {}
