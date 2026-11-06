//go:build !wasm

package std

import (
	"context"
	"errors"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/suite"
)

type fakeQualityWorkspace struct {
	types.WorkspaceRepository
	risk     types.ChangeRisk
	err      error
	riskBase string
}

func (f *fakeQualityWorkspace) ChangeRisk(_ context.Context, base string) (types.ChangeRisk, error) {
	f.riskBase = base
	return f.risk, f.err
}

type fakeWorkspaceWithoutQuality struct {
	types.WorkspaceRepository
}

type MagusChangeRiskSuite struct {
	suite.Suite
}

func TestMagusChangeRiskSuite(t *testing.T) {
	suite.Run(t, new(MagusChangeRiskSuite))
}

func (s *MagusChangeRiskSuite) TestReturnsTheTypedRecordForTheBase() {
	want := types.ChangeRisk{
		Definition: "d",
		Base:       "origin/main",
		Functions:  []types.ChangeRiskFunction{{Label: "f", Complexity: 3, Covered: 2, Coverable: 4, Score: 4.5}},
		Gaps:       []types.FunctionGap{{Label: "g", Reason: "spell bash declares no coverage"}},
	}
	ws := &fakeQualityWorkspace{risk: want}

	got, err := scoreRisk(types.WithWorkspace(s.T().Context(), ws), "Change.risk", "origin/main")

	s.Require().NoError(err)
	s.Equal(want, got)
	s.Equal("origin/main", ws.riskBase)
}

func (s *MagusChangeRiskSuite) TestWorkspaceRiskScoresEveryFunction() {
	ws := &fakeQualityWorkspace{riskBase: "unset"}

	_, err := MagusWorkspaceRisk(types.WithWorkspace(s.T().Context(), ws), types.MagusWorkspace{})

	s.Require().NoError(err)
	s.Empty(ws.riskBase)
}

func (s *MagusChangeRiskSuite) TestChangeRiskRefusesAChangeWithNoTree() {
	ws := &fakeQualityWorkspace{}

	_, err := MagusChangeRisk(types.WithWorkspace(s.T().Context(), ws), types.Change{Base: "origin/main"})

	s.ErrorContains(err, "magus\\Change.risk: this Change records no tree")
	s.Empty(ws.riskBase)
}

func (s *MagusChangeRiskSuite) TestRaisesOutsideAWorkspace() {
	_, err := MagusWorkspaceRisk(s.T().Context(), types.MagusWorkspace{})

	s.ErrorContains(err, "magus\\Workspace.risk: no workspace on the context")
}

func (s *MagusChangeRiskSuite) TestRaisesWhenTheWorkspaceCannotScore() {
	ws := &fakeWorkspaceWithoutQuality{}

	_, err := MagusWorkspaceRisk(types.WithWorkspace(s.T().Context(), ws), types.MagusWorkspace{})

	s.ErrorContains(err, "magus\\Workspace.risk: this workspace cannot score functions")
}

func (s *MagusChangeRiskSuite) TestRaisesWhatTheEngineRaises() {
	ws := &fakeQualityWorkspace{err: errors.New("no coverage report")}

	_, err := scoreRisk(types.WithWorkspace(s.T().Context(), ws), "Change.risk", "origin/main")

	s.ErrorContains(err, "magus\\Change.risk: no coverage report")
}

type RiskVerdictSuite struct {
	suite.Suite
}

func TestRiskVerdictSuite(t *testing.T) {
	suite.Run(t, new(RiskVerdictSuite))
}

func (s *RiskVerdictSuite) TestChargedRefusesANewFunctionPastThreshold() {
	got, err := MagusChangeRiskFunctionCharged(s.T().Context(), types.ChangeRiskFunction{Score: 31.5, BaseReason: "it is new since base"}, 10)

	s.Require().NoError(err)
	s.True(got)
}

func (s *RiskVerdictSuite) TestChargedPassesAFunctionAtThreshold() {
	got, err := MagusChangeRiskFunctionCharged(s.T().Context(), types.ChangeRiskFunction{Score: 10, BaseReason: "it is new since base"}, 10)

	s.Require().NoError(err)
	s.False(got)
}

func (s *RiskVerdictSuite) TestChargedPassesAFunctionNoWorseThanAtBase() {
	got, err := MagusChangeRiskFunctionCharged(s.T().Context(), types.ChangeRiskFunction{Score: 80, BaseScore: 80}, 10)

	s.Require().NoError(err)
	s.False(got)
}

func (s *RiskVerdictSuite) TestChargedRefusesAFunctionWorseThanAtBase() {
	got, err := MagusChangeRiskFunctionCharged(s.T().Context(), types.ChangeRiskFunction{Score: 156, BaseScore: 80}, 10)

	s.Require().NoError(err)
	s.True(got)
}

func (s *RiskVerdictSuite) TestDecidedHoldsWhenTheWorstCaseIsWithinThreshold() {
	got, err := MagusFunctionGapDecided(s.T().Context(), types.FunctionGap{Complexity: 2}, 10)

	s.Require().NoError(err)
	s.True(got)
}

func (s *RiskVerdictSuite) TestDecidedFailsWhenTheWorstCaseIsPastThreshold() {
	got, err := MagusFunctionGapDecided(s.T().Context(), types.FunctionGap{Complexity: 3}, 10)

	s.Require().NoError(err)
	s.False(got)
}

func (s *RiskVerdictSuite) TestDecidedFailsOnUnknownComplexity() {
	got, err := MagusFunctionGapDecided(s.T().Context(), types.FunctionGap{}, 10)

	s.Require().NoError(err)
	s.False(got)
}
