package model

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/suite"
)

type PriceContextSuite struct{ suite.Suite }

func TestPriceContext(t *testing.T) { suite.Run(t, new(PriceContextSuite)) }

func metered(tokens int64, id string) types.AgentFacts {
	return types.AgentFacts{ContextTokens: tokens, ContextModel: id, Billing: types.AgentBillingMetered}
}

func (s *PriceContextSuite) TestARepeatedContextIsPricedOnce() {
	table := tier.InEffect([]tier.Pricing{{ID: "m", CacheRead: 1}}, nil)
	floor, why := PriceContext(table, []types.AgentFacts{metered(1_000_000, "m"), metered(1_000_000, "m"), metered(2_000_000, "m")})
	s.Empty(why)
	assert.Equal(s.T(), contextFloor{Calls: 2, Tokens: 3_000_000, USD: 3}, floor)
}

func (s *PriceContextSuite) TestAModelTheTableDoesNotKnowIsAReason() {
	_, why := PriceContext(tier.Table{}, []types.AgentFacts{metered(1_000_000, "unpriced")})
	s.Contains(why, `model "unpriced" is not in the model table`)
}

func (s *PriceContextSuite) TestUnknownBillingIsAReason() {
	_, why := PriceContext(tier.Table{}, []types.AgentFacts{{ContextTokens: 1, ContextModel: "m"}})
	s.Contains(why, "billing is unknown")
}
