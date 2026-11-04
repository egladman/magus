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
	crap       types.Crap
	mutants    types.Mutants
	err        error
	crapBase   string
	mutantBase string
}

func (f *fakeQualityWorkspace) Crap(_ context.Context, base string) (types.Crap, error) {
	f.crapBase = base
	return f.crap, f.err
}

func (f *fakeQualityWorkspace) Mutants(_ context.Context, base string) (types.Mutants, error) {
	f.mutantBase = base
	return f.mutants, f.err
}

type MagusCrapSuite struct {
	suite.Suite
}

func TestMagusCrapSuite(t *testing.T) {
	suite.Run(t, new(MagusCrapSuite))
}

func (s *MagusCrapSuite) TestReturnsTheTypedRecordForTheBase() {
	want := types.Crap{
		Definition: "d",
		Base:       "origin/main",
		Functions:  []types.CrapFunction{{Label: "f", Complexity: 3, Covered: 2, Coverable: 4, Score: 4.5}},
		Gaps:       []types.FunctionGap{{Label: "g", Reason: "spell bash declares no coverage"}},
	}
	ws := &fakeQualityWorkspace{crap: want}

	got, err := MagusCrap(types.WithWorkspace(s.T().Context(), ws), "origin/main")

	s.Require().NoError(err)
	s.Equal(want, got)
	s.Equal("origin/main", ws.crapBase)
}

func (s *MagusCrapSuite) TestEmptyBaseScoresTheWholeWorkspace() {
	ws := &fakeQualityWorkspace{}

	_, err := MagusCrap(types.WithWorkspace(s.T().Context(), ws), "")

	s.Require().NoError(err)
	s.Empty(ws.crapBase)
}

func (s *MagusCrapSuite) TestRaisesOutsideAWorkspace() {
	_, err := MagusCrap(s.T().Context(), "origin/main")

	s.ErrorContains(err, "magus\\crap: no workspace on the context")
}

func (s *MagusCrapSuite) TestRaisesWhenTheWorkspaceCannotScore() {
	ws := &fakeWorkspaceWithoutQuality{}

	_, err := MagusCrap(types.WithWorkspace(s.T().Context(), ws), "origin/main")

	s.ErrorContains(err, "magus\\crap: this workspace cannot score functions")
}

func (s *MagusCrapSuite) TestRaisesWhatTheEngineRaises() {
	ws := &fakeQualityWorkspace{err: errors.New("no coverage report")}

	_, err := MagusCrap(types.WithWorkspace(s.T().Context(), ws), "origin/main")

	s.ErrorContains(err, "magus\\crap: no coverage report")
}

type fakeWorkspaceWithoutQuality struct {
	types.WorkspaceRepository
}

type MagusMutantsSuite struct {
	suite.Suite
}

func TestMagusMutantsSuite(t *testing.T) {
	suite.Run(t, new(MagusMutantsSuite))
}

func (s *MagusMutantsSuite) TestReturnsTheTypedRecordForTheBase() {
	want := types.Mutants{
		Definition: "d",
		Base:       "origin/main",
		Mutants:    []types.Mutant{{ID: "a1", Outcome: types.MutantSurvived, Line: 4, Col: 9}},
		Gaps:       []types.FunctionGap{{Label: "g", Reason: "tests fail without a mutant"}},
		Unused:     []types.Finding{{Path: "a.go", Line: 3, Message: "marker matches no survivor"}},
	}
	ws := &fakeQualityWorkspace{mutants: want}

	got, err := MagusMutants(types.WithWorkspace(s.T().Context(), ws), "origin/main")

	s.Require().NoError(err)
	s.Equal(want, got)
	s.Equal("origin/main", ws.mutantBase)
}

func (s *MagusMutantsSuite) TestRefusesAnEmptyBaseBeforeTouchingTheWorkspace() {
	ws := &fakeQualityWorkspace{}

	_, err := MagusMutants(types.WithWorkspace(s.T().Context(), ws), "")

	s.ErrorContains(err, "name a base")
	s.Empty(ws.mutantBase)
}

func (s *MagusMutantsSuite) TestRaisesOutsideAWorkspace() {
	_, err := MagusMutants(s.T().Context(), "origin/main")

	s.ErrorContains(err, "magus\\mutants: no workspace on the context")
}

func (s *MagusMutantsSuite) TestRaisesWhenTheWorkspaceCannotMutate() {
	ws := &fakeWorkspaceWithoutQuality{}

	_, err := MagusMutants(types.WithWorkspace(s.T().Context(), ws), "origin/main")

	s.ErrorContains(err, "magus\\mutants: this workspace cannot score functions")
}

func (s *MagusMutantsSuite) TestRaisesWhatTheEngineRaises() {
	ws := &fakeQualityWorkspace{err: errors.New("baseline red")}

	_, err := MagusMutants(types.WithWorkspace(s.T().Context(), ws), "origin/main")

	s.ErrorContains(err, "magus\\mutants: baseline red")
}
