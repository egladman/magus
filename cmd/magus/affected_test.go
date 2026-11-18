package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/clikit/exit"
)

type AffectedOperandsSuite struct{ suite.Suite }

func TestAffectedOperandsSuite(t *testing.T) { suite.Run(t, new(AffectedOperandsSuite)) }

// refused runs affected with args and returns its refusal, failing when there is none.
func (s *AffectedOperandsSuite) refused(args ...string) *affectedOperandError {
	err := affected(context.Background(), s.T().TempDir(), runConfig{}, args)
	var refusal *affectedOperandError
	s.Require().ErrorAs(err, &refusal)
	s.Equal(exit.Usage, exit.Code(err))
	return refusal
}

func (s *AffectedOperandsSuite) TestANamedProjectIsRefused() {
	got := s.refused("ci", "web")
	s.Equal([]string{"web"}, got.Operands)
	s.ErrorContains(got, "every project the diff reaches")
	s.ErrorContains(got, "run ci <projects>` picks the projects")
}

func (s *AffectedOperandsSuite) TestDotIsRefused() {
	s.Equal([]string{"."}, s.refused("ci", ".").Operands)
}

func (s *AffectedOperandsSuite) TestSlashIsRefused() {
	s.Equal([]string{"/"}, s.refused("ci", "/").Operands)
}

func (s *AffectedOperandsSuite) TestAProjectAfterAFlagIsRefused() {
	s.Equal([]string{"web", "api"}, s.refused("test", "--base", "main", "web", "api").Operands)
}
