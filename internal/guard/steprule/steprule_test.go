package steprule

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/agent"
)

type setSuite struct{ suite.Suite }

func TestSet(t *testing.T) { suite.Run(t, new(setSuite)) }

func verdict(reason string) Rule {
	return func(context.Context, agent.StepRequest, hint.Gate) (types.GuardVerdict, error) {
		return types.GuardVerdict{Decision: types.GuardDeny, Reason: reason}, nil
	}
}

func (s *setSuite) TestAKindWithNoRuleAnswersNil() {
	var set Set
	s.Nil(set.Rule("refund"))
	s.Nil(set.Kinds())
}

func (s *setSuite) TestANilSetHoldsNothing() {
	var set *Set
	s.Nil(set.Rule("refund"))
	s.Nil(set.Kinds())
}

func (s *setSuite) TestASecondSetReplacesTheFirst() {
	var set Set
	set.Set("refund", verdict("first"))
	set.Set("refund", verdict("second"))
	v, err := set.Rule("refund")(s.T().Context(), agent.StepRequest{}, hint.Gate{})
	s.Require().NoError(err)
	s.Equal("second", v.Reason)
}

func (s *setSuite) TestKindsAreSorted() {
	var set Set
	set.Set("refund", verdict("r"))
	set.Set("egress", verdict("e"))
	s.Equal([]string{"egress", "refund"}, set.Kinds())
}

func (s *setSuite) TestTheContextCarriesTheSet() {
	set := &Set{}
	s.Same(set, FromContext(WithSet(s.T().Context(), set)))
	s.Nil(FromContext(s.T().Context()))
}
