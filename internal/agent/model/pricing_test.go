package model

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/safetyscissors/budget"
)

var fixtureRows = []Row{
	{ID: "claude-opus-5-5", Window: 1_000_000, Price: budget.Price{Input: 4, Output: 20}},
	{ID: "claude-haiku-5-5", Window: 1_000_000, Price: budget.Price{Input: 0.1, CacheRead: 0.01, Output: 0.5,
		LongContext: &budget.PriceStep{Above: 100_000, Multiplier: 5}}},
	{ID: "claude-haiku-4-5", Window: 200_000, Price: budget.Price{Input: 1, Output: 5}},
}

type TableSuite struct{ suite.Suite }

func TestTable(t *testing.T) { suite.Run(t, new(TableSuite)) }

func (s *TableSuite) TestZeroTableHasNoFacts() {
	_, ok := Table{}.Lookup("claude-opus-5-5")
	s.False(ok, "no rows is no facts, never a free or zero-sized model")
	_, ok = Table{}.Price("claude-opus-5-5")
	s.False(ok)
	s.Empty(Table{}.Rows())
	s.Empty(InEffect(nil, nil).Rows())
}

// A row matches its own id and no other: every spelling a host adds around it is another
// model until a row names it.
func (s *TableSuite) TestLookupMatchesTheExactID() {
	table := InEffect(fixtureRows, nil)
	for _, id := range []string{"claude-opus-5-5", "CLAUDE-OPUS-5-5"} {
		f, ok := table.Lookup(id)
		s.Require().True(ok, id)
		s.Equal(fixtureRows[0], f, id)
	}
	for _, id := range []string{
		"", "claude-opus-5-5-20260401", "claude-opus-5-5[1m]", "us.anthropic.claude-opus-5-5-v1:0",
		"anthropic/claude-opus-5-5", "claude-opus-5", "claude-opus-5-55", "gpt-5", "opus",
	} {
		_, ok := table.Lookup(id)
		s.False(ok, id)
	}
}

func (s *TableSuite) TestATableIsAPricer() {
	var pricer budget.Pricer = InEffect(fixtureRows, nil)
	p, ok := pricer.Price("claude-haiku-5-5")
	s.Require().True(ok)
	s.Equal(fixtureRows[1].Price, p)
}

func (s *TableSuite) TestInEffectOverlaysAndAdds() {
	harness := []Row{
		{ID: "claude-opus-5-5", Window: 1_000_000, Price: budget.Price{Input: 4, Output: 20}},
		{ID: "claude-haiku-5-5", Window: 1_000_000, Price: budget.Price{Input: 0.1, CacheRead: 0.01, Output: 0.5,
			LongContext: &budget.PriceStep{Above: 100_000, Multiplier: 5}}},
	}
	overrides := []Row{
		{ID: "Claude-Haiku-5-5", Window: 2_000_000, Price: budget.Price{CacheRead: 0.02}},
		{ID: "claude-opus-5-5", Price: budget.Price{LongContext: &budget.PriceStep{Above: 200_000, Multiplier: 2}}},
		{ID: "claude-next-1", Window: 3_000_000, Price: budget.Price{LongContext: &budget.PriceStep{Above: 50_000, Multiplier: 2}}},
	}

	table := InEffect(harness, overrides)

	s.Equal([]Row{
		{ID: "claude-opus-5-5", Window: 1_000_000, Price: budget.Price{Input: 4, Output: 20,
			LongContext: &budget.PriceStep{Above: 200_000, Multiplier: 2}}},
		{ID: "claude-haiku-5-5", Window: 2_000_000, Price: budget.Price{Input: 0.1, CacheRead: 0.02, Output: 0.5,
			LongContext: &budget.PriceStep{Above: 100_000, Multiplier: 5}}},
		{ID: "claude-next-1", Window: 3_000_000, Price: budget.Price{LongContext: &budget.PriceStep{Above: 50_000, Multiplier: 2}}},
	}, table.Rows(), "an override's 0 field and nil LongContext keep the harness value")
	s.Equal(int64(1_000_000), harness[1].Window, "InEffect leaves its arguments alone")
	s.Equal("Claude-Haiku-5-5", overrides[0].ID, "InEffect leaves its arguments alone")
}

// Harness rows go in before agents.models, so a person's row wins field by field, its
// tier and alias included.
func (s *TableSuite) TestInEffectLayersHarnessRowsUnderAgentsModels() {
	table := InEffect(
		[]Row{{ID: "claude-opus-5-5", Tier: "design", Window: 1_000_000, Price: budget.Price{Input: 4, Output: 20}}},
		[]Row{{ID: "claude-opus-5-5", Alias: "opus", Price: budget.Price{Input: 3}}},
	)

	f, ok := table.Lookup("claude-opus-5-5")
	s.Require().True(ok)
	s.Equal(Row{ID: "claude-opus-5-5", Tier: "design", Alias: "opus", Window: 1_000_000, Price: budget.Price{Input: 3, Output: 20}}, f)
}
