package access

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/safetyscissors/verdict"
)

type ruleSuite struct {
	suite.Suite
}

func TestRule(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(ruleSuite))
}

func never(context.Context, refundRequest) (bool, error) { return false, nil }

func supportBundle(rules ...Rule[agent, ticket, int]) Bundle[agent, ticket, int] {
	return Bundle[agent, ticket, int]{Name: "support", Acts: []Act{refund}, Rules: rules}
}

// refused is the declaration error New gives b.
func (s *ruleSuite) refused(b Bundle[agent, ticket, int]) *DeclarationError {
	table, err := New(b)
	s.Nil(table)
	var problem *DeclarationError
	s.Require().ErrorAs(err, &problem)
	return problem
}

func (s *ruleSuite) TestAccessorsReadTheDeclaration() {
	permit := Permit("refund-own-team", refund, "the agent's own team", never)
	s.Equal("refund-own-team", permit.Name())
	s.Equal(refund, permit.Act())
	s.Equal("the agent's own team", permit.Description())
	s.True(permit.Permits())
	s.Equal(verdict.Decision(""), permit.Decision())
	s.Empty(permit.Bundle(), "a rule outside a table names no bundle")

	forbid := Forbid("large-refund", refund, verdict.Ask, "over 100", never)
	s.False(forbid.Permits())
	s.Equal(verdict.Ask, forbid.Decision())
}

func (s *ruleSuite) TestABadRuleNameUnwrapsToTheVerdictRefusal() {
	problem := s.refused(supportBundle(Permit("RefundOwnTeam", refund, "own team", never)))
	var name *verdict.RuleNameError
	s.Require().ErrorAs(problem, &name)
	s.Equal("refund-own-team", name.Suggestion)
}

func (s *ruleSuite) TestAnEmptyDescriptionIsRefused() {
	s.Equal("the description is empty", s.refused(supportBundle(Permit("own-team", refund, " ", never))).Problem)
}

func (s *ruleSuite) TestANilConditionIsRefused() {
	s.Equal("the condition is nil", s.refused(supportBundle(Forbid[agent, ticket, int]("flagged", refund, verdict.Deny, "flagged", nil))).Problem)
}

func (s *ruleSuite) TestAForbidAnswersOnlyDenyOrAsk() {
	for _, d := range []verdict.Decision{verdict.Advise, verdict.Defer, ""} {
		problem := s.refused(supportBundle(Forbid("flagged", refund, d, "flagged", never)))
		s.Contains(problem.Problem, "deny or ask", "decision %q", d)
	}
}

func (s *ruleSuite) TestABadActNameIsRefused() {
	b := supportBundle()
	b.Acts = []Act{"Refund"}
	s.Equal(Act("Refund"), s.refused(b).Act)
}

func (s *ruleSuite) TestAnActMayTakeAGuardSeamName() {
	b := supportBundle()
	b.Acts = []Act{"write", "two-words2"}
	_, err := New(b)
	s.NoError(err)
}

func (s *ruleSuite) TestAnActNameIsKebabCase() {
	for _, act := range []Act{"-refund", "refund-", "re--fund", "re_fund"} {
		b := supportBundle()
		b.Acts = []Act{act}
		s.Equal(act, s.refused(b).Act)
	}
}

func (s *ruleSuite) TestABadBundleNameIsRefused() {
	b := supportBundle()
	b.Name = ""
	s.Contains(s.refused(b).Problem, "bundle name")
}

func (s *ruleSuite) TestAnActListedTwiceIsRedeclared() {
	b := supportBundle()
	b.Acts = []Act{refund, refund}
	_, err := New(b)
	var redeclared *RedeclaredActError
	s.Require().ErrorAs(err, &redeclared)
	s.Equal(RedeclaredActError{Act: refund, Bundle: "support", DeclaredBy: "support"}, *redeclared)
}

func (s *ruleSuite) TestALaterBundleMayNotRedeclareAnAct() {
	table, err := New(supportBundle())
	s.Require().NoError(err)
	later := supportBundle()
	later.Name = "finance"
	_, err = table.Extend(later)
	var redeclared *RedeclaredActError
	s.Require().ErrorAs(err, &redeclared)
	s.Equal(RedeclaredActError{Act: refund, Bundle: "finance", DeclaredBy: "support"}, *redeclared)
}

func (s *ruleSuite) TestABundleNameIsUsedOnce() {
	table, err := New(supportBundle())
	s.Require().NoError(err)
	_, err = table.Extend(Bundle[agent, ticket, int]{Name: "support"})
	var duplicate *DuplicateBundleError
	s.Require().ErrorAs(err, &duplicate)
	s.Equal("support", duplicate.Bundle)
}

func (s *ruleSuite) TestARuleNameIsUsedOnce() {
	table, err := New(supportBundle(Forbid("flagged", refund, verdict.Deny, "flagged", never)))
	s.Require().NoError(err)
	_, err = table.Extend(Bundle[agent, ticket, int]{Name: "finance", Rules: []Rule[agent, ticket, int]{
		Forbid("flagged", refund, verdict.Ask, "flagged again", never),
	}})
	var duplicate *DuplicateRuleError
	s.Require().ErrorAs(err, &duplicate)
	s.Equal(DuplicateRuleError{Rule: "flagged", Bundle: "finance", DeclaredBy: "support"}, *duplicate)
}

func (s *ruleSuite) TestARuleForAnUndeclaredActIsRefused() {
	_, err := New(supportBundle(Forbid("void-all", "void", verdict.Deny, "never void", never)))
	var undeclared *UndeclaredActError
	s.Require().ErrorAs(err, &undeclared)
	s.Equal(UndeclaredActError{Act: "void", Rule: "void-all", Bundle: "support"}, *undeclared)
}

func (s *ruleSuite) TestAFailingConditionDeniesWithItsError() {
	broken := errors.New("ledger unreachable")
	table, err := New(supportBundle(Forbid("ledger-hold", refund, verdict.Ask, "the ledger holds the ticket",
		func(context.Context, refundRequest) (bool, error) { return false, broken })))
	s.Require().NoError(err)
	v, err := table.Judge(s.T().Context(), refundRequest{Act: refund})
	s.Require().ErrorIs(err, broken)
	var condition *ConditionError
	s.Require().ErrorAs(err, &condition)
	s.Equal("ledger-hold", condition.Rule)
	s.Equal("support", condition.Bundle)
	s.Equal(verdict.Deny, v.Decision)
	s.Equal("ledger-hold", v.Rule)
}

func (s *ruleSuite) TestAnActWithNoPermitIsDenied() {
	table, err := New(supportBundle())
	s.Require().NoError(err)
	v, err := table.Judge(s.T().Context(), refundRequest{Act: refund})
	s.Require().NoError(err)
	s.Equal(verdict.Deny, v.Decision)
	s.Contains(v.Reason, "no rule permits refund")
}

func (s *ruleSuite) TestANilTableDeclaresNothing() {
	var table *Table[agent, ticket, int]
	s.Nil(table.Rules())
	v, err := table.Judge(s.T().Context(), refundRequest{Act: refund})
	var undeclared *UndeclaredActError
	s.Require().ErrorAs(err, &undeclared)
	s.Equal(verdict.Deny, v.Decision)
}
