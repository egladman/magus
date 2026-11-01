package doctor

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
)

// conformanceWorkspace answers the conformance lens with a fixed output, reached the way the check
// reaches it: by runtime assertion to types.HistoryMetrics.
type conformanceWorkspace struct {
	hotWorkspace
	out types.Conformance
	err error
}

func (c conformanceWorkspace) Conformance(context.Context) (types.Conformance, error) {
	return c.out, c.err
}

var _ types.HistoryMetrics = conformanceWorkspace{}

type conformanceCeilingSuite struct {
	suite.Suite
}

func TestConformanceCeilingSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(conformanceCeilingSuite))
}

func (s *conformanceCeilingSuite) check(ws types.WorkspaceReader) types.Check {
	var cfg config.Config
	cfg.Knowledge.Conformance = config.ConformanceConfig{
		Checks: map[types.ConformanceMetric]map[types.ConformancePopulation]config.ConformanceLimitConfig{
			types.ConformanceFuncLines: {types.ConformancePopSource: {Percentile: 99, Anchor: 92}},
		},
	}
	r := &runner{opts: options{cfg: cfg}, root: s.T().TempDir(), ws: ws, ctx: s.T().Context()}
	return r.checkConformanceCeiling(nil)
}

func (s *conformanceCeilingSuite) TestNoAnalyzerCannotMeasure() {
	got := s.check(nil)
	s.Equal(types.Check{
		Name: "conformance-ceiling", Status: types.CheckOK, Evidence: types.EvidenceUnknown,
		Message: "conformance anchors were not measured: this workspace exposes no conformance lens",
		Details: []string{"build the symbol index with `magus graph build`, then run doctor again"},
	}, got)
}

func (s *conformanceCeilingSuite) TestALensErrorIsAFailureNamingTheCause() {
	got := s.check(conformanceWorkspace{err: errors.New("min_sample 0: want at least 1")})
	s.Equal(types.Check{
		Name: "conformance-ceiling", Status: types.CheckFail, Evidence: types.EvidenceUnknown,
		Message: "conformance anchors could not be measured", Details: []string{"min_sample 0: want at least 1"},
	}, got)
}

func (s *conformanceCeilingSuite) TestAProposalNamesTheAnchorToLower() {
	key := types.ConformanceKey{Metric: types.ConformanceFuncLines, Population: types.ConformancePopSource}
	got := s.check(conformanceWorkspace{out: types.Conformance{
		Answer: types.KnowledgeAnswer{Verdict: types.VerdictFound},
		Rows:   []types.ConformanceRow{{Key: key, Value: 92, Propose: 70}},
	}})
	s.Equal(types.Check{
		Name: "conformance-ceiling", Status: types.CheckAdvice, Evidence: types.EvidenceMeasured,
		Message: "1 conformance anchor(s) can tighten",
		Details: []string{"knowledge.conformance.checks.func-lines.source.anchor: 92, the code measures 70 at p99"},
	}, got)
}

func (s *conformanceCeilingSuite) TestNothingToTightenPasses() {
	key := types.ConformanceKey{Metric: types.ConformanceFuncLines, Population: types.ConformancePopSource}
	got := s.check(conformanceWorkspace{out: types.Conformance{
		Answer: types.KnowledgeAnswer{Verdict: types.VerdictFound},
		Rows:   []types.ConformanceRow{{Key: key, Value: 92}},
	}})
	s.Equal(types.Check{
		Name: "conformance-ceiling", Status: types.CheckOK, Evidence: types.EvidenceMeasured,
		Message: "no conformance anchor is looser than the code measures",
	}, got)
}
