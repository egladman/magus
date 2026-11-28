package access

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/safetyscissors/verdict"
)

type agent struct {
	Team      string
	Clearance int
}

type ticket struct {
	Team    string
	Limit   int
	Flagged bool
}

type refundRequest = Request[agent, ticket, int]

const (
	refund     Act = "refund"
	chargeback Act = "chargeback"
)

func supportDesk() Scope[agent, ticket, int] {
	return Scope[agent, ticket, int]{
		Name: "support",
		Acts: []Act{refund},
		Rules: []Rule[agent, ticket, int]{
			Permit("refund-own-team", refund, "the agent is on the ticket's team and the amount is within the ticket's limit and the agent's clearance",
				func(_ context.Context, r refundRequest) (bool, error) {
					return r.Principal.Team == r.Target.Team && r.Detail <= r.Target.Limit && r.Detail <= r.Principal.Clearance, nil
				}),
			Forbid("flagged-ticket", refund, verdict.Deny, "a flagged ticket is never refunded",
				func(_ context.Context, r refundRequest) (bool, error) { return r.Target.Flagged, nil }),
			Forbid("large-refund", refund, verdict.Ask, "a refund over 100 needs a person's approval",
				func(_ context.Context, r refundRequest) (bool, error) { return r.Detail > 100, nil }),
		},
	}
}

func finance() Scope[agent, ticket, int] {
	return Scope[agent, ticket, int]{
		Name: "finance",
		Acts: []Act{chargeback},
		Rules: []Rule[agent, ticket, int]{
			Permit("chargeback-by-finance", chargeback, "the agent is on the finance team",
				func(_ context.Context, r refundRequest) (bool, error) { return r.Principal.Team == "finance", nil }),
			Forbid("refund-over-500", refund, verdict.Deny, "finance freezes refunds over 500",
				func(_ context.Context, r refundRequest) (bool, error) { return r.Detail > 500, nil }),
		},
	}
}

type supportDeskSuite struct {
	suite.Suite
	set *RuleSet[agent, ticket, int]
}

func TestSupportDesk(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(supportDeskSuite))
}

func (s *supportDeskSuite) SetupTest() {
	set, err := New(supportDesk())
	s.Require().NoError(err)
	s.set = set
}

func (s *supportDeskSuite) judge(set *RuleSet[agent, ticket, int], who agent, what ticket, amount int) verdict.Verdict {
	v, err := set.Judge(s.T().Context(), refundRequest{Principal: who, Act: refund, Target: what, Detail: amount})
	s.Require().NoError(err)
	return v
}

func alice() agent { return agent{Team: "billing", Clearance: 1000} }

func open() ticket { return ticket{Team: "billing", Limit: 1000} }

func (s *supportDeskSuite) TestARefundWithinEveryLimitDefers() {
	s.Equal(verdict.Verdict{}, s.judge(s.set, alice(), open(), 50))
}

func (s *supportDeskSuite) TestAFlaggedTicketIsDenied() {
	flagged := open()
	flagged.Flagged = true
	v := s.judge(s.set, alice(), flagged, 50)
	s.Equal(verdict.Deny, v.Decision)
	s.Equal("flagged-ticket", v.Rule)
	s.Equal("a flagged ticket is never refunded", v.Reason)
}

func (s *supportDeskSuite) TestARefundOver100Asks() {
	v := s.judge(s.set, alice(), open(), 150)
	s.Equal(verdict.Ask, v.Decision)
	s.Equal("large-refund", v.Rule)
}

func (s *supportDeskSuite) TestFlaggedAndLargeTogetherIsADeny() {
	flagged := open()
	flagged.Flagged = true
	v := s.judge(s.set, alice(), flagged, 150)
	s.Equal(verdict.Deny, v.Decision)
	s.Equal("flagged-ticket", v.Rule)
}

func (s *supportDeskSuite) TestOverTheTicketLimitIsADenyListingThePermit() {
	small := open()
	small.Limit = 20
	v := s.judge(s.set, alice(), small, 50)
	s.Equal(verdict.Deny, v.Decision)
	s.Contains(v.Reason, "refund-own-team: the agent is on the ticket's team and the amount is within the ticket's limit and the agent's clearance")
}

func (s *supportDeskSuite) TestOverTheAgentClearanceIsADeny() {
	junior := alice()
	junior.Clearance = 20
	v := s.judge(s.set, junior, open(), 50)
	s.Equal(verdict.Deny, v.Decision)
	s.Contains(v.Reason, "refund-own-team")
}

func (s *supportDeskSuite) TestAnotherTeamsTicketIsADenyListingThePermit() {
	shipping := open()
	shipping.Team = "shipping"
	v := s.judge(s.set, alice(), shipping, 50)
	s.Equal(verdict.Deny, v.Decision)
	s.Contains(v.Reason, "refund-own-team: ")
}

// An ask-forbid that holds beside a missing permit must not read as "ask a person":
// the person would be approving an act no rule allows.
func (s *supportDeskSuite) TestAnAskForbidNeverSoftensAMissingPermit() {
	shipping := open()
	shipping.Team = "shipping"
	v := s.judge(s.set, alice(), shipping, 150)
	s.Equal(verdict.Deny, v.Decision)
	s.Contains(v.Reason, "refund-own-team")
}

func (s *supportDeskSuite) TestAnUndeclaredActErrorsAndDenies() {
	v, err := s.set.Judge(s.T().Context(), refundRequest{Principal: alice(), Act: "void", Target: open()})
	var undeclared *UndeclaredActError
	s.Require().ErrorAs(err, &undeclared)
	s.Equal(Act("void"), undeclared.Act)
	s.Equal(verdict.Deny, v.Decision)
}

func (s *supportDeskSuite) TestFinanceDeclaresItsOwnAct() {
	set, err := s.set.Extend(finance())
	s.Require().NoError(err)
	v, err := set.Judge(s.T().Context(), refundRequest{Principal: agent{Team: "finance"}, Act: chargeback, Target: open()})
	s.Require().NoError(err)
	s.Equal(verdict.Verdict{}, v)
}

func (s *supportDeskSuite) TestFinanceCannotPermitARefund() {
	b := finance()
	b.Rules = append(b.Rules, Permit("finance-refunds", refund, "the agent is on the finance team",
		func(_ context.Context, r refundRequest) (bool, error) { return r.Principal.Team == "finance", nil }))
	set, err := s.set.Extend(b)
	s.Nil(set)
	var foreign *ForeignPermitError
	s.Require().ErrorAs(err, &foreign)
	s.Equal(ForeignPermitError{Rule: "finance-refunds", Act: refund, Scope: "finance", DeclaredBy: "support"}, *foreign)
}

func (s *supportDeskSuite) TestFinanceMayForbidARefund() {
	set, err := s.set.Extend(finance())
	s.Require().NoError(err)
	v := s.judge(set, alice(), open(), 600)
	s.Equal(verdict.Deny, v.Decision)
	s.Equal("refund-over-500", v.Rule)
}

func (s *supportDeskSuite) TestExtendLeavesTheOldRuleSetUnchanged() {
	before := s.set.Rules()
	_, err := s.set.Extend(finance())
	s.Require().NoError(err)
	s.Len(s.set.Rules(), len(before))
	s.Equal(verdict.Ask, s.judge(s.set, alice(), open(), 600).Decision, "the old rule set never learns finance's forbid")
	_, err = s.set.Judge(s.T().Context(), refundRequest{Act: chargeback})
	var undeclared *UndeclaredActError
	s.ErrorAs(err, &undeclared)
}

func (s *supportDeskSuite) TestRulesListInDeclarationOrderWithTheirScope() {
	set, err := s.set.Extend(finance())
	s.Require().NoError(err)
	var names, scopes []string
	for _, r := range set.Rules() {
		names = append(names, r.Name())
		scopes = append(scopes, r.Scope())
	}
	s.Equal([]string{"refund-own-team", "flagged-ticket", "large-refund", "chargeback-by-finance", "refund-over-500"}, names)
	s.Equal([]string{"support", "support", "support", "finance", "finance"}, scopes)
}

type researcher struct{ Name string }

type draft struct {
	Author    string
	Citations int
}

type draftRequest = Request[researcher, draft, struct{}]

const (
	publish Act = "publish"
	accept  Act = "accept"
)

func always(context.Context, draftRequest) (bool, error) { return true, nil }

func pipeline() Scope[researcher, draft, struct{}] {
	return Scope[researcher, draft, struct{}]{
		Name: "research",
		Acts: []Act{publish, accept},
		Rules: []Rule[researcher, draft, struct{}]{
			Permit("publish-a-draft", publish, "anyone in the pipeline may publish a draft", always),
			Forbid("person-approves-publish", publish, verdict.Ask, "a person approves every publish", always),
			Forbid("uncited-draft", publish, verdict.Deny, "a draft with no citation is never published",
				func(_ context.Context, r draftRequest) (bool, error) { return r.Target.Citations == 0, nil }),
			Permit("accept-a-draft", accept, "anyone in the pipeline may accept a draft", always),
			Forbid("self-accept", accept, verdict.Deny, "an author never accepts their own draft",
				func(_ context.Context, r draftRequest) (bool, error) { return r.Principal.Name == r.Target.Author, nil }),
		},
	}
}

type researchPipelineSuite struct {
	suite.Suite
	set *RuleSet[researcher, draft, struct{}]
}

func TestResearchPipeline(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(researchPipelineSuite))
}

func (s *researchPipelineSuite) SetupTest() {
	set, err := New(pipeline())
	s.Require().NoError(err)
	s.set = set
}

func (s *researchPipelineSuite) judge(who string, act Act, d draft) verdict.Verdict {
	v, err := s.set.Judge(s.T().Context(), draftRequest{Principal: researcher{Name: who}, Act: act, Target: d})
	s.Require().NoError(err)
	return v
}

func (s *researchPipelineSuite) TestEveryPublishAsks() {
	v := s.judge("writer", publish, draft{Author: "writer", Citations: 3})
	s.Equal(verdict.Ask, v.Decision)
	s.Equal("person-approves-publish", v.Rule)
}

func (s *researchPipelineSuite) TestAnUncitedDraftIsDenied() {
	v := s.judge("writer", publish, draft{Author: "writer"})
	s.Equal(verdict.Deny, v.Decision)
	s.Equal("uncited-draft", v.Rule)
}

func (s *researchPipelineSuite) TestTheAuthorMayNotAcceptTheirOwnDraft() {
	v := s.judge("writer", accept, draft{Author: "writer", Citations: 3})
	s.Equal(verdict.Deny, v.Decision)
	s.Equal("self-accept", v.Rule)
}

func (s *researchPipelineSuite) TestAnotherResearcherMayAccept() {
	s.Equal(verdict.Verdict{}, s.judge("checker", accept, draft{Author: "writer", Citations: 3}))
}
