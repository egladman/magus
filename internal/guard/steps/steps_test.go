package steps

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/safetyscissors/step"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
)

type groupSuite struct{ suite.Suite }

func TestGroup(t *testing.T) { suite.Run(t, new(groupSuite)) }

func denyWith(reason string) Rule {
	return func(context.Context, step.Request, hint.Gate) (verdict.Verdict, error) {
		return verdict.Verdict{Decision: verdict.Deny, Reason: reason}, nil
	}
}

func (s *groupSuite) TestAKindWithNoRuleAnswersNil() {
	var group Group
	s.Nil(group.Rule("refund"))
	s.Nil(group.Kinds())
}

func (s *groupSuite) TestANilGroupHoldsNothing() {
	var group *Group
	s.Nil(group.Rule("refund"))
	s.Nil(group.Kinds())
}

func (s *groupSuite) TestASecondRuleReplacesTheFirst() {
	var group Group
	group.Set("refund", denyWith("first"))
	group.Set("refund", denyWith("second"))
	v, err := group.Rule("refund")(s.T().Context(), step.Request{}, hint.Gate{})
	s.Require().NoError(err)
	s.Equal("second", v.Reason)
}

func (s *groupSuite) TestKindsAreSorted() {
	var group Group
	group.Set("refund", denyWith("r"))
	group.Set("egress", denyWith("e"))
	s.Equal([]string{"egress", "refund"}, group.Kinds())
}

func (s *groupSuite) TestTheContextCarriesTheGroup() {
	group := &Group{}
	s.Same(group, GroupFromContext(WithGroup(s.T().Context(), group)))
	s.Nil(GroupFromContext(s.T().Context()))
}
