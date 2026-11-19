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

func (s *AnalyzerSuite) policy(body string) string {
	path := filepath.Join(s.T().TempDir(), "analyzers.yaml")
	s.Require().NoError(os.WriteFile(path, []byte(body), 0o644))
	return path
}

const policyBody = `other:
  baseline: {unread: true}
accessorclone:
  baseline:
    api/api.go: [Store.Listed]
    stale/stale.go: [Box.Gone]
  exits:
    - path: nolint/nolint.go
      symbol: Box.Held
      reason: the fixture pins that a nolint directive excuses nothing
`

// TestReportsAccessorsOutsideThePolicy covers api (reported, resliced,
// multi-value, package variable, excused, cloned, closure, unexported),
// quiet (nothing to report), stale (a baseline entry to delete) and nolint
// (a directive that excuses nothing).
func (s *AnalyzerSuite) TestReportsAccessorsOutsideThePolicy() {
	analyzer, err := New(Options{Policy: s.policy(policyBody)})
	s.Require().NoError(err)
	analysistest.Run(s.T(), analysistest.TestData(), analyzer, "api", "quiet", "stale", "nolint")
}

func (s *AnalyzerSuite) TestNewRequiresAPolicy() {
	_, err := New(Options{})
	s.Require().ErrorContains(err, "no policy file")
}

func (s *AnalyzerSuite) rejects(body, want string) {
	_, err := New(Options{Policy: s.policy(body)})
	s.Require().ErrorContains(err, want)
}

func (s *AnalyzerSuite) TestNewRejectsAPolicyWithNoKey() {
	s.rejects("other: {}\n", "holds no accessorclone key")
}

func (s *AnalyzerSuite) TestNewRejectsAnEmptySymbol() {
	s.rejects("accessorclone:\n  baseline:\n    a.go: [\"\"]\n", "names a path and a symbol")
}

func (s *AnalyzerSuite) TestNewRejectsAnUnknownField() {
	s.rejects("accessorclone:\n  baselines: {}\n", "not found")
}

func (s *AnalyzerSuite) TestNewRejectsAnAccessorListedTwice() {
	s.rejects("accessorclone:\n  baseline:\n    a.go: [A, A]\n", "twice")
}

func (s *AnalyzerSuite) TestNewRejectsAnExitWithNoReason() {
	s.rejects("accessorclone:\n  exits:\n    - {path: a.go, symbol: A}\n", "with no reason")
}

func (s *AnalyzerSuite) TestNewRejectsAnExitThatRepeatsTheBaseline() {
	s.rejects("accessorclone:\n  baseline:\n    a.go: [A]\n  exits:\n    - {path: a.go, symbol: A, reason: r}\n",
		"in accessorclone.baseline and accessorclone.exits")
}

func (s *AnalyzerSuite) TestNewRejectsAMovedFile() {
	sourcetest.Module(s.T(), "example.com/m", "pkg/pkg.go")
	path := s.policy("accessorclone:\n  baseline:\n    pkg/moved.go: [A]\n")
	_, err := New(Options{Module: "example.com/m", Policy: path})
	s.Require().Error(err)
	s.True(strings.Contains(err.Error(), "pkg/moved.go"), err.Error())
}

func (s *AnalyzerSuite) TestNewAcceptsAFileThatExists() {
	sourcetest.Module(s.T(), "example.com/m", "pkg/pkg.go")
	path := s.policy("accessorclone:\n  baseline:\n    pkg/pkg.go: [A]\n")
	_, err := New(Options{Module: "example.com/m", Policy: path})
	s.Require().NoError(err)
}
