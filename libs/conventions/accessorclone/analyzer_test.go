package accessorclone

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/conventions/internal/sourcetest"

	"github.com/stretchr/testify/suite"
	"golang.org/x/tools/go/analysis/analysistest"
)

type AnalyzerSuite struct {
	suite.Suite
}

func TestAccessorClone(t *testing.T) {
	suite.Run(t, new(AnalyzerSuite))
}

func (s *AnalyzerSuite) baseline(body string) string {
	path := filepath.Join(s.T().TempDir(), "baseline.jsonl")
	s.Require().NoError(os.WriteFile(path, []byte(body), 0o644))
	return path
}

const baselineBody = `{"path":"api/api.go","symbol":"Store.Listed"}
{"path":"stale/stale.go","symbol":"Box.Gone"}
{"path":"nolint/nolint.go","symbol":"Box.Held"}
`

// TestReportsAccessorsOutsideTheBaseline covers api (reported, resliced,
// multi-value, package variable, excused, cloned, closure, unexported),
// quiet (nothing to report), stale (a baseline line to delete) and nolint
// (a directive that excuses nothing).
func (s *AnalyzerSuite) TestReportsAccessorsOutsideTheBaseline() {
	analyzer, err := New(Options{Baseline: s.baseline(baselineBody)})
	s.Require().NoError(err)
	analysistest.Run(s.T(), analysistest.TestData(), analyzer, "api", "quiet", "stale", "nolint")
}

func (s *AnalyzerSuite) TestNewRequiresABaseline() {
	_, err := New(Options{})
	s.Require().ErrorContains(err, "no baseline file")
}

func (s *AnalyzerSuite) TestNewRejectsABadLine() {
	cases := map[string]string{
		"{\"path\":\"a.go\"}\n":                                                        "names a path and a symbol",
		"{\"path\":\"a.go\",\"symbol\":\"A\",\"x\":1}\n":                               "unknown field",
		"{\"path\":\"a.go\",\"symbol\":\"A\"}\n{\"path\":\"a.go\",\"symbol\":\"A\"}\n": "twice",
	}
	for body, want := range cases {
		_, err := New(Options{Baseline: s.baseline(body)})
		s.Require().ErrorContains(err, want)
	}
}

func (s *AnalyzerSuite) TestNewRejectsAMovedFile() {
	sourcetest.Module(s.T(), "example.com/m", "pkg/pkg.go")
	path := s.baseline("{\"path\":\"pkg/moved.go\",\"symbol\":\"A\"}\n")
	_, err := New(Options{Module: "example.com/m", Baseline: path})
	s.Require().Error(err)
	s.True(strings.Contains(err.Error(), "pkg/moved.go"), err.Error())
}

func (s *AnalyzerSuite) TestNewAcceptsAFileThatExists() {
	sourcetest.Module(s.T(), "example.com/m", "pkg/pkg.go")
	path := s.baseline("{\"path\":\"pkg/pkg.go\",\"symbol\":\"A\"}\n")
	_, err := New(Options{Module: "example.com/m", Baseline: path})
	s.Require().NoError(err)
}
