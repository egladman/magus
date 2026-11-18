package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
)

// operandWS answers only what resolving project operands asks of a workspace.
type operandWS struct {
	types.WorkspaceRepository
	projects []string
}

func (w operandWS) Root() string { return "/ws" }

func (w operandWS) ExpandPath(t types.Target) ([]types.Target, error) {
	if t.Path == "" || t.Path == "/" {
		out := make([]types.Target, len(w.projects))
		for i, p := range w.projects {
			out[i] = types.Target{Path: p, Name: t.Name}
		}
		return out, nil
	}
	for _, p := range w.projects {
		if p == t.Path {
			return []types.Target{{Path: p, Name: t.Name}}, nil
		}
	}
	return nil, fmt.Errorf("expand %q: %w", t.Path, types.ErrUnknownProject)
}

type AffectedOperandsSuite struct{ suite.Suite }

func TestAffectedOperandsSuite(t *testing.T) { suite.Run(t, new(AffectedOperandsSuite)) }

func (s *AffectedOperandsSuite) paths(ts []types.Target) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Path
	}
	return out
}

func (s *AffectedOperandsSuite) setup() (operandWS, []types.Target) {
	ws := operandWS{projects: []string{"api", "web", "docs"}}
	affected := []types.Target{{Path: "api", Name: "test"}, {Path: "web", Name: "test"}}
	return ws, affected
}

func (s *AffectedOperandsSuite) TestANamedProjectNarrowsTheSet() {
	ws, affected := s.setup()
	got, err := narrowAffected(context.Background(), ws, "test", affected, []string{"web"})
	s.Require().NoError(err)
	s.Equal([]string{"web"}, s.paths(got))
}

func (s *AffectedOperandsSuite) TestANamedProjectOutsideTheSetEmptiesIt() {
	ws, affected := s.setup()
	got, err := narrowAffected(context.Background(), ws, "test", affected, []string{"docs"})
	s.Require().NoError(err)
	s.Empty(got)
}

func (s *AffectedOperandsSuite) TestSlashIsNoFilter() {
	ws, affected := s.setup()
	got, err := narrowAffected(context.Background(), ws, "test", affected, []string{"/"})
	s.Require().NoError(err)
	s.Equal([]string{"api", "web"}, s.paths(got))
}

func (s *AffectedOperandsSuite) TestNoOperandsIsNoFilter() {
	ws, affected := s.setup()
	got, err := narrowAffected(context.Background(), ws, "test", affected, nil)
	s.Require().NoError(err)
	s.Equal([]string{"api", "web"}, s.paths(got))
}

func (s *AffectedOperandsSuite) TestAnUnknownProjectIsRefusedByName() {
	ws, affected := s.setup()
	_, err := narrowAffected(context.Background(), ws, "test", affected, []string{"nope"})
	s.Require().ErrorIs(err, types.ErrUnknownProject)
	s.ErrorContains(err, "nope")
}
