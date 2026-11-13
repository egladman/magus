package steps

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/agent"
)

type groupSuite struct{ suite.Suite }

func TestGroup(t *testing.T) { suite.Run(t, new(groupSuite)) }

func verdict(reason string) Rule {
	return func(context.Context, agent.StepRequest, hint.Gate) (types.GuardVerdict, error) {
		return types.GuardVerdict{Decision: types.GuardDeny, Reason: reason}, nil
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
	group.Set("refund", verdict("first"))
	group.Set("refund", verdict("second"))
	v, err := group.Rule("refund")(s.T().Context(), agent.StepRequest{}, hint.Gate{})
	s.Require().NoError(err)
	s.Equal("second", v.Reason)
}

func (s *groupSuite) TestKindsAreSorted() {
	var group Group
	group.Set("refund", verdict("r"))
	group.Set("egress", verdict("e"))
	s.Equal([]string{"egress", "refund"}, group.Kinds())
}

func (s *groupSuite) TestTheContextCarriesTheGroup() {
	group := &Group{}
	s.Same(group, GroupFromContext(WithGroup(s.T().Context(), group)))
	s.Nil(GroupFromContext(s.T().Context()))
}
